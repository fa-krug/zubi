// Package relay is the server behind `zubi relay`: it lets a Mac running Zubi and its clients reach
// each other when neither can accept connections from the other, by pairing WebSockets both open
// to the relay and copying messages between them. It never holds a key: what it copies is
// end-to-end encrypted between the Mac and the client, and it only learns who connects when.
// docs/features/relay.md describes the protocol.
package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fa-krug/zubi/cli/internal/websocket"
)

// RegisterContext is what a host's signature covers, followed by the relay's 32-byte nonce, so a
// signature made for the relay can't be mistaken for one made for anything else.
const RegisterContext = "zubi-relay-v1/register"

// Close codes the relay sends, besides RFC 6455's own.
const (
	// CloseReplaced: a newer connection registered the same room, so this host connection ends.
	CloseReplaced = 4000
	// CloseNoAnswer: the host didn't accept the client's session in time.
	CloseNoAnswer = 4001
	// CloseHostGone: the host disconnected before accepting the client's session.
	CloseHostGone = 4002
	// CloseRegistration: the host's registration was malformed or its signature didn't verify.
	CloseRegistration = 4003
	// CloseTryAgain: the relay is full or shutting down (RFC 6455's "try again later").
	CloseTryAgain = 1013
)

// MaxDataMessage is the largest message the relay copies: a Noise message's limit.
const MaxDataMessage = 65535

const maxControlMessage = 4096

