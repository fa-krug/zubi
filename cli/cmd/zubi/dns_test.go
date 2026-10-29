package main

import (
	"bytes"
	"encoding/hex"
	"net"
	"reflect"
	"strings"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	var clean strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line, _, _ = strings.Cut(line, "#")
		clean.WriteString(strings.Join(strings.Fields(line), ""))
	}
	b, err := hex.DecodeString(clean.String())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// zubiAnswer is what a Mac running Zubi sends, written out byte by byte. Every name after the
// first is compressed: a pointer, or labels ending in one.
const zubiAnswer = `
0000 8400 0000 0001 0000 0004                 # id 0, response + authoritative, 1 answer, 4 additional
055f7a756269 045f746370 056c6f63616c 00       # @12 _zubi._tcp.local.   (@23 is local.)
000c 0001 00001194 000e                       # PTR IN, TTL 4500, 14 bytes
0b5a756269206f6e204d6163 c00c                 # @40 "Zubi on Mac" + -> @12
c028 0021 8001 00000078 000c                  # -> @40; SRV, cache-flush IN, TTL 120, 12 bytes
0000 0000 2253                                #   priority 0, weight 0, port 8787
036d6163 c017                                 # @72 mac + -> @23 (local.)
c028 0010 8001 00001194 0014                  # -> @40; TXT, 20 bytes
056170693d31                                  #   "api=1"
0d76657273696f6e3d312e342e30                  #   "version=1.4.0"
c048 0001 8001 00000078 0004 c0a80114         # -> @72 (mac.local.); A 192.168.1.20
c048 001c 8001 00000078 0010 fe800000000000000000000000000001  # AAAA fe80::1
`

func TestDNSDecodeCompressed(t *testing.T) {
	packet := unhex(t, zubiAnswer)
	if len(packet) != 154 {
		t.Fatalf("the crafted packet is %d bytes; the offsets in its comments assume 154", len(packet))
	}
	m, err := unpack(packet)
	if err != nil {
		t.Fatal(err)
	}
	if m.Flags != 0x8400 || len(m.Questions) != 0 || len(m.Answers) != 1 || len(m.Additional) != 4 {
		t.Fatalf("header: %+v", m)
	}
	ptr := m.Answers[0]
	if ptr.Name != "_zubi._tcp.local." || ptr.Type != dnsTypePTR || ptr.TTL != 4500 || ptr.Target != "Zubi on Mac._zubi._tcp.local." {
		t.Errorf("PTR %+v", ptr)
	}
	srv, txt, a, aaaa := m.Additional[0], m.Additional[1], m.Additional[2], m.Additional[3]
	if srv.Name != "Zubi on Mac._zubi._tcp.local." || srv.Class != dnsClassIN|dnsClassTopBit || srv.Port != 8787 || srv.Target != "mac.local." {
		t.Errorf("SRV %+v", srv)
	}
	if !reflect.DeepEqual(txt.Text, []string{"api=1", "version=1.4.0"}) {
		t.Errorf("TXT %+v", txt)
	}
	if a.Name != "mac.local." || !a.IP.Equal(net.ParseIP("192.168.1.20")) {
		t.Errorf("A %+v", a)
	}
	if aaaa.Name != "mac.local." || !aaaa.IP.Equal(net.ParseIP("fe80::1")) {
		t.Errorf("AAAA %+v", aaaa)
	}

	found := collectInstances([]mdnsPacket{{Msg: m, From: net.ParseIP("192.168.1.20")}})
	want := []zubiInstance{{
		Name: "Zubi on Mac", Host: "mac.local", Port: 8787,
		Addresses: []string{"192.168.1.20", "fe80::1"},
		Version:   "1.4.0", TXT: map[string]string{"api": "1", "version": "1.4.0"},
	}}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("collectInstances:\n%+v\nwant\n%+v", found, want)
	}
}

func TestDNSEncodeQuery(t *testing.T) {
	got, err := dnsMessage{ID: 0x1234, Questions: []dnsQuestion{{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN | dnsClassTopBit}}}.pack()
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, `
1234 0000 0001 0000 0000 0000                 # id, a query, one question
055f7a756269 045f746370 056c6f63616c 00       # _zubi._tcp.local.
000c 8001                                     # PTR, IN with the unicast-response bit
`)
	if !bytes.Equal(got, want) {
		t.Errorf("query\n%x\nwant\n%x", got, want)
	}
}

