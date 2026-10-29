package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeNetwork makes discover see these instances (name, host, port, address) instead of the network.
func fakeNetwork(t *testing.T, instances ...[4]string) *time.Duration {
	t.Helper()
	var waited time.Duration
	savedBrowse, savedResolves := mdnsBrowse, hostResolves
	t.Cleanup(func() { mdnsBrowse, hostResolves = savedBrowse, savedResolves })
	hostResolves = func(host string) bool { return host == "mac.local" }
	mdnsBrowse = func(wait time.Duration) ([]mdnsPacket, error) {
		waited = wait
		var packets []mdnsPacket
		for _, z := range instances {
			name := escapeLabel(z[0]) + "." + zubiService
			port := uint16(0)
			for _, c := range z[2] {
				port = port*10 + uint16(c-'0')
			}
			m := dnsMessage{
				Flags:   dnsFlagResponse,
				Answers: []dnsRecord{{Name: zubiService, Type: dnsTypePTR, Class: dnsClassIN, TTL: 4500, Target: name}},
				Additional: []dnsRecord{
					{Name: name, Type: dnsTypeSRV, Class: dnsClassIN, TTL: 120, Port: port, Target: z[1] + "."},
					{Name: name, Type: dnsTypeTXT, Class: dnsClassIN, TTL: 4500, Text: []string{"api=1", "version=1.4.0"}},
					{Name: z[1] + ".", Type: dnsTypeA, Class: dnsClassIN, TTL: 120, IP: net.ParseIP(z[3]).To4()},
				},
			}
			// Through the codec, as the network would deliver it.
			data, err := m.pack()
			if err != nil {
				t.Fatal(err)
			}
			back, err := unpack(data)
			if err != nil {
				t.Fatal(err)
			}
			packets = append(packets, mdnsPacket{Msg: back, From: net.ParseIP(z[3])})
		}
		return packets, nil
	}
	return &waited
}

var (
	macInstance    = [4]string{"Zubi on Mac", "mac.local", "8787", "192.168.1.20"}
	studioInstance = [4]string{"Zubi on Studio", "studio.local", "9000", "192.168.1.30"}
)

func TestDiscoverOne(t *testing.T) {
	path := isolate(t)
	waited := fakeNetwork(t, macInstance)
	code, out, errOut := runZubi(t, "", "config", "discover")
	want := "1  Zubi on Mac  Zubi 1.4.0\n   http://mac.local:8787  (192.168.1.20)\nRun `zubi config discover --save` to use it.\n"
	if code != exitOK || out != want {
		t.Errorf("exit %d, stdout %q (want %q), stderr %q", code, out, want, errOut)
	}
	if *waited != 2*time.Second {
		t.Errorf("waited %s, want the default 2s", *waited)
	}
	code, out, _ = runZubi(t, "", "config", "discover", "--save", "--timeout", "5s")
	if code != exitOK || !strings.HasSuffix(out, "Saved url http://mac.local:8787 to "+path+"\n") {
		t.Errorf("--save: exit %d, stdout %q", code, out)
	}
	if got := readConfig(t, path).URL; got != "http://mac.local:8787" {
		t.Errorf("saved url %q", got)
	}
	if *waited != 5*time.Second {
		t.Errorf("--timeout 5s waited %s", *waited)
	}
}

func TestDiscoverJSON(t *testing.T) {
	path := isolate(t)
	fakeNetwork(t, macInstance, studioInstance)
	code, out, errOut := runZubi(t, "", "config", "discover", "--json", "--pick", "2")
	if code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var found []zubiInstance
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		t.Fatalf("stdout isn't the JSON list: %q", out)
	}
	if len(found) != 2 || found[1].URL != "http://192.168.1.30:9000" || found[1].Version != "1.4.0" || found[1].TXT["api"] != "1" {
		t.Errorf("found %+v", found)
	}
	if !strings.Contains(errOut, "Saved url http://192.168.1.30:9000") {
		t.Errorf("with --json, the note goes to stderr: %q", errOut)
	}
	if got := readConfig(t, path).URL; got != "http://192.168.1.30:9000" {
		t.Errorf("saved url %q", got)
	}
}

func TestDiscoverChoosing(t *testing.T) {
	isolate(t)
	fakeNetwork(t, macInstance, studioInstance)
	for _, c := range []struct {
		args []string
		want int
		says string
	}{
		{[]string{"--save"}, exitUsage, "choose one with --pick N"},
		{[]string{"--pick", "3"}, exitUsage, "only 2 Zubi answered"},
		{[]string{"--pick", "0"}, exitUsage, "--pick takes the number"},
		{nil, exitOK, ""},
	} {
		code, out, errOut := runZubi(t, "", append([]string{"config", "discover"}, c.args...)...)
		if code != c.want || !strings.Contains(errOut, c.says) {
			t.Errorf("%v: exit %d (want %d), stderr %q", c.args, code, c.want, errOut)
		}
		if c.args == nil && !strings.Contains(out, "2  Zubi on Studio") {
			t.Errorf("stdout %q", out)
		}
	}
	if cfg := readConfigIfAny(t); cfg.URL != "" {
		t.Errorf("nothing should be saved, got %q", cfg.URL)
	}
}

func TestDiscoverNothing(t *testing.T) {
	isolate(t)
	fakeNetwork(t)
	code, out, errOut := runZubi(t, "", "config", "discover", "--save")
	if code != exitNotFound || out != "" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
	for _, hint := range []string{"no Zubi answered", "All interfaces", "SSH tunnel", "--timeout"} {
		if !strings.Contains(errOut, hint) {
			t.Errorf("stderr lacks %q: %q", hint, errOut)
		}
	}
}

func readConfigIfAny(t *testing.T) Config {
	t.Helper()
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
