// Package tunnel carries a connection to Zubi's API through a relay, end-to-end encrypted: a
// client dials the Mac's room on the relay, the two run a Noise handshake keyed with the device
// key, and from then on each WebSocket message is one encrypted chunk of the byte stream. The
// relay only ever sees ciphertext. docs/features/relay.md describes the protocol.
package tunnel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fa-krug/zubi/cli/internal/noise"
	"github.com/fa-krug/zubi/cli/internal/websocket"
)

// Version is the first byte of a client's first message.
const Version = 1

// CloseKeyRefused is the close code a host sends during the handshake when it doesn't know the
// key, or the handshake shows the client doesn't hold it: 1008, policy violation, as the app's
// WebSocket API can send no code of its own.
const CloseKeyRefused = websocket.ClosePolicy

// pairingPrefix starts a pairing code.
const pairingPrefix = "zubi1:"

// Pairing is everything a client needs to reach a Mac through a relay, as Zubi's Settings hand
// it out: which relay, which room on it, and which key. The secret is the key's, so a pairing code
// is as secret as the key.
type Pairing struct {
	Relay  string `json:"relay"`
	Room   string `json:"room"`
	KeyID  string `json:"key_id"`
	Secret string `json:"secret"`
	// Name is the key's name, for display only.
	Name string `json:"name,omitempty"`
}

// ParsePairing reads a code made by Pairing.String. A ws:// relay is refused unless allowPlain.
func ParsePairing(code string, allowPlain bool) (Pairing, error) {
	code = strings.TrimSpace(code)
	rest, ok := strings.CutPrefix(code, pairingPrefix)
	if !ok {
		return Pairing{}, errors.New("not a pairing code: it starts with " + pairingPrefix)
	}
	data, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return Pairing{}, errors.New("the pairing code is damaged; copy it again")
	}
	var p Pairing
	if err := json.Unmarshal(data, &p); err != nil {
		return Pairing{}, errors.New("the pairing code is damaged; copy it again")
	}
	return p, p.Validate(allowPlain)
}

// Validate checks that every field is usable.
func (p Pairing) Validate(allowPlain bool) error {
	u, err := url.Parse(p.Relay)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "wss" && u.Scheme != "ws"):
		return fmt.Errorf("the relay %q is not a wss:// URL", p.Relay)
	case u.Scheme == "ws" && !allowPlain:
		return fmt.Errorf("the relay %s is plain ws://; only wss:// is used", p.Relay)
	}
	if room, err := base64.RawURLEncoding.DecodeString(p.Room); err != nil || len(room) != 32 {
		return errors.New("the pairing code's room is not valid")
	}
	if p.KeyID == "" || len(p.KeyID) > 255 || p.Secret == "" {
		return errors.New("the pairing code has no key")
	}
	return nil
}

// String is the pairing code: "zubi1:" and the JSON, base64url-encoded.
func (p Pairing) String() string {
	data, _ := json.Marshal(p)
	return pairingPrefix + base64.RawURLEncoding.EncodeToString(data)
}

// Prologue binds a session to its room and key: both sides hash it into the handshake, so a
// session can't be moved to another room or key.
func Prologue(room, keyID string) []byte {
	return []byte("zubi-relay-v1\x00" + room + "\x00" + keyID)
}

// PSK is the handshake's pre-shared key, derived from the device key's secret.
func PSK(secret string) []byte {
	sum := sha256.Sum256([]byte("zubi-relay-v1 psk\x00" + secret))
	return sum[:]
}

// RefusedError is the Mac refusing the key: unknown, deleted or regenerated since.
type RefusedError struct{}

func (RefusedError) Error() string {
	return "Zubi refused the key; it was deleted or regenerated. Pair again with a new code from Settings › Keys"
}

// UnavailableError is any other failure to reach the Mac: the relay, or the Mac on it.
type UnavailableError struct {
	Relay string
	Err   error
}