func TestDNSRoundTrip(t *testing.T) {
	m := dnsMessage{
		ID: 7, Flags: dnsFlagResponse,
		Questions: []dnsQuestion{{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN}},
		Answers: []dnsRecord{
			{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN, TTL: 4500, Target: `Zubi\.v2 on Mac._zubi._tcp.local.`},
		},
		Authority: []dnsRecord{{Name: "local.", Type: 99, Class: dnsClassIN, TTL: 1, Data: []byte{1, 2, 3}}},
		Additional: []dnsRecord{
			{Name: `Zubi\.v2 on Mac._zubi._tcp.local.`, Type: dnsTypeSRV, Class: dnsClassIN | dnsClassTopBit, TTL: 120, Priority: 1, Weight: 2, Port: 8787, Target: "MAC.local."},
			{Name: `Zubi\.v2 on Mac._zubi._tcp.local.`, Type: dnsTypeTXT, Class: dnsClassIN, TTL: 120, Text: []string{"api=1", ""}},
			{Name: "MAC.local.", Type: dnsTypeA, Class: dnsClassIN, TTL: 120, IP: net.IPv4(10, 0, 0, 2).To4()},
			{Name: "mac.local.", Type: dnsTypeAAAA, Class: dnsClassIN, TTL: 120, IP: net.ParseIP("2001:db8::2")},
		},
	}
	packed, err := m.pack()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(packed, []byte("_zubi")) != 1 || bytes.Count(packed, []byte("local")) != 1 {
		t.Errorf("names should be written once and pointed to after that: %x", packed)
	}
	back, err := unpack(packed)
	if err != nil {
		t.Fatal(err)
	}
	// Compression matches names ignoring case, so the last record points at "MAC.local.".
	m.Additional[3].Name = "MAC.local."
	if !reflect.DeepEqual(back, m) {
		t.Errorf("round trip:\n%+v\nwant\n%+v", back, m)
	}
	if got := firstLabel(back.Answers[0].Target); got != "Zubi.v2 on Mac" {
		t.Errorf("firstLabel = %q", got)
	}
	// An empty TXT record is one empty string on the wire.
	empty, _ := dnsMessage{Answers: []dnsRecord{{Name: "x.local.", Type: dnsTypeTXT}}}.pack()
	if back, err := unpack(empty); err != nil || !reflect.DeepEqual(back.Answers[0].Text, []string{""}) {
		t.Errorf("empty TXT: %v %v", back.Answers, err)
	}
}

