package main

// A small DNS message encoder and decoder (RFC 1035), enough for multicast DNS service
// discovery (RFC 6762, RFC 6763): questions, and PTR, SRV, TXT, A and AAAA records, with name
// compression both ways.
//
// Names are written in presentation form with a trailing dot ("_zubi._tcp.local."). A label may
// hold any byte, so a dot inside one is escaped as `\.` and a backslash as `\\`; an instance name
// such as "Zubi on Mac.local" stays one label.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	dnsTypeA    uint16 = 1
	dnsTypePTR  uint16 = 12
	dnsTypeTXT  uint16 = 16
	dnsTypeAAAA uint16 = 28
	dnsTypeSRV  uint16 = 33

	dnsClassIN uint16 = 1
	// In a question, the top bit of the class asks for a unicast answer (mDNS "QU"); in a record,
	// it is the cache-flush bit. Either way the class itself is the low 15 bits.
	dnsClassTopBit uint16 = 0x8000

	dnsFlagResponse uint16 = 0x8000
)

type dnsQuestion struct {
	Name  string
	Type  uint16
	Class uint16
}

type dnsRecord struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32

	Target                 string   // PTR and SRV
	Priority, Weight, Port uint16   // SRV
	Text                   []string // TXT, one entry per character-string
	IP                     net.IP   // A and AAAA
	Data                   []byte   // the raw data of any other type
}

type dnsMessage struct {
	ID         uint16
	Flags      uint16
	Questions  []dnsQuestion
	Answers    []dnsRecord
	Authority  []dnsRecord
	Additional []dnsRecord
}

// splitName turns a presentation name into its labels, undoing `\.` and `\\`.
func splitName(name string) ([]string, error) {
	if name == "." || name == "" {
		return nil, nil
	}
	var labels []string
	var label []byte
	escaped := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case escaped:
			label = append(label, c)
			escaped = false
		case c == '\\':
			escaped = true
		case c == '.':
			if len(label) == 0 {
				return nil, fmt.Errorf("dns: empty label in %q", name)
			}
			labels = append(labels, string(label))
			label = nil
		default:
			label = append(label, c)
		}
	}
	if escaped {
		return nil, fmt.Errorf("dns: %q ends in a lone backslash", name)
	}
	if len(label) > 0 {
		labels = append(labels, string(label))
	}
	for _, l := range labels {
		if len(l) > 63 {
			return nil, fmt.Errorf("dns: label %q is longer than 63 bytes", l)
		}
	}
	return labels, nil
}