// Message is one control message on a host's connection, as JSON in a text message.
type Message struct {
	Type      string `json:"type"`
	Nonce     string `json:"nonce,omitempty"`
	Key       string `json:"key,omitempty"`
	Signature string `json:"signature,omitempty"`
	Room      string `json:"room,omitempty"`
	ID        string `json:"id,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Options tune a Server; a zero field means its default.
type Options struct {
	// BehindProxy trusts X-Forwarded-For and X-Forwarded-Proto from a TLS-terminating proxy in
	// front of the relay, and refuses WebSockets the proxy didn't receive over TLS.
	BehindProxy bool
	// Logf receives one line per host connecting or leaving, and per refused registration.
	Logf func(format string, args ...any)

	MaxRooms        int           // hosts at once; default 1000
	MaxSessions     int           // pending and open sessions per room; default 32
	RegisterTimeout time.Duration // from the challenge to a valid registration; default 10s
	AcceptTimeout   time.Duration // from a client connecting to its host accepting; default 10s
	HostIdle        time.Duration // the longest a host may stay silent; default 90s
	PingInterval    time.Duration // how often the relay pings both ends of a session; default 30s
	RatePerSecond   float64       // new WebSockets per client address, sustained; default 2
	RateBurst       int           // ... and in a burst; default 30
	MaxConnsPerAddr int           // open WebSockets per client address; default 64
}

func (o *Options) defaults() {
	set := func(p *time.Duration, d time.Duration) {
		if *p <= 0 {
			*p = d
		}
	}
	set(&o.RegisterTimeout, 10*time.Second)
	set(&o.AcceptTimeout, 10*time.Second)
	set(&o.HostIdle, 90*time.Second)
	set(&o.PingInterval, 30*time.Second)
	if o.MaxRooms <= 0 {
		o.MaxRooms = 1000
	}
	if o.MaxSessions <= 0 {
		o.MaxSessions = 32
	}
	if o.RatePerSecond <= 0 {
		o.RatePerSecond = 2
	}
	if o.RateBurst <= 0 {
		o.RateBurst = 30
	}
	if o.MaxConnsPerAddr <= 0 {
		o.MaxConnsPerAddr = 64
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
}

// Server pairs hosts and clients. Its Handler serves the relay's HTTP endpoints.
type Server struct {
	opts    Options
	limiter *limiter

	mu      sync.Mutex
	rooms   map[string]*host
	pending map[string]*session
	conns   map[*websocket.Conn]struct{}
	closed  bool
}

type host struct {
	room     string
	conn     *websocket.Conn
	sessions int // pending and open, under Server.mu
}

type session struct {
	id       string
	host     *host
	accepted chan *websocket.Conn // the host's data connection, or nil when its upgrade failed
	gone     chan struct{}        // closed when the host leaves before accepting
	done     chan struct{}        // closed when the session is over
}

// New makes a Server.
func New(opts Options) *Server {
	opts.defaults()
	return &Server{
		opts:    opts,
		limiter: newLimiter(opts.RatePerSecond, opts.RateBurst, opts.MaxConnsPerAddr),
		rooms:   map[string]*host{},
		pending: map[string]*session{},
		conns:   map[*websocket.Conn]struct{}{},
	}
}

// Handler serves GET /, /health and the /v1 WebSocket endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Zubi relay. It pairs a Mac running Zubi with its clients; see "+
			"https://zubi.fa-krug.de/docs.html#relay\n")
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /v1/host", s.guard(s.serveHost))
	mux.HandleFunc("GET /v1/connect/{room}", s.guard(s.serveConnect))
	mux.HandleFunc("GET /v1/accept/{session}", s.guard(s.serveAccept))
	return mux
}

// Close ends every WebSocket with 1001, going away. The HTTP server is the caller's to stop.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	conns := make([]*websocket.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close(websocket.CloseGoingAway, "the relay is shutting down")
	}
}

// guard admits a WebSocket request: over TLS when behind a proxy, and within the address's limits.
func (s *Server) guard(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.BehindProxy && r.Header.Get("X-Forwarded-Proto") != "https" {
			http.Error(w, "this relay only takes wss:// connections", http.StatusForbidden)
			return
		}
		addr := s.clientAddr(r)
		release, ok := s.limiter.admit(addr, time.Now())
		if !ok {
			http.Error(w, "too many connections from this address", http.StatusTooManyRequests)
			return
		}
		defer release()
		next(w, r, addr)
	}
}

// clientAddr is the address limits apply to: the TCP peer, or behind a proxy the address it saw,
// the last X-Forwarded-For entry, which is the one the proxy itself appended.
func (s *Server) clientAddr(r *http.Request) string {
	if s.opts.BehindProxy {
		if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
			hops := strings.Split(values[len(values)-1], ",")
			if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// upgrade takes over the connection and tracks it for Close.
func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, maxMessage int) (*websocket.Conn, bool) {
	conn, err := websocket.Upgrade(w, r)
	if err != nil {
		return nil, false
	}
	conn.MaxMessage = maxMessage
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close(CloseTryAgain, "the relay is shutting down")
		return nil, false
	}
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	return conn, true
}

func (s *Server) forget(conn *websocket.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
}

func sendControl(conn *websocket.Conn, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.Text, data)
}

func readControl(conn *websocket.Conn) (Message, error) {
	op, data, err := conn.ReadMessage()
	if err != nil {
		return Message{}, err
	}
	var m Message
	if op != websocket.Text || json.Unmarshal(data, &m) != nil {
		return Message{}, errors.New("a control message is a JSON object in a text message")
	}
	return m, nil
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// short is the start of a room ID, enough to tell rooms apart in the log.
func short(room string) string {
	if len(room) > 8 {
		return room[:8] + "…"
	}
	return room
}

// serveHost is a host's control connection: a challenge, a signed registration, then session
// announcements to the host and pings from it, until either side ends it.
func (s *Server) serveHost(w http.ResponseWriter, r *http.Request, _ string) {
	conn, ok := s.upgrade(w, r, maxControlMessage)
	if !ok {
		return
	}
	defer s.forget(conn)
	defer conn.Close(websocket.CloseNormal, "")

	room, err := s.register(conn)
	if err != nil {
		s.opts.Logf("refused a host: %v", err)
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) {
			sendControl(conn, Message{Type: "error", Message: err.Error()})
			conn.Close(CloseRegistration, "registration refused")
		}
		return
	}
	h := &host{room: room, conn: conn}

	s.mu.Lock()
	old := s.rooms[room]
	if old == nil && len(s.rooms) >= s.opts.MaxRooms {
		s.mu.Unlock()
		sendControl(conn, Message{Type: "error", Message: "this relay is full"})
		conn.Close(CloseTryAgain, "this relay is full")
		return
	}
	s.rooms[room] = h
	s.mu.Unlock()
	if old != nil {
		sendControl(old.conn, Message{Type: "error", Message: "a newer connection registered this room"})
		old.conn.Close(CloseReplaced, "replaced by a newer connection")
	}
	if sendControl(conn, Message{Type: "registered", Room: room}) != nil {
		s.dropHost(h)
		return
	}
	s.opts.Logf("host connected: room %s", short(room))

	for {
		conn.SetReadDeadline(time.Now().Add(s.opts.HostIdle))
		m, err := readControl(conn)
		if err != nil {
			break
		}
		if m.Type == "ping" {
			if sendControl(conn, Message{Type: "pong"}) != nil {
				break
			}
		}
		// Anything else is ignored, so a newer host can talk to an older relay.
	}
	s.dropHost(h)
	s.opts.Logf("host left: room %s", short(room))
}

// register sends the challenge and checks the answer: an Ed25519 public key, which is the room,
// and its signature over RegisterContext and the nonce.
func (s *Server) register(conn *websocket.Conn) (string, error) {
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	if err := sendControl(conn, Message{Type: "challenge", Nonce: base64.RawURLEncoding.EncodeToString(nonce)}); err != nil {
		return "", err
	}
	conn.SetReadDeadline(time.Now().Add(s.opts.RegisterTimeout))
	m, err := readControl(conn)
	if err != nil {
		return "", err
	}
	if m.Type != "register" {
		return "", fmt.Errorf("expected register, got %q", m.Type)
	}
	key, err := base64.RawURLEncoding.DecodeString(m.Key)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return "", errors.New("key is not a base64url Ed25519 public key")
	}
	sig, err := base64.RawURLEncoding.DecodeString(m.Signature)
	if err != nil || !ed25519.Verify(key, append([]byte(RegisterContext), nonce...), sig) {
		return "", errors.New("the signature doesn't verify")
	}
	// Re-encoded: the decoder ignores the last character's spare bits, so one key has several
	// spellings, and a room must have one.
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// dropHost removes h, unless a newer host replaced it, and ends its sessions still waiting.
func (s *Server) dropHost(h *host) {
	s.mu.Lock()
	if s.rooms[h.room] == h {
		delete(s.rooms, h.room)
	}
	for id, sess := range s.pending {
		if sess.host == h {
			delete(s.pending, id)
			close(sess.gone)
		}
	}
	s.mu.Unlock()
}

// serveConnect is a client asking for a room's host. The host learns of the session over its
// control connection and opens /v1/accept/{session}; until then, the client's first messages wait
// in its connection.
func (s *Server) serveConnect(w http.ResponseWriter, r *http.Request, _ string) {
	room := r.PathValue("room")
	s.mu.Lock()
	h := s.rooms[room]
	switch {
	case h == nil:
		s.mu.Unlock()
		http.Error(w, "no Zubi is connected to this relay under that room", http.StatusNotFound)
		return
	case h.sessions >= s.opts.MaxSessions:
		s.mu.Unlock()
		http.Error(w, "this room has too many sessions open", http.StatusServiceUnavailable)
		return
	}
	sess := &session{id: randomID(16), host: h, accepted: make(chan *websocket.Conn, 1),
		gone: make(chan struct{}), done: make(chan struct{})}
	h.sessions++
	s.pending[sess.id] = sess
	s.mu.Unlock()
	defer close(sess.done)
	defer func() {
		s.mu.Lock()
		h.sessions--
		s.mu.Unlock()
	}()

	client, ok := s.upgrade(w, r, MaxDataMessage)
	if !ok {
		s.unpend(sess)
		return
	}
	defer s.forget(client)
	if sendControl(h.conn, Message{Type: "session", ID: sess.id}) != nil {
		s.unpend(sess)
		client.Close(CloseHostGone, "the Mac is not connected")
		return
	}

	timer := time.NewTimer(s.opts.AcceptTimeout)
	defer timer.Stop()
	var hostConn *websocket.Conn
	select {
	case hostConn = <-sess.accepted:
	case <-sess.gone:
		client.Close(CloseHostGone, "the Mac disconnected")
		return
	case <-timer.C:
		if s.unpend(sess) {
			client.Close(CloseNoAnswer, "the Mac did not answer")
			return
		}
		// The host claimed the session just now; its connection is on the way.
		hostConn = <-sess.accepted
	}
	if hostConn == nil {
		client.Close(CloseNoAnswer, "the Mac did not answer")
		return
	}
	s.pipe(client, hostConn)
}

// unpend removes a session nobody accepted, reporting whether it was still pending.
func (s *Server) unpend(sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[sess.id] != sess {
		return false
	}
	delete(s.pending, sess.id)
	return true
}

// serveAccept is the host's data connection for one session. The session ID, sent only to the
// host, is what authorizes it.
func (s *Server) serveAccept(w http.ResponseWriter, r *http.Request, _ string) {
	id := r.PathValue("session")
	s.mu.Lock()
	sess := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if sess == nil {
		http.Error(w, "no such session, or it timed out", http.StatusNotFound)
		return
	}
	conn, ok := s.upgrade(w, r, MaxDataMessage)
	if !ok {
		sess.accepted <- nil
		return
	}
	defer s.forget(conn)
	// serveConnect runs the session; this handler stays until it ends, so the connection counts
	// against the address's limit for as long as it is open.
	sess.accepted <- conn
	<-sess.done
}

// pipe copies messages both ways between a client and its host until either side ends, then
// ends the other with the same close code, and pings both meanwhile so a dead peer is noticed.
func (s *Server) pipe(client, hostConn *websocket.Conn) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var once sync.Once
	end := func(err error) {
		once.Do(func() {
			code, reason := websocket.CloseGoingAway, "the other side disconnected"
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) && forwardable(closeErr.Code) {
				code, reason = closeErr.Code, closeErr.Reason
			}
			client.Close(code, reason)
			hostConn.Close(code, reason)
			close(stop)
		})
	}
	copyTo := func(dst, src *websocket.Conn) {
		defer wg.Done()
		for {
			src.SetReadDeadline(time.Now().Add(2*s.opts.PingInterval + 15*time.Second))
			op, data, err := src.ReadMessage()
			if err == nil {
				err = dst.WriteMessage(op, data)
			}
			if err != nil {
				end(err)
				return
			}
		}
	}
	go copyTo(hostConn, client)
	go copyTo(client, hostConn)
	ticker := time.NewTicker(s.opts.PingInterval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				client.Ping()
				hostConn.Ping()
			}
		}
	}()
	wg.Wait()
}

// forwardable reports whether a close code one side sent may be passed on to the other.
func forwardable(code int) bool {
	switch {
	case code == 1000, code == 1001, code == 1003, code >= 1007 && code <= 1011, code >= 3000 && code <= 4999:
		return true
	}
	return false
}
