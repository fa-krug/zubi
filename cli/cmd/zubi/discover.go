package main

// `zubi config discover`: finds Zubi on the local network by multicast DNS. A Mac running Zubi
// advertises the service _zubi._tcp (TXT api=1, version=x.y.z) while Zubi listens on all
// interfaces.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const zubiService = "_zubi._tcp.local."

var (
	mdnsIPv4 = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	mdnsIPv6 = net.ParseIP("ff02::fb")
)

// mdnsPacket is one response, with the address it came from.
type mdnsPacket struct {
	Msg  dnsMessage
	From net.IP
}

// zubiInstance is one Zubi found on the network.
type zubiInstance struct {
	Name      string            `json:"name"`
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	Addresses []string          `json:"addresses"`
	Version   string            `json:"version,omitempty"`
	TXT       map[string]string `json:"txt"`
	URL       string            `json:"url"`
}

var (
	// mdnsBrowse asks the network for Zubi and returns the responses that arrive within wait.
	// Tests replace it.
	mdnsBrowse = browse
	// hostResolves says whether this machine can look up a .local name, so the saved URL can
	// use the Mac's name, which outlives its address. Tests replace it.
	hostResolves = func(host string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		return err == nil && len(addrs) > 0
	}
)

func runDiscover(cfg Config, path string, g globals, o options, stdout, stderr io.Writer) int {
	save := o.save || o.set["pick"]
	if o.set["pick"] && o.pick < 1 {
		fmt.Fprintln(stderr, "zubi config discover: --pick takes the number discover lists, from 1")
		return exitUsage
	}
	wait := 2 * time.Second
	if o.set["timeout"] {
		wait = g.timeout
	}
	packets, err := mdnsBrowse(wait)
	if err != nil {
		fmt.Fprintln(stderr, "zubi config discover:", err)
		return exitError
	}
	found := collectInstances(packets)
	for i := range found {
		found[i].URL = instanceURL(found[i], hostResolves)
	}
	if len(found) == 0 {
		fmt.Fprintln(stderr, "zubi config discover: no Zubi answered on this network within "+wait.String()+". "+
			"Zubi announces itself only while it listens on all interfaces (Zubi › Settings › Server › Network: "+
			"All interfaces), and only to the local network. Otherwise use an SSH tunnel "+
			"(ssh -N -L 8787:localhost:8787 user@mac.local) and the default URL, or set it with "+
			"`zubi config set url`. A longer --timeout can help on a busy network.")
		return exitNotFound
	}

	if g.json {
		data, _ := json.MarshalIndent(found, "", "  ")
		fmt.Fprintln(stdout, string(data))
	} else {
		for i, z := range found {
			line := fmt.Sprintf("%d  %s", i+1, z.Name)
			if z.Version != "" {
				line += "  " + "Zubi " + z.Version
			}
			if api := z.TXT["api"]; api != "1" {
				line += fmt.Sprintf("  (API %q; this zubi speaks API 1)", api)
			}
			fmt.Fprintln(stdout, line)
			fmt.Fprintf(stdout, "   %s", z.URL)
			if len(z.Addresses) > 0 {
				fmt.Fprintf(stdout, "  (%s)", strings.Join(z.Addresses, ", "))
			}
			fmt.Fprintln(stdout)
		}
	}
	// With --json, stdout is the list alone; what follows goes to stderr.
	notes := stdout
	if g.json {
		notes = stderr
	}
	if !save {
		if len(found) == 1 {
			fmt.Fprintln(notes, "Run `zubi config discover --save` to use it.")
		} else {
			fmt.Fprintln(notes, "Run `zubi config discover --pick N` to use one.")
		}
		return exitOK
	}
	pick := 1
	switch {
	case o.set["pick"] && o.pick > len(found):
		fmt.Fprintf(stderr, "zubi config discover: --pick %d, but only %d Zubi answered\n", o.pick, len(found))
		return exitUsage
	case o.set["pick"]:
		pick = o.pick
	case len(found) > 1:
		fmt.Fprintf(stderr, "zubi config discover: %d Zubi answered; choose one with --pick N\n", len(found))
		return exitUsage
	}
	cfg.URL = found[pick-1].URL
	if err := saveConfig(path, cfg); err != nil {
		fmt.Fprintln(stderr, "zubi config:", err)
		return exitError
	}
	fmt.Fprintf(notes, "Saved url %s to %s\n", cfg.URL, path)
	return exitOK
}

