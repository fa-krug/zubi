package main

import (
	"slices"
	"strings"
	"testing"
)

// These stop short of systemd: a CI runner has passwordless sudo, so an install would be real.

func TestServiceUnit(t *testing.T) {
	proxy := options{behindProxy: true, listen: "127.0.0.1:8080"}
	unit := serviceUnit("relay", "/usr/local/bin/zubi", "pi", []string{"--behind-tls-proxy", "--listen", "127.0.0.1:8080"}, proxy)
	for _, want := range []string{
		"ExecStart=/usr/local/bin/zubi relay --behind-tls-proxy --listen 127.0.0.1:8080\n",
		"User=pi\n",
		"Restart=on-failure\n",
		"RestartSec=5s\n",
		"After=network-online.target\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "AmbientCapabilities") {
		t.Errorf("port 8080 needs no capability:\n%s", unit)
	}

	// With its own certificate on 443: the capability to bind it, and a slow retry, as Let's
	// Encrypt counts failed checks.
	own := serviceUnit("relay", "/usr/local/bin/zubi", "pi", nil, options{})
	if !strings.Contains(own, "AmbientCapabilities=CAP_NET_BIND_SERVICE\n") || !strings.Contains(own, "RestartSec=15min\n") {
		t.Errorf("own certificate on 443:\n%s", own)
	}
	if root := serviceUnit("relay", "/usr/local/bin/zubi", "root", nil, options{}); strings.Contains(root, "AmbientCapabilities") {
		t.Errorf("root needs no capability:\n%s", root)
	}
	if client := serviceUnit("serve", "/usr/local/bin/zubi", "pi", nil, options{}); !strings.Contains(client, "ExecStart=/usr/local/bin/zubi serve\n") ||
		strings.Contains(client, "AmbientCapabilities") || !strings.Contains(client, "Description=Zubi client") {
		t.Errorf("client:\n%s", client)
	}
	if serviceName("serve") != "zubi-client" || serviceName("relay") != "zubi-relay" {
		t.Errorf("names: %s, %s", serviceName("serve"), serviceName("relay"))
	}
}

func TestSystemdQuote(t *testing.T) {
	for in, want := range map[string]string{
		"--listen":             "--listen",
		"/home/pi/my dir/zubi": `"/home/pi/my dir/zubi"`,
		`say "hi"`:             `"say \"hi\""`,
		"100%":                 "100%%",
		"$HOME":                "$$HOME",
		"":                     `""`,
	} {
		if got := systemdQuote(in); got != want {
			t.Errorf("systemdQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestServiceFlagsAreNotRecorded(t *testing.T) {
	got := withoutServiceFlags([]string{"--behind-tls-proxy", "--install-service", "--listen", ":8080", "-uninstall-service=true"})
	if want := []string{"--behind-tls-proxy", "--listen", ":8080"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestServiceRefusals(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{
		{"relay", "--install-service", "--uninstall-service"},
		{"relay", "--install-service", "--no-tls", "--behind-tls-proxy"}, // a relay that couldn't start
		{"serve", "--install-service"},                                   // not paired
	} {
		code, out, errOut := runZubi(t, "", args...)
		if code != exitUsage || out != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want a usage error", args, code, out, errOut)
		}
	}
	if _, _, errOut := runZubi(t, "", "serve", "--install-service"); !strings.Contains(errOut, "zubi pair") {
		t.Errorf("tunnel without a pairing: %q", errOut)
	}
}