func (e *UnavailableError) Error() string { return fmt.Sprintf("through %s: %v", e.Relay, e.Err) }
func (e *UnavailableError) Unwrap() error { return e.Err }

// explain turns a relay's refusal into words.
func explain(err error) error {
	var hs *websocket.HandshakeError
	var closeErr *websocket.CloseError
	switch {
	case errors.As(err, &hs) && hs.Status == 404:
		return errors.New("the Mac isn't connected to the relay: check that Zubi runs there with the relay on, " +
			"and that the Mac is awake")
	case errors.As(err, &closeErr) && closeErr.Code == CloseKeyRefused:
		return RefusedError{}
	case errors.As(err, &closeErr) && closeErr.Reason != "":
		return errors.New(closeErr.Reason)
	}
	return err
}

// Dial opens a session with the Mac: a WebSocket to the room, then the handshake. tlsConfig may be
// nil.
func Dial(ctx context.Context, p Pairing, tlsConfig *tls.Config) (*Conn, error) {
	ws, err := websocket.Dial(ctx, strings.TrimRight(p.Relay, "/")+"/v1/connect/"+p.Room, tlsConfig)
	if err != nil {
		return nil, &UnavailableError{Relay: p.Relay, Err: explain(err)}
	}
	c, err := clientHandshake(ctx, ws, p)
	if err != nil {
		ws.Close(websocket.CloseNormal, "")
		var refused RefusedError
		if errors.As(err, &refused) {
			return nil, err
		}
		return nil, &UnavailableError{Relay: p.Relay, Err: err}
	}
	return c, nil
}

func clientHandshake(ctx context.Context, ws *websocket.Conn, p Pairing) (*Conn, error) {
	hs, err := noise.NewHandshake(true, Prologue(p.Room, p.KeyID), PSK(p.Secret), nil)
	if err != nil {
		return nil, err
	}
	m1, err := hs.WriteMessage(nil)
	if err != nil {
		return nil, err
	}
	first := append([]byte{Version, byte(len(p.KeyID))}, p.KeyID...)
	if err := ws.WriteMessage(websocket.Binary, append(first, m1...)); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		ws.SetReadDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { ws.SetReadDeadline(time.Unix(1, 0)) })
	defer stop()
	op, m2, err := ws.ReadMessage()
	if err != nil {
		return nil, explain(err)
	}
	if op != websocket.Binary {
		return nil, errors.New("the Mac's answer is not a handshake")
	}
	if _, err := hs.ReadMessage(m2); err != nil {
		// Only a Mac holding the key can answer; a relay or anyone else can't.
		return nil, errors.New("the answer didn't come from the Mac holding this key")
	}
	if !stop() {
		return nil, ctx.Err()
	}
	ws.SetReadDeadline(time.Time{})
	return newConn(ws, hs)
}

// Accept is the Mac's side of a new session: it reads the client's first message, finds the key's
// secret with lookup, and answers the handshake. On an unknown key or a failed handshake it closes
// the WebSocket with CloseKeyRefused. It returns the key's ID with the session.
func Accept(ws *websocket.Conn, room string, lookup func(keyID string) (secret string, ok bool)) (*Conn, string, error) {
	refuse := func(err error) (*Conn, string, error) {
		ws.Close(CloseKeyRefused, "the key was refused")
		return nil, "", err
	}
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	op, first, err := ws.ReadMessage()
	if err != nil {
		return nil, "", err
	}
	if op != websocket.Binary || len(first) < 2 || first[0] != Version || len(first) < 2+int(first[1]) {
		return refuse(errors.New("not a version 1 handshake"))
	}
	keyID := string(first[2 : 2+int(first[1])])
	secret, ok := lookup(keyID)
	if !ok {
		return refuse(fmt.Errorf("unknown key %q", keyID))
	}
	hs, err := noise.NewHandshake(false, Prologue(room, keyID), PSK(secret), nil)
	if err != nil {
		return refuse(err)
	}
	if _, err := hs.ReadMessage(first[2+int(first[1]):]); err != nil {
		return refuse(fmt.Errorf("key %q: %w", keyID, err))
	}
	m2, err := hs.WriteMessage(nil)
	if err != nil {
		return refuse(err)
	}
	if err := ws.WriteMessage(websocket.Binary, m2); err != nil {
		return nil, "", err
	}
	ws.SetReadDeadline(time.Time{})
	c, err := newConn(ws, hs)
	return c, keyID, err
}

