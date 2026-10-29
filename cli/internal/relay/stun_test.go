package relay

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// fakeSTUN answers Binding requests with a fixed XOR-MAPPED-ADDRESS, after a stray reply to some
// other request that the client must skip.
func fakeSTUN(t *testing.T, mapped net.IP) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if n != 20 || binary.BigEndian.Uint16(buf) != 0x0001 {
				continue
			}
			stray := make([]byte, 20)
			binary.BigEndian.PutUint16(stray, 0x0101)
			binary.BigEndian.PutUint32(stray[4:], stunMagicCookie)
			conn.WriteTo(stray, from) // transaction ID all zeros: not the client's

			reply := make([]byte, 20, 44)
			copy(reply, buf[:20])
			binary.BigEndian.PutUint16(reply, 0x0101)
			// An unknown attribute with padding first, then XOR-MAPPED-ADDRESS.
			reply = append(reply, 0x80, 0x22, 0, 3, 'z', 'u', 'b', 0)
			reply = append(reply, 0, 0x20, 0, 8, 0, 0x01, 0, 0)
			reply = binary.BigEndian.AppendUint32(reply, binary.BigEndian.Uint32(mapped.To4())^stunMagicCookie)
			binary.BigEndian.PutUint16(reply[2:], uint16(len(reply)-20))
			conn.WriteTo(reply, from)
		}
	}()
	return conn.LocalAddr().String()
}

func TestPublicIPv4(t *testing.T) {
	want := net.IPv4(203, 0, 113, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The first server doesn't answer; the second does.
	silent, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	defer silent.Close()
	got, err := PublicIPv4(ctx, []string{silent.LocalAddr().String(), fakeSTUN(t, want)})
	if err != nil || !got.Equal(want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

func TestParseBindingResponseRejectsGarbage(t *testing.T) {
	txID := make([]byte, 12)
	for _, msg := range [][]byte{nil, make([]byte, 19), make([]byte, 20)} {
		if _, err := parseBindingResponse(msg, txID); err == nil {
			t.Errorf("%v parsed", msg)
		}
	}
}
