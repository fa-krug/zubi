// Package websocket is the part of RFC 6455 the relay needs: the opening handshake on both sides,
// whole messages up to a size limit, ping, pong and close. No extensions and no subprotocols.
package websocket

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Message types, as the frame opcodes that carry them.
const (
	Text   = 1
	Binary = 2
)

const (
	opContinuation = 0
	opClose        = 8
	opPing         = 9
	opPong         = 10
)

// Close codes this package sends itself; callers may send any code RFC 6455 allows.
const (
	CloseNormal        = 1000
	CloseGoingAway     = 1001
	CloseProtocolError = 1002
	CloseInvalidData   = 1007
	ClosePolicy        = 1008
	CloseTooBig        = 1009
)

// DefaultMaxMessage is the largest message a Conn accepts unless MaxMessage says otherwise.
const DefaultMaxMessage = 64 << 10

// writeTimeout bounds every write, so a peer that stops reading can't block a writer forever.
const writeTimeout = 15 * time.Second

const acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// CloseError is how a connection ended when the peer closed it.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("websocket closed (%d)", e.Code)
	}
	return fmt.Sprintf("websocket closed (%d): %s", e.Code, e.Reason)
}

// ErrClosed is returned by writes after the connection was closed.
var ErrClosed = errors.New("websocket: connection closed")

// HandshakeError is a refused opening handshake: the server answered with Status instead of 101.
type HandshakeError struct {
	Status  int
	Message string
}

func (e *HandshakeError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("websocket handshake refused: %s", http.StatusText(e.Status))
	}
	return fmt.Sprintf("websocket handshake refused (%d): %s", e.Status, e.Message)
}

// Conn is one WebSocket connection. ReadMessage must be called from one goroutine at a time;
// WriteMessage, Ping and Close may be called from any.
type Conn struct {
	conn   net.Conn
	r      *bufio.Reader
	client bool // a client masks what it sends and expects nothing masked back

	// MaxMessage is the largest message ReadMessage accepts; a larger one closes the connection
	// with 1009. Zero means DefaultMaxMessage. Set it before the first read.
	MaxMessage int

	wmu       sync.Mutex
	closeSent bool
	closeOnce sync.Once
}

// Upgrade answers a WebSocket opening handshake and takes over the connection. On a request that
// isn't one, it writes a 400 or 426 and returns an error.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	fail := func(status int, message string) (*Conn, error) {
		if status == http.StatusUpgradeRequired {
			w.Header().Set("Sec-WebSocket-Version", "13")
		}
		http.Error(w, message, status)
		return nil, errors.New("websocket: " + message)
	}
	if r.Method != http.MethodGet {
		return fail(http.StatusMethodNotAllowed, "a WebSocket handshake is a GET")
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") || !headerHasToken(r.Header, "Upgrade", "websocket") {
		return fail(http.StatusUpgradeRequired, "this endpoint takes a WebSocket connection")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return fail(http.StatusUpgradeRequired, "unsupported WebSocket version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if decoded, err := base64.StdEncoding.DecodeString(key); err != nil || len(decoded) != 16 {
		return fail(http.StatusBadRequest, "bad Sec-WebSocket-Key")
	}
	netConn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return fail(http.StatusInternalServerError, "the connection can't be taken over")
	}
	_ = netConn.SetDeadline(time.Time{}) // the server's read and write timeouts no longer apply
	response := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	_ = netConn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := rw.WriteString(response); err == nil {
		err = rw.Flush()
	}
	if err != nil {
		netConn.Close()
		return nil, err
	}
	_ = netConn.SetWriteDeadline(time.Time{})
	return &Conn{conn: netConn, r: rw.Reader}, nil
}

