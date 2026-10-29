package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fa-krug/zubi/cli/internal/websocket"
)

// startRelay serves a relay. Behind a proxy, the test's request sets the forwarded headers through
// the query: ?proto=https&for=203.0.113.9.
func startRelay(t *testing.T, opts Options) (*Server, string) {
	t.Helper()
	s := New(opts)
	handler := s.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proto := r.URL.Query().Get("proto"); proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if addr := r.URL.Query().Get("for"); addr != "" {
			r.Header.Set("X-Forwarded-For", "198.51.100.1, "+addr)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		s.Close()
		srv.Close()
	})
	return s, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		t.Cleanup(func() { c.Close(websocket.CloseNormal, "") })
	}
	return c, err
}

func mustDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, err := dial(t, url)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func read(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	m, err := readControl(c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// registerHost connects a host with key and completes its registration.
func registerHost(t *testing.T, url string, key ed25519.PrivateKey) *websocket.Conn {
	t.Helper()
	c := mustDial(t, url+"/v1/host")
	challenge := read(t, c)
	nonce, err := base64.RawURLEncoding.DecodeString(challenge.Nonce)
	if challenge.Type != "challenge" || err != nil || len(nonce) != 32 {
		t.Fatalf("challenge %+v", challenge)
	}
	sig := ed25519.Sign(key, append([]byte(RegisterContext), nonce...))
	pub := base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	sendControl(c, Message{Type: "register", Key: pub, Signature: base64.RawURLEncoding.EncodeToString(sig)})
	if m := read(t, c); m.Type != "registered" || m.Room != pub {
		t.Fatalf("got %+v, want registered for %s", m, pub)
	}
	return c
}

func newKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.RawURLEncoding.EncodeToString(pub)
}

func wantClose(t *testing.T, c *websocket.Conn, code int) {
	t.Helper()
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue // a control message before the close
		}
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) || closeErr.Code != code {
			t.Fatalf("got %v, want close %d", err, code)
		}
		return
	}
}

func wantRefused(t *testing.T, url string, status int) {
	t.Helper()
	_, err := dial(t, url)
	var hsErr *websocket.HandshakeError
	if !errors.As(err, &hsErr) || hsErr.Status != status {
		t.Fatalf("%s: got %v, want %d", url, err, status)
	}
}

func TestSessionCarriesMessagesBothWays(t *testing.T) {
	_, url := startRelay(t, Options{})
	key, room := newKey(t)
	host := registerHost(t, url, key)

	client := mustDial(t, url+"/v1/connect/"+room)
	// The client may speak first: its message waits until the host accepts.
	client.WriteMessage(websocket.Binary, []byte("noise handshake 1"))
	announce := read(t, host)
	if announce.Type != "session" || announce.ID == "" {
		t.Fatalf("got %+v, want a session", announce)
	}
	data := mustDial(t, url+"/v1/accept/"+announce.ID)

	_, got, err := data.ReadMessage()
	if err != nil || string(got) != "noise handshake 1" {
		t.Fatalf("host got %q, %v", got, err)
	}
	data.WriteMessage(websocket.Binary, make([]byte, MaxDataMessage))
	if _, got, err := client.ReadMessage(); err != nil || len(got) != MaxDataMessage {
		t.Fatalf("client got %d bytes, %v", len(got), err)
	}
	// The session ID is spent: nobody can accept it again.
	wantRefused(t, url+"/v1/accept/"+announce.ID, http.StatusNotFound)

	client.Close(4100, "done")
	wantClose(t, data, 4100)
}

func TestRegistrationIsVerified(t *testing.T) {
	_, url := startRelay(t, Options{})
	key, _ := newKey(t)
	_, other := newKey(t)
	c := mustDial(t, url+"/v1/host")
	nonce, _ := base64.RawURLEncoding.DecodeString(read(t, c).Nonce)
	// A valid signature, but by a different key than the one claimed.
	sig := ed25519.Sign(key, append([]byte(RegisterContext), nonce...))
	sendControl(c, Message{Type: "register", Key: other, Signature: base64.RawURLEncoding.EncodeToString(sig)})
	if m := read(t, c); m.Type != "error" {
		t.Fatalf("got %+v, want an error", m)
	}
	wantClose(t, c, CloseRegistration)
	wantRefused(t, url+"/v1/connect/"+other, http.StatusNotFound)
}

func TestUnknownRoomAndSession(t *testing.T) {
	_, url := startRelay(t, Options{})
	_, room := newKey(t)
	wantRefused(t, url+"/v1/connect/"+room, http.StatusNotFound)
	wantRefused(t, url+"/v1/accept/nope", http.StatusNotFound)
}