func escapeLabel(label string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`).Replace(label)
}

// joinName is splitName's inverse.
func joinName(labels []string) string {
	if len(labels) == 0 {
		return "."
	}
	var b strings.Builder
	for _, l := range labels {
		b.WriteString(escapeLabel(l))
		b.WriteByte('.')
	}
	return b.String()
}

// firstLabel is a name's first label, unescaped: a DNS-SD instance's own name.
func firstLabel(name string) string {
	labels, err := splitName(name)
	if err != nil || len(labels) == 0 {
		return name
	}
	return labels[0]
}

// sameName compares names the way DNS does: ASCII letters ignore case.
func sameName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, ".")+".", strings.TrimSuffix(b, ".")+".")
}

// packer writes a message, compressing each name against the names written before it.
type packer struct {
	buf     []byte
	offsets map[string]int // lower-cased name suffix → where it was written
}

func (p *packer) u16(v uint16) { p.buf = binary.BigEndian.AppendUint16(p.buf, v) }
func (p *packer) u32(v uint32) { p.buf = binary.BigEndian.AppendUint32(p.buf, v) }

func (p *packer) name(name string) error {
	labels, err := splitName(name)
	if err != nil {
		return err
	}
	for i := range labels {
		suffix := strings.ToLower(joinName(labels[i:]))
		if off, ok := p.offsets[suffix]; ok {
			p.u16(0xC000 | uint16(off))
			return nil
		}
		if len(p.buf) < 0x3FFF {
			p.offsets[suffix] = len(p.buf)
		}
		p.buf = append(p.buf, byte(len(labels[i])))
		p.buf = append(p.buf, labels[i]...)
	}
	p.buf = append(p.buf, 0)
	return nil
}

func (p *packer) record(r dnsRecord) error {
	if err := p.name(r.Name); err != nil {
		return err
	}
	p.u16(r.Type)
	p.u16(r.Class)
	p.u32(r.TTL)
	lengthAt := len(p.buf)
	p.u16(0) // filled in below
	start := len(p.buf)
	switch r.Type {
	case dnsTypePTR:
		if err := p.name(r.Target); err != nil {
			return err
		}
	case dnsTypeSRV:
		p.u16(r.Priority)
		p.u16(r.Weight)
		p.u16(r.Port)
		if err := p.name(r.Target); err != nil {
			return err
		}
	case dnsTypeTXT:
		if len(r.Text) == 0 {
			p.buf = append(p.buf, 0) // an empty TXT record is one empty string
		}
		for _, s := range r.Text {
			if len(s) > 255 {
				return fmt.Errorf("dns: TXT string longer than 255 bytes")
			}
			p.buf = append(p.buf, byte(len(s)))
			p.buf = append(p.buf, s...)
		}
	case dnsTypeA:
		ip := r.IP.To4()
		if ip == nil {
			return fmt.Errorf("dns: A record for %s without an IPv4 address", r.Name)
		}
		p.buf = append(p.buf, ip...)
	case dnsTypeAAAA:
		ip := r.IP.To16()
		if ip == nil {
			return fmt.Errorf("dns: AAAA record for %s without an IPv6 address", r.Name)
		}
		p.buf = append(p.buf, ip...)
	default:
		p.buf = append(p.buf, r.Data...)
	}
	if len(p.buf)-start > 0xFFFF {
		return errors.New("dns: record data too long")
	}
	binary.BigEndian.PutUint16(p.buf[lengthAt:], uint16(len(p.buf)-start))
	return nil
}

// pack encodes m.
func (m dnsMessage) pack() ([]byte, error) {
	p := &packer{offsets: map[string]int{}}
	p.u16(m.ID)
	p.u16(m.Flags)
	for _, n := range []int{len(m.Questions), len(m.Answers), len(m.Authority), len(m.Additional)} {
		if n > 0xFFFF {
			return nil, errors.New("dns: too many entries")
		}
		p.u16(uint16(n))
	}
	for _, q := range m.Questions {
		if err := p.name(q.Name); err != nil {
			return nil, err
		}
		p.u16(q.Type)
		p.u16(q.Class)
	}
	for _, section := range [][]dnsRecord{m.Answers, m.Authority, m.Additional} {
		for _, r := range section {
			if err := p.record(r); err != nil {
				return nil, err
			}
		}
	}
	return p.buf, nil
}

var errShort = errors.New("dns: message too short")

// readName reads the name at off and returns it with the offset just after it. A compression
// pointer must point before the start of the labels it ends, so each jump goes further back and
// a loop is impossible.
func readName(msg []byte, off int) (string, int, error) {
	var labels []string
	next := -1 // where reading continues, once a pointer has been followed
	length := 0
	start := off // where the labels being read began
	for pos := off; ; {
		if pos >= len(msg) {
			return "", 0, errShort
		}
		b := int(msg[pos])
		switch b & 0xC0 {
		case 0x00:
			if b == 0 {
				if next < 0 {
					next = pos + 1
				}
				return joinName(labels), next, nil
			}
			if pos+1+b > len(msg) {
				return "", 0, errShort
			}
			length += b + 1
			if length > 255 {
				return "", 0, errors.New("dns: name longer than 255 bytes")
			}
			labels = append(labels, string(msg[pos+1:pos+1+b]))
			pos += 1 + b
		case 0xC0:
			if pos+1 >= len(msg) {
				return "", 0, errShort
			}
			target := int(binary.BigEndian.Uint16(msg[pos:]) & 0x3FFF)
			if target >= start {
				return "", 0, fmt.Errorf("dns: compression pointer at %d doesn't point back, to %d", pos, target)
			}
			if next < 0 {
				next = pos + 2
			}
			pos, start = target, target
		default:
			return "", 0, fmt.Errorf("dns: unsupported label type 0x%02x", b&0xC0)
		}
	}
}

// unpack decodes a message.
func unpack(msg []byte) (dnsMessage, error) {
	var m dnsMessage
	if len(msg) < 12 {
		return m, errShort
	}
	m.ID = binary.BigEndian.Uint16(msg[0:])
	m.Flags = binary.BigEndian.Uint16(msg[2:])
	counts := [4]int{}
	for i := range counts {
		counts[i] = int(binary.BigEndian.Uint16(msg[4+2*i:]))
	}
	off := 12
	for i := 0; i < counts[0]; i++ {
		name, next, err := readName(msg, off)
		if err != nil {
			return m, err
		}
		if next+4 > len(msg) {
			return m, errShort
		}
		m.Questions = append(m.Questions, dnsQuestion{
			Name:  name,
			Type:  binary.BigEndian.Uint16(msg[next:]),
			Class: binary.BigEndian.Uint16(msg[next+2:]),
		})
		off = next + 4
	}
	sections := []*[]dnsRecord{&m.Answers, &m.Authority, &m.Additional}
	for s, section := range sections {
		for i := 0; i < counts[s+1]; i++ {
			r, next, err := readRecord(msg, off)
			if err != nil {
				return m, err
			}
			*section = append(*section, r)
			off = next
		}
	}
	return m, nil
}

func readRecord(msg []byte, off int) (dnsRecord, int, error) {
	var r dnsRecord
	name, next, err := readName(msg, off)
	if err != nil {
		return r, 0, err
	}
	if next+10 > len(msg) {
		return r, 0, errShort
	}
	r.Name = name
	r.Type = binary.BigEndian.Uint16(msg[next:])
	r.Class = binary.BigEndian.Uint16(msg[next+2:])
	r.TTL = binary.BigEndian.Uint32(msg[next+4:])
	length := int(binary.BigEndian.Uint16(msg[next+8:]))
	start, end := next+10, next+10+length
	if end > len(msg) {
		return r, 0, errShort
	}
	data := msg[start:end]
	bad := func(what string) (dnsRecord, int, error) {
		return r, 0, fmt.Errorf("dns: malformed %s record for %s", what, name)
	}
	switch r.Type {
	case dnsTypePTR:
		target, after, err := readName(msg, start)
		if err != nil || after > end {
			return bad("PTR")
		}
		r.Target = target
	case dnsTypeSRV:
		if length < 7 {
			return bad("SRV")
		}
		r.Priority = binary.BigEndian.Uint16(data[0:])
		r.Weight = binary.BigEndian.Uint16(data[2:])
		r.Port = binary.BigEndian.Uint16(data[4:])
		target, after, err := readName(msg, start+6)
		if err != nil || after > end {
			return bad("SRV")
		}
		r.Target = target
	case dnsTypeTXT:
		for i := 0; i < len(data); {
			n := int(data[i])
			if i+1+n > len(data) {
				return bad("TXT")
			}
			r.Text = append(r.Text, string(data[i+1:i+1+n]))
			i += 1 + n
		}
	case dnsTypeA:
		if length != 4 {
			return bad("A")
		}
		r.IP = net.IP(append([]byte(nil), data...))
	case dnsTypeAAAA:
		if length != 16 {
			return bad("AAAA")
		}
		r.IP = net.IP(append([]byte(nil), data...))
	default:
		r.Data = append([]byte(nil), data...)
	}
	return r, end, nil
}