// Dial opens a WebSocket connection to a ws:// or wss:// URL. tlsConfig may be nil.
func Dial(ctx context.Context, rawURL string, tlsConfig *tls.Config) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	var secure bool
	switch u.Scheme {
	case "ws":
	case "wss":
		secure = true
	default:
		return nil, fmt.Errorf("websocket: %q is not a ws:// or wss:// URL", rawURL)
	}
	host := u.Host
	if u.Port() == "" {
		if secure {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	var d net.Dialer
	netConn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	// Until the handshake is done, the context's deadline is the connection's.
	if deadline, ok := ctx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { netConn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if secure {
		cfg := &tls.Config{}
		if tlsConfig != nil {
			cfg = tlsConfig.Clone()
		}
		if cfg.ServerName == "" {
			cfg.ServerName = u.Hostname()
		}
		cfg.NextProtos = []string{"http/1.1"}
		tlsConn := tls.Client(netConn, cfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			netConn.Close()
			return nil, err
		}
		netConn = tlsConn
	}

	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Host:       u.Host,
		Header: http.Header{
			"Upgrade":               {"websocket"},
			"Connection":            {"Upgrade"},
			"Sec-Websocket-Key":     {key},
			"Sec-Websocket-Version": {"13"},
		},
	}
	if err := req.Write(netConn); err != nil {
		netConn.Close()
		return nil, err
	}
	r := bufio.NewReader(netConn)
	res, err := http.ReadResponse(r, req)
	if err != nil {
		netConn.Close()
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		res.Body.Close()
		netConn.Close()
		return nil, &HandshakeError{Status: res.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	if !headerHasToken(res.Header, "Upgrade", "websocket") || res.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		netConn.Close()
		return nil, errors.New("websocket: the server's handshake answer is wrong")
	}
	// The context no longer applies; if it ended just now, its deadline may already be set.
	if !stop() {
		netConn.Close()
		return nil, ctx.Err()
	}
	_ = netConn.SetDeadline(time.Time{})
	return &Conn{conn: netConn, r: r, client: true}, nil
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// headerHasToken reports whether a comma-separated header holds token, ignoring case.
func headerHasToken(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, t := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// RemoteAddr is the peer's network address.
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// SetReadDeadline bounds the current and future reads; a frame of any kind, ping and pong
// included, is what a read waits for.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// ReadMessage returns the next whole message, answering pings and skipping pongs on the way. When
// the peer closes, it answers the close and returns a *CloseError.
func (c *Conn) ReadMessage() (messageType int, data []byte, err error) {
	limit := c.MaxMessage
	if limit <= 0 {
		limit = DefaultMaxMessage
	}
	var message []byte
	messageType = -1
	for {
		fin, op, payload, err := c.readFrame(limit)
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			code, reason := CloseNormal, ""
			switch {
			case len(payload) == 1:
				return 0, nil, c.fail(CloseProtocolError, "bad close frame")
			case len(payload) >= 2:
				code = int(binary.BigEndian.Uint16(payload))
				reason = string(payload[2:])
				if !validCloseCode(code) || !utf8.ValidString(reason) {
					return 0, nil, c.fail(CloseProtocolError, "bad close frame")
				}
			}
			c.close(code, "", false)
			return 0, nil, &CloseError{Code: code, Reason: reason}
		case Text, Binary:
			if messageType != -1 {
				return 0, nil, c.fail(CloseProtocolError, "a new message began inside a fragmented one")
			}
			messageType = op
		case opContinuation:
			if messageType == -1 {
				return 0, nil, c.fail(CloseProtocolError, "continuation frame with no message")
			}
		default:
			return 0, nil, c.fail(CloseProtocolError, "unknown opcode")
		}
		if len(message)+len(payload) > limit {
			return 0, nil, c.fail(CloseTooBig, "message too big")
		}
		message = append(message, payload...)
		if fin {
			if messageType == Text && !utf8.Valid(message) {
				return 0, nil, c.fail(CloseInvalidData, "text message is not UTF-8")
			}
			if message == nil {
				message = []byte{}
			}
			return messageType, message, nil
		}
	}
}

// fail closes the connection over a protocol violation and returns the error to report.
func (c *Conn) fail(code int, reason string) error {
	c.close(code, reason, false)
	return &CloseError{Code: code, Reason: reason}
}

// validCloseCode reports whether a close frame may carry code (RFC 6455 §7.4).
func validCloseCode(code int) bool {
	switch {
	case code >= 1000 && code <= 1003, code >= 1007 && code <= 1011, code >= 3000 && code <= 4999:
		return true
	}
	return false
}

func (c *Conn) readFrame(limit int) (fin bool, op int, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		return false, 0, nil, err
	}
	fin = head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		return false, 0, nil, c.fail(CloseProtocolError, "reserved bits set")
	}
	op = int(head[0] & 0x0f)
	masked := head[1]&0x80 != 0
	if masked == c.client {
		// RFC 6455 §5.1: a client masks every frame, a server none.
		return false, 0, nil, c.fail(CloseProtocolError, "wrong masking")
	}
	length := uint64(head[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if op >= opClose && (length > 125 || !fin) {
		return false, 0, nil, c.fail(CloseProtocolError, "bad control frame")
	}
	if length > uint64(limit) {
		return false, 0, nil, c.fail(CloseTooBig, "message too big")
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.r, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, op, payload, nil
}

// WriteMessage sends one message of type Text or Binary, unfragmented.
func (c *Conn) WriteMessage(messageType int, data []byte) error {
	if messageType != Text && messageType != Binary {
		return errors.New("websocket: a message is Text or Binary")
	}
	return c.writeFrame(messageType, data)
}

// Ping sends a ping; the peer's pong ends any read waiting on SetReadDeadline.
func (c *Conn) Ping() error { return c.writeFrame(opPing, nil) }

func (c *Conn) writeFrame(op int, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closeSent {
		return ErrClosed
	}
	return c.writeFrameLocked(op, payload)
}

func (c *Conn) writeFrameLocked(op int, payload []byte) error {
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, 0x80|byte(op))
	maskBit := byte(0)
	if c.client {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n <= 125:
		frame = append(frame, maskBit|byte(n))
	case n <= 0xffff:
		frame = append(frame, maskBit|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	default:
		frame = append(frame, maskBit|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	if c.client {
		var mask [4]byte
		_, _ = rand.Read(mask[:])
		frame = append(frame, mask[:]...)
		start := len(frame)
		frame = append(frame, payload...)
		for i := range payload {
			frame[start+i] ^= mask[i%4]
		}
	} else {
		frame = append(frame, payload...)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(frame)
	return err
}

// closeLinger is how long Close keeps the connection open after its close frame, so the frame
// arrives before the TCP connection ends. A reader that gets the peer's answer ends it sooner.
const closeLinger = time.Second

// Close sends a close frame with code and reason, once, and then closes the connection. The
// reason is cut to fit a control frame.
func (c *Conn) Close(code int, reason string) error {
	c.close(code, reason, true)
	return nil
}

// close sends the close frame and ends the connection, after closeLinger when linger is set: when
// this side starts the close, closing at once could reset the connection before the peer reads
// the frame.
func (c *Conn) close(code int, reason string, linger bool) {
	c.closeOnce.Do(func() {
		c.wmu.Lock()
		if !c.closeSent {
			if len(reason) > 123 {
				reason = reason[:123]
			}
			payload := binary.BigEndian.AppendUint16(nil, uint16(code))
			if c.writeFrameLocked(opClose, append(payload, reason...)) != nil {
				linger = false
			}
			c.closeSent = true
		}
		c.wmu.Unlock()
		if linger {
			time.AfterFunc(closeLinger, func() { c.conn.Close() })
		} else {
			c.conn.Close()
		}
	})
	// A second call, the reader answering the peer's close to ours, ends the connection now.
	if !linger {
		c.conn.Close()
	}
}