// collectInstances reads the instances of _zubi._tcp out of the responses: a PTR names each,
// its SRV gives host and port, its TXT the details, and A/AAAA records the host's addresses. A
// record with TTL 0 is a goodbye, so the instance is going away.
func collectInstances(packets []mdnsPacket) []zubiInstance {
	var records []dnsRecord
	senders := map[string][]net.IP{} // instance → the addresses its answers came from
	var names []string               // as first written; compared ignoring case
	seen := map[string]bool{}
	for _, p := range packets {
		all := slices.Concat(p.Msg.Answers, p.Msg.Authority, p.Msg.Additional)
		records = append(records, all...)
		for _, r := range all {
			if r.Type == dnsTypePTR && sameName(r.Name, zubiService) && r.Class&^dnsClassTopBit == dnsClassIN {
				key := strings.ToLower(r.Target)
				if r.TTL == 0 {
					continue
				}
				if !seen[key] {
					seen[key] = true
					names = append(names, r.Target)
				}
				if p.From != nil {
					senders[key] = append(senders[key], p.From)
				}
			}
		}
	}
	gone := map[string]bool{}
	for _, r := range records {
		if r.Type == dnsTypePTR && sameName(r.Name, zubiService) && r.TTL == 0 {
			gone[strings.ToLower(r.Target)] = true
		}
	}

	var found []zubiInstance
	for _, name := range names {
		key := strings.ToLower(name)
		if gone[key] {
			continue
		}
		z := zubiInstance{Name: firstLabel(name), TXT: map[string]string{}}
		var host string
		for _, r := range records {
			if !sameName(r.Name, key) || r.TTL == 0 {
				continue
			}
			switch r.Type {
			case dnsTypeSRV:
				host, z.Port = r.Target, int(r.Port)
			case dnsTypeTXT:
				for _, entry := range r.Text {
					k, v, _ := strings.Cut(entry, "=")
					if k != "" {
						z.TXT[strings.ToLower(k)] = v
					}
				}
			}
		}
		if host == "" || z.Port == 0 {
			continue // without its SRV record there is no port to connect to
		}
		z.Host = strings.TrimSuffix(host, ".")
		z.Version = z.TXT["version"]
		var addrs []netip.Addr
		for _, r := range records {
			if (r.Type == dnsTypeA || r.Type == dnsTypeAAAA) && r.TTL > 0 && sameName(r.Name, host) {
				if a, ok := netip.AddrFromSlice(r.IP); ok {
					addrs = append(addrs, a.Unmap())
				}
			}
		}
		if len(addrs) == 0 {
			for _, ip := range senders[key] {
				if a, ok := netip.AddrFromSlice(ip); ok {
					addrs = append(addrs, a.Unmap())
				}
			}
		}
		slices.SortStableFunc(addrs, func(a, b netip.Addr) int { return addrRank(a) - addrRank(b) })
		for _, a := range addrs {
			if s := a.String(); !slices.Contains(z.Addresses, s) {
				z.Addresses = append(z.Addresses, s)
			}
		}
		found = append(found, z)
	}
	return found
}

// addrRank orders addresses for display and for the URL: IPv4, then routable IPv6, then
// link-local IPv6 (useless without a zone), then loopback (only reachable from the Mac itself).
func addrRank(a netip.Addr) int {
	switch {
	case a.IsLoopback():
		return 3
	case a.IsLinkLocalUnicast():
		return 2
	case a.Is4():
		return 0
	}
	return 1
}

// instanceURL prefers the Mac's .local name when this machine can resolve it, since the name
// survives a new address; otherwise its first usable address.
func instanceURL(z zubiInstance, resolves func(string) bool) string {
	port := strconv.Itoa(z.Port)
	if z.Host != "" && resolves(z.Host) {
		return "http://" + net.JoinHostPort(z.Host, port)
	}
	for _, s := range z.Addresses {
		if a, err := netip.ParseAddr(s); err == nil && addrRank(a) != 2 {
			return "http://" + net.JoinHostPort(s, port)
		}
	}
	return "http://" + net.JoinHostPort(z.Host, port)
}