// Conn is one session as a net.Conn: a byte stream in encrypted messages of at most
// noise.MaxPlaintext bytes each. An empty message is the sender's end of stream, so CloseWrite
// works as on TCP. Read and Write may run concurrently.
type Conn struct {
	ws   *websocket.Conn
	send *noise.CipherState
	recv *noise.CipherState

	rmu     sync.Mutex
	pending []byte
	readEOF bool

	wmu      sync.Mutex
	writeEOF bool
}

func newConn(ws *websocket.Conn, hs *noise.Handshake) (*Conn, error) {
	send, recv, _, err := hs.Split()
	if err != nil {
		return nil, err
	}
	ws.MaxMessage = noise.MaxMessage
	return &Conn{ws: ws, send: send, recv: recv}, nil
}

// Read returns decrypted bytes, and io.EOF once the other side has finished writing.
func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for len(c.pending) == 0 {
		if c.readEOF {
			return 0, io.EOF
		}
		op, data, err := c.ws.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) && closeErr.Code == websocket.CloseNormal {
				return 0, io.EOF
			}
			return 0, explain(err)
		}
		if op != websocket.Binary {
			return 0, errors.New("tunnel: unexpected text message")
		}
		plain, err := c.recv.Decrypt(data)
		if err != nil {
			c.ws.Close(websocket.ClosePolicy, "a message failed to authenticate")
			return 0, err
		}
		if len(plain) == 0 {
			c.readEOF = true
			continue
		}
		c.pending = plain
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// Write encrypts p in as many messages as it takes.
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.writeEOF {
		return 0, errors.New("tunnel: write after CloseWrite")
	}
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), noise.MaxPlaintext)]
		sealed, err := c.send.Encrypt(chunk)
		if err != nil {
			return written, err
		}
		if err := c.ws.WriteMessage(websocket.Binary, sealed); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// CloseWrite tells the other side this side is done writing; reading goes on.
func (c *Conn) CloseWrite() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.writeEOF {
		return nil
	}
	c.writeEOF = true
	sealed, err := c.send.Encrypt(nil)
	if err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.Binary, sealed)
}

// Close ends the session.
func (c *Conn) Close() error { return c.ws.Close(websocket.CloseNormal, "") }

type relayAddr string

func (a relayAddr) Network() string { return "zubi-relay" }
func (a relayAddr) String() string  { return string(a) }

func (c *Conn) LocalAddr() net.Addr  { return relayAddr("local") }
func (c *Conn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

// SetDeadline and SetReadDeadline bound reads; writes are bounded by the WebSocket's own timeout.
func (c *Conn) SetDeadline(t time.Time) error      { return c.ws.SetReadDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return nil }

// halfCloser is a connection that can end its writing side alone.
type halfCloser interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// Pipe copies both ways between a and b, passing each side's end of stream on as the other's
// CloseWrite, and closes both once both directions are done or either fails.
func Pipe(a, b halfCloser) {
	done := make(chan error, 2)
	copyTo := func(dst, src halfCloser) {
		_, err := io.Copy(dst, src)
		if err == nil {
			err = dst.CloseWrite()
		}
		done <- err
	}
	go copyTo(a, b)
	go copyTo(b, a)
	if err := <-done; err == nil {
		err = <-done
	}
	a.Close()
	b.Close()
}