func TestHostThatDoesNotAccept(t *testing.T) {
	_, url := startRelay(t, Options{AcceptTimeout: 200 * time.Millisecond})
	key, room := newKey(t)
	host := registerHost(t, url, key)
	client := mustDial(t, url+"/v1/connect/"+room)
	id := read(t, host).ID
	wantClose(t, client, CloseNoAnswer)
	wantRefused(t, url+"/v1/accept/"+id, http.StatusNotFound)
}

func TestHostLeavingEndsWaitingClients(t *testing.T) {
	_, url := startRelay(t, Options{})
	key, room := newKey(t)
	host := registerHost(t, url, key)
	client := mustDial(t, url+"/v1/connect/"+room)
	read(t, host)
	host.Close(websocket.CloseNormal, "")
	wantClose(t, client, CloseHostGone)
}

func TestNewerHostReplacesOlder(t *testing.T) {
	_, url := startRelay(t, Options{})
	key, room := newKey(t)
	first := registerHost(t, url, key)
	second := registerHost(t, url, key)
	if m := read(t, first); m.Type != "error" {
		t.Fatalf("got %+v, want an error", m)
	}
	wantClose(t, first, CloseReplaced)
	mustDial(t, url+"/v1/connect/"+room)
	if m := read(t, second); m.Type != "session" {
		t.Fatalf("got %+v, want the session on the newer host", m)
	}
}

func TestHostMustPing(t *testing.T) {
	_, url := startRelay(t, Options{HostIdle: 300 * time.Millisecond})
	key, room := newKey(t)
	host := registerHost(t, url, key)
	for range 3 {
		time.Sleep(150 * time.Millisecond)
		sendControl(host, Message{Type: "ping"})
		if m := read(t, host); m.Type != "pong" {
			t.Fatalf("got %+v, want pong", m)
		}
	}
	_, _, err := host.ReadMessage() // silent now, so the relay hangs up
	if err == nil {
		t.Fatal("a silent host stayed connected")
	}
	wantRefused(t, url+"/v1/connect/"+room, http.StatusNotFound)
}

func TestSessionLimit(t *testing.T) {
	_, url := startRelay(t, Options{MaxSessions: 1})
	key, room := newKey(t)
	registerHost(t, url, key)
	mustDial(t, url+"/v1/connect/"+room)
	wantRefused(t, url+"/v1/connect/"+room, http.StatusServiceUnavailable)
}

func TestRateLimit(t *testing.T) {
	_, url := startRelay(t, Options{RateBurst: 3, RatePerSecond: 0.001})
	_, room := newKey(t)
	for range 3 {
		wantRefused(t, url+"/v1/connect/"+room, http.StatusNotFound)
	}
	wantRefused(t, url+"/v1/connect/"+room, http.StatusTooManyRequests)
}

func TestBehindProxy(t *testing.T) {
	_, url := startRelay(t, Options{BehindProxy: true, RateBurst: 1, RatePerSecond: 0.001})
	_, room := newKey(t)
	// Only what the proxy received over TLS.
	wantRefused(t, url+"/v1/connect/"+room, http.StatusForbidden)
	wantRefused(t, url+"/v1/connect/"+room+"?proto=http", http.StatusForbidden)
	// Limits follow the address the proxy saw, not the proxy's own.
	wantRefused(t, url+"/v1/connect/"+room+"?proto=https&for=203.0.113.1", http.StatusNotFound)
	wantRefused(t, url+"/v1/connect/"+room+"?proto=https&for=203.0.113.1", http.StatusTooManyRequests)
	wantRefused(t, url+"/v1/connect/"+room+"?proto=https&for=203.0.113.2", http.StatusNotFound)
	// Health checks come from the platform, not over TLS.
	res, err := http.Get(strings.Replace(url, "ws://", "http://", 1) + "/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("/health: %v %v", res, err)
	}
	res.Body.Close()
}

func TestCloseEndsEveryConnection(t *testing.T) {
	s, url := startRelay(t, Options{})
	key, _ := newKey(t)
	host := registerHost(t, url, key)
	s.Close()
	wantClose(t, host, websocket.CloseGoingAway)
}

func TestRoomIsTheCanonicalKey(t *testing.T) {
	_, url := startRelay(t, Options{})
	key, room := newKey(t)
	c := mustDial(t, url+"/v1/host")
	nonce, _ := base64.RawURLEncoding.DecodeString(read(t, c).Nonce)
	sig := ed25519.Sign(key, append([]byte(RegisterContext), nonce...))
	// The same key, spelt with the last character's two spare bits set.
	alias := []byte(room)
	alias[len(alias)-1] = base64URLAlphabet[strings.IndexByte(base64URLAlphabet, alias[len(alias)-1])|3]
	sendControl(c, Message{Type: "register", Key: string(alias), Signature: base64.RawURLEncoding.EncodeToString(sig)})
	if m := read(t, c); m.Type != "registered" || m.Room != room {
		t.Fatalf("got %+v, want registered as %s", m, room)
	}
}

const base64URLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
