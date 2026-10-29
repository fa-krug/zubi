package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// noService keeps the tests away from systemd: no service can be set up, and none is installed.
func noService(t *testing.T) {
	t.Helper()
	savedCan, savedPath := canHaveService, clientUnitPath
	canHaveService = func() bool { return false }
	clientUnitPath = filepath.Join(t.TempDir(), "zubi-client.service")
	t.Cleanup(func() { canHaveService, clientUnitPath = savedCan, savedPath })
}

func TestPairThenUseTheRelay(t *testing.T) {
	isolate(t)
	noService(t)
	f, code := relayedZubi(t)

	exit, out, errOut := runZubi(t, code+"\n", "pair", "-")
	if exit != exitOK || !strings.Contains(out, `Paired with your Mac as "Laptop", through ws://`) {
		t.Fatalf("pair: exit %d, stdout %q, stderr %q", exit, out, errOut)
	}
	// Every command now goes through the relay, with the pairing's key.
	if exit, out, errOut := runZubi(t, "", "lists", "--json"); exit != exitOK || !strings.Contains(out, `"lists"`) {
		t.Fatalf("lists through the relay: exit %d, %q, %q", exit, out, errOut)
	}
	if !contains(f.seen(), "/api/lists") {
		t.Errorf("the fake saw %v", f.seen())
	}
	// `zubi` on its own says so.
	exit, out, _ = runZubi(t, "")
	for _, want := range []string{`✓ Paired as "Pi", through ws://`, "· Commands connect each time", "✓ Your Mac answers"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if exit != exitOK {
		t.Errorf("status: exit %d", exit)
	}

	if exit, out, _ := runZubi(t, "", "unpair"); exit != exitOK || !strings.Contains(out, "Unpaired") {
		t.Fatalf("unpair: exit %d, %q", exit, out)
	}
	if cfg := readConfigIfAny(t); cfg.Relay != nil {
		t.Errorf("still paired: %+v", cfg.Relay)
	}
	if exit, _, errOut := runZubi(t, "", "lists"); exit != exitUsage || !strings.Contains(errOut, "not paired") {
		t.Errorf("lists after unpair: exit %d, %q", exit, errOut)
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
	noService(t)
	_, code := relayedZubi(t)
	p, _ := tunnel.ParsePairing(code, true)

	wrong := p
	wrong.Secret = "guessed"
	if exit, _, errOut := runZubi(t, "", "pair", wrong.String()); exit != exitUnauthorized || !strings.Contains(errOut, "not paired") {
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

// TestFirstRun: `zubi` on a computer that isn't paired asks for the code, pairs, sets up the
// service, and shows the status.
func TestFirstRun(t *testing.T) {
	isolate(t)
	noService(t)
	canHaveService = func() bool { return true }
	set := 0
	saved := setupClientService
	setupClientService = func(stdout, stderr io.Writer) int { set++; return exitOK }
	t.Cleanup(func() { setupClientService = saved })
	_, code := relayedZubi(t)

	exit, out, errOut := runZubi(t, code+"\n")
	for _, want := range []string{"isn't paired with a Mac yet", "Pairing code: ", `✓ Paired with your Mac as "Laptop"`,
		"Setting up the zubi-client service", "✓ Your Mac answers"} {
		if !strings.Contains(out, want) {
			t.Errorf("first run lacks %q:\n%s", want, out)
		}
	}
	if exit != exitOK || set != 1 {
		t.Errorf("exit %d, service set up %d times, stderr %q", exit, set, errOut)
	}
	if readConfigIfAny(t).Relay == nil {
		t.Error("not saved")
	}
}

func TestFirstRunWithoutACode(t *testing.T) {
	isolate(t)
	noService(t)
	exit, out, errOut := runZubi(t, "")
	if exit != exitUsage || !strings.Contains(out, "Pairing code:") || !strings.Contains(errOut, "no code given") {
		t.Errorf("exit %d, stdout %q, stderr %q", exit, out, errOut)
	}
}

// TestServe: the client service carries every command through its socket, and the status says so.
func TestServe(t *testing.T) {
	isolate(t)
	noService(t)
	f, code := relayedZubi(t)
	if exit, _, errOut := runZubi(t, code+"\n", "pair", "-"); exit != exitOK {
		t.Fatalf("pair: %q", errOut)
	}

	ctx, cancel := context.WithCancel(context.Background())
	saved := signalContext
	signalContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { signalContext = saved })
	out := &lockedBuffer{}
	codes := make(chan int, 1)
	go func() { codes <- run([]string{"serve"}, strings.NewReader(""), out, io.Discard) }()
	socket, _ := socketPath()
	for i := 0; !strings.Contains(out.String(), "Serving Zubi on "+socket); i++ {
		if i == 200 {
			t.Fatalf("serve didn't start: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info, err := os.Stat(socket); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("socket %v %v, want mode 0600", info, err)
	}

	before := len(f.seen())
	if exit, out, errOut := runZubi(t, "", "lists", "--json"); exit != exitOK || !strings.Contains(out, `"lists"`) {
		t.Fatalf("lists through the service: exit %d, %q, %q", exit, out, errOut)
	}
	if len(f.seen()) != before+1 {
		t.Errorf("the fake saw %v", f.seen())
	}
	if _, status, _ := runZubi(t, ""); !strings.Contains(status, "✓ The zubi-client service keeps the connection") {
		t.Errorf("status:\n%s", status)
	}
	cancel()
	if exit := <-codes; exit != exitOK {
		t.Errorf("exit %d after Ctrl-C", exit)
	}
	if _, err := os.Stat(socket); err == nil {
		t.Error("the socket outlived the service")
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
