package noise

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// The vectors come from an independent implementation (github.com/flynn/noise). The Swift
// implementation in the app's package is held to the same file.
const vectorsPath = "../../../Packages/ZubiAPI/Tests/Fixtures/relay-noise-vectors.json"

type vectorFile struct {
	Protocol string `json:"protocol"`
	Vectors  []struct {
		Name               string `json:"name"`
		Prologue           string `json:"prologue"`
		PSK                string `json:"psk"`
		InitiatorEphemeral string `json:"initiator_ephemeral"`
		ResponderEphemeral string `json:"responder_ephemeral"`
		HandshakeHash      string `json:"handshake_hash"`
		Messages           []struct {
			Payload    string `json:"payload"`
			Ciphertext string `json:"ciphertext"`
		} `json:"messages"`
	} `json:"vectors"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVectors(t *testing.T) {
	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var file vectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Protocol != ProtocolName || len(file.Vectors) == 0 {
		t.Fatalf("protocol %q, %d vectors", file.Protocol, len(file.Vectors))
	}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			ie, err := ecdh.X25519().NewPrivateKey(unhex(t, v.InitiatorEphemeral))
			if err != nil {
				t.Fatal(err)
			}
			re, err := ecdh.X25519().NewPrivateKey(unhex(t, v.ResponderEphemeral))
			if err != nil {
				t.Fatal(err)
			}
			prologue, psk := unhex(t, v.Prologue), unhex(t, v.PSK)
			init, _ := NewHandshake(true, prologue, psk, ie)
			resp, _ := NewHandshake(false, prologue, psk, re)

			m := v.Messages
			out, err := init.WriteMessage(unhex(t, m[0].Payload))
			if err != nil || !bytes.Equal(out, unhex(t, m[0].Ciphertext)) {
				t.Fatalf("message 1: %x, %v", out, err)
			}
			if got, err := resp.ReadMessage(out); err != nil || !bytes.Equal(got, unhex(t, m[0].Payload)) {
				t.Fatalf("message 1 payload: %x, %v", got, err)
			}
			out, err = resp.WriteMessage(unhex(t, m[1].Payload))
			if err != nil || !bytes.Equal(out, unhex(t, m[1].Ciphertext)) {
				t.Fatalf("message 2: %x, %v", out, err)
			}
			if got, err := init.ReadMessage(out); err != nil || !bytes.Equal(got, unhex(t, m[1].Payload)) {
				t.Fatalf("message 2 payload: %x, %v", got, err)
			}
			iSend, iRecv, iHash, err := init.Split()
			if err != nil {
				t.Fatal(err)
			}
			rSend, rRecv, rHash, _ := resp.Split()
			if !bytes.Equal(iHash, unhex(t, v.HandshakeHash)) || !bytes.Equal(rHash, iHash) {
				t.Fatalf("handshake hash %x / %x", iHash, rHash)
			}
			for i, msg := range m[2:] {
				send, recv := iSend, rRecv
				if i%2 == 1 {
					send, recv = rSend, iRecv
				}
				out, err := send.Encrypt(unhex(t, msg.Payload))
				if err != nil || !bytes.Equal(out, unhex(t, msg.Ciphertext)) {
					t.Fatalf("transport %d: %x, %v", i, out, err)
				}
				if got, err := recv.Decrypt(out); err != nil || !bytes.Equal(got, unhex(t, msg.Payload)) {
					t.Fatalf("transport %d payload: %x, %v", i, got, err)
				}
			}
		})
	}
}

func handshake(t *testing.T, ipsk, rpsk []byte) (*Handshake, *Handshake, error) {
	t.Helper()
	init, _ := NewHandshake(true, []byte("p"), ipsk, nil)
	resp, _ := NewHandshake(false, []byte("p"), rpsk, nil)
	m1, err := init.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resp.ReadMessage(m1); err != nil {
		return init, resp, err
	}
	m2, _ := resp.WriteMessage(nil)
	_, err = init.ReadMessage(m2)
	return init, resp, err
}

func TestWrongKeyFails(t *testing.T) {
	a, b := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	if _, _, err := handshake(t, a, b); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v, want ErrDecrypt", err)
	}
	if _, _, err := handshake(t, a, a); err != nil {
		t.Fatal(err)
	}
}

func TestTamperingAndReplayFail(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	init, resp, err := handshake(t, key, key)
	if err != nil {
		t.Fatal(err)
	}
	send, _, _, _ := init.Split()
	_, recv, _, _ := resp.Split()
	c, _ := send.Encrypt([]byte("hello"))
	bad := bytes.Clone(c)
	bad[0] ^= 1
	if _, err := recv.Decrypt(bad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := recv.Decrypt(c); err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Decrypt(c); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("replayed: %v", err)
	}
}

func TestTurnOrder(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	resp, _ := NewHandshake(false, nil, key, nil)
	if _, err := resp.WriteMessage(nil); err == nil {
		t.Error("the responder wrote first")
	}
	if _, err := resp.ReadMessage(make([]byte, 10)); err == nil {
		t.Error("a short message was read")
	}
	if _, _, _, err := resp.Split(); err == nil {
		t.Error("split before the handshake")
	}
	if _, err := NewHandshake(true, nil, []byte("short"), nil); err == nil {
		t.Error("a short pre-shared key was taken")
	}
}
