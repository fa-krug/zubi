package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fa-krug/zubi/cli/internal/relay"
	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

// relayedZubi is the fake Zubi behind a real relay, served by the Go host as the Mac would.
// Its pairing code is for the key "pi", whose secret is the fake's key.
func relayedZubi(t *testing.T) (*fakeZubi, string) {
	t.Helper()
	allowPlainRelay = true
	t.Cleanup(func() { allowPlainRelay = false })
	f := newFakeZubi(t)
	r := relay.New(relay.Options{})
	relaySrv := httptest.NewServer(r.Handler())
	t.Cleanup(func() { r.Close(); relaySrv.Close() })
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	host := &tunnel.Host{
		Relay:  "ws" + strings.TrimPrefix(relaySrv.URL, "http"),
		Key:    key,
		Target: strings.TrimPrefix(f.URL, "http://"),
		Secret: func(id string) (string, bool) { return testKey, id == "pi" },
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan struct{})
	go host.Run(ctx, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the host didn't register")
	}
	p := tunnel.Pairing{Relay: host.Relay, Room: host.Room(), KeyID: "pi", Secret: testKey, Name: "Pi"}
	return f, p.String()
}

func TestPairThenUseTheRelay(t *testing.T) {
	isolate(t)
	f, code := relayedZubi(t)

	exit, out, errOut := runZubi(t, code+"\n", "pair", "-")
	if exit != exitOK || !strings.Contains(out, "Paired: Zubi answers through ws://") {
		t.Fatalf("pair: exit %d, stdout %q, stderr %q", exit, out, errOut)
	}
	// Every command now goes through the relay, with the pairing's key.
	if exit, out, errOut := runZubi(t, "", "lists", "--json"); exit != exitOK || !strings.Contains(out, `"lists"`) {
		t.Fatalf("lists through the relay: exit %d, %q, %q", exit, out, errOut)
	}
	if exit, out, _ := runZubi(t, "", "health"); exit != exitOK || !strings.Contains(out, "at the relay ws://") {
		t.Errorf("health names the relay: %q", out)
	}
	if exit, out, _ := runZubi(t, "", "config"); exit != exitOK || !strings.Contains(out, "relay ws://") {
		t.Errorf("config shows the relay: %q", out)
	}
	if exit, _, errOut := runZubi(t, "", "feed", "Work"); exit != exitUsage || !strings.Contains(errOut, "zubi tunnel") {
		t.Errorf("feed through the relay: exit %d, %q", exit, errOut)
	}
	// A URL on the command line wins over the pairing.
	if exit, _, _ := runZubi(t, "", "lists", "--url", "http://127.0.0.1:1"); exit != exitUnreachable {
		t.Errorf("--url over the relay: exit %d", exit)
	}
	if !contains(f.seen(), "/api/lists") {
		t.Errorf("the fake saw %v", f.seen())
	}

	if exit, _, _ := runZubi(t, "", "config", "unset", "relay"); exit != exitOK {
		t.Fatal("unset relay")
	}
	if _, out, _ := runZubi(t, "", "config"); strings.Contains(out, "relay") {
		t.Errorf("relay still set: %q", out)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestPairRefusals(t *testing.T) {
	isolate(t)
	_, code := relayedZubi(t)
	p, _ := tunnel.ParsePairing(code, true)

	wrong := p
	wrong.Secret = "guessed"
	if exit, _, errOut := runZubi(t, "", "pair", wrong.String()); exit != exitUnauthorized || !strings.Contains(errOut, "not saved") {
		t.Errorf("a wrong secret: exit %d, %q", exit, errOut)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	noMac := p
	noMac.Room = (&tunnel.Host{Key: other}).Room()
	if exit, _, errOut := runZubi(t, "", "pair", noMac.String()); exit != exitUnreachable || !strings.Contains(errOut, "isn't connected") {
		t.Errorf("no Mac in the room: exit %d, %q", exit, errOut)
	}
	if exit, _, _ := runZubi(t, "", "pair", "nonsense"); exit != exitUsage {
		t.Errorf("not a code: exit %d", exit)
	}
	allowPlainRelay = false
	if exit, _, errOut := runZubi(t, "", "pair", code); exit != exitUsage || !strings.Contains(errOut, "ws://") {
		t.Errorf("a plain relay: exit %d, %q", exit, errOut)
	}
	if cfg := readConfigIfAny(t); cfg.Relay != nil {
		t.Errorf("a failed pairing was saved: %+v", cfg.Relay)
	}
}

func TestTunnel(t *testing.T) {
	isolate(t)
	f, code := relayedZubi(t)
	if exit, _, errOut := runZubi(t, "", "pair", code); exit != exitOK {
		t.Fatalf("pair: %q", errOut)
	}

	ctx, cancel := context.WithCancel(context.Background())
	saved := signalContext
	signalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { signalContext = saved })
	out := &lockedBuffer{}
	codes := make(chan int, 1)
	go func() {
		codes <- run([]string{"tunnel", "--listen", "127.0.0.1:0"}, strings.NewReader(""), out, io.Discard)
	}()
	serving := regexp.MustCompile(`Serving Zubi at (http://\S+),`)
	var base string
	for i := 0; base == ""; i++ {
		if m := serving.FindStringSubmatch(out.String()); m != nil {
			base = m[1]
		} else if i == 200 {
			t.Fatalf("the tunnel didn't start: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Any program can use it, with the key, as if Zubi were local.
	req, _ := http.NewRequest("GET", base+"/api/lists", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("through the tunnel: %d", res.StatusCode)
	}
	if exit, _, _ := runZubi(t, "", "lists", "--url", base, "--key", testKey); exit != exitOK {
		t.Errorf("zubi --url through the tunnel: exit %d", exit)
	}
	if n := len(f.seen()); n < 2 {
		t.Errorf("the fake saw %d requests", n)
	}
	cancel()
	if exit := <-codes; exit != exitOK {
		t.Errorf("exit %d after Ctrl-C", exit)
	}
}

func TestTunnelNeedsAPairing(t *testing.T) {
	isolate(t)
	if exit, _, errOut := runZubi(t, "", "tunnel"); exit != exitUsage || !strings.Contains(errOut, "zubi pair") {
		t.Errorf("exit %d, %q", exit, errOut)
	}
}

// TestWatchThroughTheRelay: a stream that stays open works through the relay too.
func TestWatchThroughTheRelay(t *testing.T) {
	isolate(t)
	f, code := relayedZubi(t)
	if exit, _, errOut := runZubi(t, "", "pair", code); exit != exitOK {
		t.Fatalf("pair: %q", errOut)
	}
	f.setStreams("event: change\ndata: " + changeReminders + "\n\n")
	stdout, _, stop := watching(t, "--json")
	eventually(t, "the change", func() bool { return strings.Contains(stdout.String(), changeReminders) })
	if code := stop(); code != exitOK {
		t.Errorf("exit %d", code)
	}
}