func TestDNSRejectsMalformed(t *testing.T) {
	header := "0000 8400 0001 0000 0000 0000"
	cases := map[string]string{
		"pointer to itself":     header + " c00c 000c 0001",
		"pointer forward":       header + " c00e 000c 0001",
		"pointer loop":          header + " 0161 c00c 000c 0001", // @12 "a" then -> @12
		"truncated question":    header + " 05 5f7a75",
		"truncated header":      "0000 8400 0001",
		"extended label type":   header + " 40 000c 0001",
		"pointer cut short":     header + " c0",
		"name over 255 bytes":   header + " " + strings.Repeat("3f"+strings.Repeat("61", 63), 5) + " 00 000c 0001",
		"record data too long":  "0000 8400 0000 0001 0000 0000 00 0010 0001 00000078 00ff 0161",
		"TXT string too long":   "0000 8400 0000 0001 0000 0000 00 0010 0001 00000078 0002 0561",
		"A of three bytes":      "0000 8400 0000 0001 0000 0000 00 0001 0001 00000078 0003 0a0000",
		"SRV without a target":  "0000 8400 0000 0001 0000 0000 00 0021 0001 00000078 0006 000000002253",
		"PTR past its data":     "0000 8400 0000 0001 0000 0000 00 000c 0001 00000078 0001 0161 00",
		"answer count too high": "0000 8400 0000 0002 0000 0000 00 0001 0001 00000078 0004 0a000001",
	}
	for name, packet := range cases {
		if _, err := unpack(unhex(t, packet)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for name, m := range map[string]dnsMessage{
		"label over 63 bytes": {Questions: []dnsQuestion{{Name: strings.Repeat("a", 64) + ".local."}}},
		"empty label":         {Questions: []dnsQuestion{{Name: "a..local."}}},
		"A without IPv4":      {Answers: []dnsRecord{{Name: "a.local.", Type: dnsTypeA, IP: net.ParseIP("::1")}}},
	} {
		if _, err := m.pack(); err == nil {
			t.Errorf("pack %s: want an error", name)
		}
	}
}

func TestCollectInstances(t *testing.T) {
	ptr := func(instance string, ttl uint32) dnsRecord {
		return dnsRecord{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN, TTL: ttl, Target: instance + "._zubi._tcp.local."}
	}
	srv := func(instance, host string, port uint16) dnsRecord {
		return dnsRecord{Name: instance + "._zubi._tcp.local.", Type: dnsTypeSRV, Class: dnsClassIN, TTL: 120, Port: port, Target: host}
	}
	packets := []mdnsPacket{
		// Answered over IPv4 and again over IPv6: one instance. No A record, so the sender's address.
		{From: net.ParseIP("10.0.0.5"), Msg: dnsMessage{Answers: []dnsRecord{ptr("Office", 4500)}, Additional: []dnsRecord{srv("Office", "office.local.", 9000)}}},
		{From: net.ParseIP("fe80::5"), Msg: dnsMessage{Answers: []dnsRecord{ptr("OFFICE", 4500)}}},
		// No SRV yet: not listed, but asked for.
		{From: net.ParseIP("10.0.0.6"), Msg: dnsMessage{Answers: []dnsRecord{ptr("Studio", 4500)}}},
		// Saying goodbye.
		{From: net.ParseIP("10.0.0.7"), Msg: dnsMessage{Answers: []dnsRecord{ptr("Gone", 0)}, Additional: []dnsRecord{srv("Gone", "gone.local.", 8787)}}},
		// Another service in the same answer.
		{From: net.ParseIP("10.0.0.8"), Msg: dnsMessage{Answers: []dnsRecord{{Name: "_http._tcp.local.", Type: dnsTypePTR, TTL: 10, Target: "x._http._tcp.local."}}}},
	}
	found := collectInstances(packets)
	if len(found) != 1 || found[0].Name != "Office" || found[0].Port != 9000 || !reflect.DeepEqual(found[0].Addresses, []string{"10.0.0.5", "fe80::5"}) {
		t.Errorf("found %+v", found)
	}
	var asked []string
	for _, q := range followUp(packets) {
		asked = append(asked, q.Name+"/"+map[uint16]string{dnsTypePTR: "PTR", dnsTypeSRV: "SRV", dnsTypeTXT: "TXT"}[q.Type])
	}
	// Office's SRV came in the first answer (names compare ignoring case); Studio's is missing.
	if want := []string{"_zubi._tcp.local./PTR", "Studio._zubi._tcp.local./SRV", "Studio._zubi._tcp.local./TXT"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("follow-up asks %v, want %v", asked, want)
	}
}

func TestInstanceURL(t *testing.T) {
	z := zubiInstance{Host: "mac.local", Port: 8787, Addresses: []string{"192.168.1.20", "fe80::1"}}
	never := func(string) bool { return false }
	always := func(string) bool { return true }
	for _, c := range []struct {
		z        zubiInstance
		resolves func(string) bool
		want     string
	}{
		{z, always, "http://mac.local:8787"},
		{z, never, "http://192.168.1.20:8787"},
		{zubiInstance{Host: "mac.local", Port: 8787, Addresses: []string{"2001:db8::2"}}, never, "http://[2001:db8::2]:8787"},
		{zubiInstance{Host: "mac.local", Port: 8787, Addresses: []string{"fe80::1"}}, never, "http://mac.local:8787"},
	} {
		if got := instanceURL(c.z, c.resolves); got != c.want {
			t.Errorf("instanceURL(%+v) = %q, want %q", c.z, got, c.want)
		}
	}
}
