package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fa-krug/zubi/cli/internal/relay"
)

func TestPairingRoundTrip(t *testing.T) {
	p := Pairing{Relay: "wss://203.0.113.7", Room: strings.Repeat("A", 43), KeyID: "id", Secret: "s", Name: "Pi"}
	code := p.String()
	if !strings.HasPrefix(code, "zubi1:") {
		t.Fatalf("code %q", code)
	}
	got, err := ParsePairing("  "+code+"\n", false)
	if err != nil || got != p {
		t.Fatalf("got %+v, %v", got, err)
	}
	plain := p
	plain.Relay = "ws://203.0.113.7"
	if _, err := ParsePairing(plain.String(), false); err == nil {
		t.Error("a ws:// relay was taken without allowPlain")
	}
	if _, err := ParsePairing(plain.String(), true); err != nil {
		t.Errorf("allowPlain: %v", err)
	}
	for _, bad := range []string{"", "zubi1:", "zubi1:!!", "zubi2:" + code[6:], code[:len(code)-4]} {
		if _, err := ParsePairing(bad, true); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	noRoom := p
	noRoom.Room = "short"
	if _, err := ParsePairing(noRoom.String(), false); err == nil {
		t.Error("a bad room was taken")
	}
}

// TestDerivationMatchesTheSharedVectors: the app derives the same prologue and key, held to the
// same file.
func TestDerivationMatchesTheSharedVectors(t *testing.T) {
	data, err := os.ReadFile("../../../Packages/ZubiAPI/Tests/Fixtures/relay-noise-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Derivation struct {
			Room, Secret, Prologue, PSK string
			KeyID                       string `json:"key_id"`
		}
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	d := file.Derivation
	if got := hex.EncodeToString(Prologue(d.Room, d.KeyID)); got != d.Prologue {
		t.Errorf("prologue %s, want %s", got, d.Prologue)
	}
	if got := hex.EncodeToString(PSK(d.Secret)); got != d.PSK {
		t.Errorf("psk %s, want %s", got, d.PSK)
	}
}

type world struct {
	relayURL string
	host     *Host
	pairing  Pairing
	api      *httptest.Server
}

// newWorld runs a relay, a fake Zubi API, and a host serving the API through the relay, or
// target instead when it isn't empty.
func newWorld(t *testing.T, api http.Handler, target string) *world {
	t.Helper()
	r := relay.New(relay.Options{})
	relaySrv := httptest.NewServer(r.Handler())
	t.Cleanup(func() { r.Close(); relaySrv.Close() })
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)

	_, key, _ := ed25519.GenerateKey(rand.Reader)
	w := &world{relayURL: "ws" + strings.TrimPrefix(relaySrv.URL, "http"), api: apiSrv}
	if target == "" {
		target = strings.TrimPrefix(apiSrv.URL, "http://")
	}
	w.host = &Host{
		Relay:  w.relayURL,
		Key:    key,
		Target: target,
		Secret: func(id string) (string, bool) {
			if id == "pi" {
				return "the secret", true
			}
			return "", false
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	go w.host.Run(ctx, func() { close(ready) })
	t.Cleanup(cancel)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the host didn't register")
	}
	w.pairing = Pairing{Relay: w.relayURL, Room: w.host.Room(), KeyID: "pi", Secret: "the secret"}
	return w
}

func (w *world) client(p Pairing) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return Dial(ctx, p, nil) },
	}}
}

func TestHTTPThroughTheRelay(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789"), 200_000) // 2 MB: many messages each way
	w := newWorld(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/echo" {
			rw.Write(body)
			return
		}
		io.WriteString(rw, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
	}), "")
	client := w.client(w.pairing)

	for i := range 3 { // one connection, kept alive
		req, _ := http.NewRequest("GET", "http://zubi/api/lists", nil)
		req.Header.Set("Authorization", "Bearer k")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(body) != "GET /api/lists Bearer k" {
			t.Fatalf("request %d: %q", i, body)
		}
	}
	res, err := client.Post("http://zubi/echo", "application/octet-stream", bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(body, big) {
		t.Fatalf("echo: %d bytes back of %d", len(body), len(big))
	}
}

// TestHalfClose: a client that finishes sending still gets the whole answer.
func TestHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		data, _ := io.ReadAll(c) // until the client's CloseWrite
		c.Write(bytes.ToUpper(data))
		c.Close()
	}()
	w := newWorld(t, http.NotFoundHandler(), ln.Addr().String())

	c, err := Dial(context.Background(), w.pairing, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("shout"))
	c.CloseWrite()
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "SHOUT" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRefusals(t *testing.T) {
	w := newWorld(t, http.NotFoundHandler(), "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wrongSecret := w.pairing
	wrongSecret.Secret = "guessed"
	unknownKey := w.pairing
	unknownKey.KeyID = "deleted"
	for name, p := range map[string]Pairing{"wrong secret": wrongSecret, "unknown key": unknownKey} {
		_, err := Dial(ctx, p, nil)
		var refused RefusedError
		if !errors.As(err, &refused) {
			t.Errorf("%s: got %v, want RefusedError", name, err)
		}
	}

	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	noHost := w.pairing
	noHost.Room = (&Host{Key: otherKey}).Room()
	_, err := Dial(ctx, noHost, nil)
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "isn't connected") {
		t.Errorf("no host: got %v", err)
	}
}

// TestPairingCodeMatchesTheApp: the shared fixture's code is what this package writes, and a code
// with the keys sorted, as the app writes them, reads the same.
func TestPairingCodeMatchesTheApp(t *testing.T) {
	data, err := os.ReadFile("../../../Packages/ZubiAPI/Tests/Fixtures/relay-noise-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Pairing struct {
			Code string
			Pairing
		}
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	want := file.Pairing.Pairing
	if got := want.String(); got != file.Pairing.Code {
		t.Errorf("String() = %s, want the fixture's %s", got, file.Pairing.Code)
	}
	sorted, _ := json.Marshal(map[string]string{ // encoding/json sorts map keys, as the app's encoder does
		"key_id": want.KeyID, "name": want.Name, "relay": want.Relay, "room": want.Room, "secret": want.Secret,
	})
	got, err := ParsePairing("zubi1:"+base64.RawURLEncoding.EncodeToString(sorted), false)
	if err != nil || got != want {
		t.Errorf("the app's code: %+v, %v", got, err)
	}
}

// TestNotARelay: an address that answers but isn't a relay, as when a tunnel points at the wrong
// port, says so rather than blaming the Mac.
func TestNotARelay(t *testing.T) {
	for status, want := range map[int]string{
		404: "isn't a Zubi relay",
		502: "nothing answering behind it",
		530: "nothing answering behind it",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(status), status)
		}))
		p := Pairing{Relay: "ws" + strings.TrimPrefix(srv.URL, "http"), Room: strings.Repeat("A", 43), KeyID: "k", Secret: "s"}
		_, err := Dial(context.Background(), p, nil)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: got %v, want %q", status, err, want)
		}
	}
}
