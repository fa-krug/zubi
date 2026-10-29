package websocket

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoServer echoes every message back until the client closes.
func echoServer(t *testing.T, maxMessage int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		c.MaxMessage = maxMessage
		for {
			op, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(op, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(CloseNormal, "") })
	return c
}

func TestEchoEverySize(t *testing.T) {
	c := dial(t, echoServer(t, 1<<20))
	c.MaxMessage = 1 << 20
	for _, n := range []int{0, 1, 125, 126, 0xffff, 0x10000, 300000} {
		for _, op := range []int{Text, Binary} {
			sent := bytes.Repeat([]byte("z"), n)
			if err := c.WriteMessage(op, sent); err != nil {
				t.Fatal(err)
			}
			gotOp, got, err := c.ReadMessage()
			if err != nil {
				t.Fatalf("%d bytes: %v", n, err)
			}
			if gotOp != op || !bytes.Equal(got, sent) {
				t.Fatalf("%d bytes, type %d: got type %d and %d bytes", n, op, gotOp, len(got))
			}
		}
	}
}

func TestPingIsAnsweredAndPongSkipped(t *testing.T) {
	c := dial(t, echoServer(t, 0))
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMessage(Text, []byte("after ping")); err != nil {
		t.Fatal(err)
	}
	_, got, err := c.ReadMessage()
	if err != nil || string(got) != "after ping" {
		t.Fatalf("got %q, %v; want the message after the pong", got, err)
	}
}

func TestTooBigClosesWith1009(t *testing.T) {
	c := dial(t, echoServer(t, 10))
	if err := c.WriteMessage(Binary, make([]byte, 11)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.ReadMessage()
	var closeErr *CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != CloseTooBig {
		t.Fatalf("got %v, want a close with %d", err, CloseTooBig)
	}
}

func TestCloseCarriesCodeAndReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		c.Close(4001, "bye")
	}))
	defer srv.Close()
	c := dial(t, "ws"+strings.TrimPrefix(srv.URL, "http"))
	_, _, err := c.ReadMessage()
	var closeErr *CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != 4001 || closeErr.Reason != "bye" {
		t.Fatalf("got %v, want close 4001 bye", err)
	}
	if err := c.WriteMessage(Text, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("a write after the close: %v, want ErrClosed", err)
	}
}

func TestRefusedHandshake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such room", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	var hsErr *HandshakeError
	if !errors.As(err, &hsErr) || hsErr.Status != 404 || hsErr.Message != "no such room" {
		t.Fatalf("got %v, want a 404 handshake error with the body", err)
	}
}

func TestUpgradeRefusesPlainRequests(t *testing.T) {
	url := strings.Replace(echoServer(t, 0), "ws://", "http://", 1)
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUpgradeRequired || res.Header.Get("Sec-WebSocket-Version") != "13" {
		t.Fatalf("got %d, want 426 with the version", res.StatusCode)
	}
}

// rawClient does the handshake by hand, so a test can send frames this package would never write.
func rawClient(t *testing.T, url string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(url, "ws://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	r := bufio.NewReader(conn)
	res, err := http.ReadResponse(r, nil)
	if err != nil || res.StatusCode != 101 {
		t.Fatalf("handshake: %v %v", res, err)
	}
	// RFC 6455 §1.3's worked example.
	if got := res.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("Sec-WebSocket-Accept %q", got)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, r
}

func frame(fin bool, op byte, masked bool, payload []byte) []byte {
	b := op
	if fin {
		b |= 0x80
	}
	out := []byte{b}
	if masked {
		out = append(out, 0x80|byte(len(payload)), 1, 2, 3, 4)
		for i, p := range payload {
			out = append(out, p^byte(i%4+1))
		}
		return out
	}
	return append(append(out, byte(len(payload))), payload...)
}

// readServerFrame reads one unmasked frame of up to 125 bytes.
func readServerFrame(t *testing.T, r *bufio.Reader) (op byte, payload []byte) {
	t.Helper()
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatal(err)
	}
	payload = make([]byte, head[1]&0x7f)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatal(err)
	}
	return head[0] & 0x0f, payload
}

func TestFragmentedMessage(t *testing.T) {
	conn, r := rawClient(t, echoServer(t, 0))
	conn.Write(frame(false, Text, true, []byte("hel")))
	conn.Write(frame(true, opPing, true, []byte("p"))) // a control frame may sit between fragments
	conn.Write(frame(true, opContinuation, true, []byte("lo")))
	if op, payload := readServerFrame(t, r); op != opPong || string(payload) != "p" {
		t.Fatalf("got op %d %q, want the pong first", op, payload)
	}
	if op, payload := readServerFrame(t, r); op != Text || string(payload) != "hello" {
		t.Fatalf("got op %d %q, want the joined message", op, payload)
	}
}

func TestUnmaskedClientFrameIsAProtocolError(t *testing.T) {
	conn, r := rawClient(t, echoServer(t, 0))
	conn.Write(frame(true, Text, false, []byte("x")))
	op, payload := readServerFrame(t, r)
	if op != opClose || binary.BigEndian.Uint16(payload) != CloseProtocolError {
		t.Fatalf("got op %d %v, want close %d", op, payload, CloseProtocolError)
	}
}

func TestInvalidUTF8Text(t *testing.T) {
	conn, r := rawClient(t, echoServer(t, 0))
	conn.Write(frame(true, Text, true, []byte{0xff, 0xfe}))
	op, payload := readServerFrame(t, r)
	if op != opClose || binary.BigEndian.Uint16(payload) != CloseInvalidData {
		t.Fatalf("got op %d %v, want close %d", op, payload, CloseInvalidData)
	}
}