// followUp asks for what the first answers left out: the SRV and TXT records of instances
// without them, and the addresses of hosts without any.
func followUp(packets []mdnsPacket) []dnsQuestion {
	var records []dnsRecord
	for _, p := range packets {
		records = append(records, slices.Concat(p.Msg.Answers, p.Msg.Authority, p.Msg.Additional)...)
	}
	has := func(typ uint16, name string) bool {
		return slices.ContainsFunc(records, func(r dnsRecord) bool { return r.Type == typ && sameName(r.Name, name) })
	}
	questions := []dnsQuestion{{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN | dnsClassTopBit}}
	asked := map[string]bool{}
	for _, r := range records {
		if r.Type != dnsTypePTR || !sameName(r.Name, zubiService) || r.TTL == 0 {
			continue
		}
		if key := strings.ToLower(r.Target); !asked[key] && !has(dnsTypeSRV, r.Target) {
			asked[key] = true
			questions = append(questions,
				dnsQuestion{Name: r.Target, Type: dnsTypeSRV, Class: dnsClassIN | dnsClassTopBit},
				dnsQuestion{Name: r.Target, Type: dnsTypeTXT, Class: dnsClassIN | dnsClassTopBit})
		}
	}
	return questions
}

// browse sends a one-shot mDNS query (RFC 6762 §5.1) from an ephemeral port, so responders
// answer it directly, over IPv4 and over IPv6 on each interface; asks again a third of the way
// through for anything missing; and collects responses until wait is up.
func browse(wait time.Duration) ([]mdnsPacket, error) {
	var conns []*net.UDPConn
	if c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero}); err == nil {
		conns = append(conns, c)
	}
	if c, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified}); err == nil {
		conns = append(conns, c)
	}
	if len(conns) == 0 {
		return nil, errors.New("can't open a UDP socket for multicast DNS")
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	var ifaces []net.Interface
	if all, err := net.Interfaces(); err == nil {
		for _, i := range all {
			if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagMulticast != 0 {
				ifaces = append(ifaces, i)
			}
		}
	}

	id := uint16(rand.IntN(0x10000))
	sent := 0
	send := func(questions []dnsQuestion) {
		query, err := dnsMessage{ID: id, Questions: questions}.pack()
		if err != nil {
			return
		}
		for _, c := range conns {
			if c.LocalAddr().(*net.UDPAddr).IP.To4() != nil {
				if _, err := c.WriteToUDP(query, mdnsIPv4); err == nil {
					sent++
				}
				continue
			}
			for _, i := range ifaces {
				if _, err := c.WriteToUDP(query, &net.UDPAddr{IP: mdnsIPv6, Port: 5353, Zone: i.Name}); err == nil {
					sent++
				}
			}
		}
	}

	deadline := time.Now().Add(wait)
	var mu sync.Mutex
	var packets []mdnsPacket
	var wg sync.WaitGroup
	for _, c := range conns {
		c.SetReadDeadline(deadline)
		wg.Add(1)
		go func(c *net.UDPConn) {
			defer wg.Done()
			buf := make([]byte, 9000)
			for {
				n, from, err := c.ReadFromUDP(buf)
				if err != nil {
					return
				}
				msg, err := unpack(buf[:n])
				if err != nil || msg.Flags&dnsFlagResponse == 0 {
					continue
				}
				mu.Lock()
				packets = append(packets, mdnsPacket{Msg: msg, From: from.IP})
				mu.Unlock()
			}
		}(c)
	}
	send([]dnsQuestion{{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN | dnsClassTopBit}})
	if sent == 0 {
		return nil, errors.New("can't send a multicast DNS query; is this machine on a network?")
	}
	time.Sleep(wait / 3)
	mu.Lock()
	again := followUp(packets)
	mu.Unlock()
	send(again)
	wg.Wait()
	return packets, nil
}
