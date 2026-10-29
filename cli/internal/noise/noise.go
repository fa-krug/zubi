// Package noise is the one Noise protocol the relay's sessions use,
// Noise_NNpsk0_25519_AESGCM_SHA256 (https://noiseprotocol.org/noise.html): two messages, fresh
// X25519 keys on both sides for forward secrecy, and a pre-shared key, the device key, that
// authenticates both. Only the standard library: X25519 from crypto/ecdh, AES-GCM, SHA-256.
package noise

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// ProtocolName is exactly 32 bytes, SHA-256's length, so it is the initial hash as it stands.
const ProtocolName = "Noise_NNpsk0_25519_AESGCM_SHA256"

const (
	// MaxMessage is the largest Noise message.
	MaxMessage = 65535
	tagSize    = 16
	// MaxPlaintext is the most one transport message carries.
	MaxPlaintext = MaxMessage - tagSize
	keySize      = 32
)

// ErrDecrypt is any message that doesn't authenticate: the wrong pre-shared key or prologue, or
// tampering on the way.
var ErrDecrypt = errors.New("noise: message failed to authenticate")

// CipherState encrypts one direction of a session. Not safe for concurrent use.
type CipherState struct {
	aead cipher.AEAD
	n    uint64
}

func newCipherState(key []byte) *CipherState {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // a 32-byte key never fails
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &CipherState{aead: aead}
}

// nonce is 32 bits of zeros and the counter, big-endian, as the spec has it for AES-GCM.
func (c *CipherState) nonce() []byte {
	var n [12]byte
	binary.BigEndian.PutUint64(n[4:], c.n)
	return n[:]
}

func (c *CipherState) encryptWithAD(ad, plaintext []byte) ([]byte, error) {
	if c.n == ^uint64(0) {
		return nil, errors.New("noise: nonces exhausted")
	}
	out := c.aead.Seal(nil, c.nonce(), plaintext, ad)
	c.n++
	return out, nil
}

func (c *CipherState) decryptWithAD(ad, ciphertext []byte) ([]byte, error) {
	if c.n == ^uint64(0) {
		return nil, errors.New("noise: nonces exhausted")
	}
	out, err := c.aead.Open(nil, c.nonce(), ciphertext, ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	c.n++
	return out, nil
}

// Encrypt seals one transport message of at most MaxPlaintext bytes.
func (c *CipherState) Encrypt(plaintext []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, errors.New("noise: plaintext too long")
	}
	return c.encryptWithAD(nil, plaintext)
}

// Decrypt opens one transport message.
func (c *CipherState) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) > MaxMessage {
		return nil, ErrDecrypt
	}
	return c.decryptWithAD(nil, ciphertext)
}

type symmetricState struct {
	cs *CipherState // nil until the first key is mixed in
	ck [32]byte
	h  [32]byte
}

func (s *symmetricState) mixHash(data []byte) {
	sum := sha256.New()
	sum.Write(s.h[:])
	sum.Write(data)
	sum.Sum(s.h[:0])
}

// hkdf is the spec's HKDF over HMAC-SHA256, giving two or three outputs.
func hkdf(ck, ikm []byte, outputs int) [][32]byte {
	mac := func(key, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	temp := mac(ck, ikm)
	var out [][32]byte
	prev := []byte{}
	for i := 1; i <= outputs; i++ {
		next := mac(temp, append(prev, byte(i)))
		var o [32]byte
		copy(o[:], next)
		out = append(out, o)
		prev = next
	}
	return out
}

func (s *symmetricState) mixKey(ikm []byte) {
	o := hkdf(s.ck[:], ikm, 2)
	s.ck = o[0]
	s.cs = newCipherState(o[1][:])
}

func (s *symmetricState) mixKeyAndHash(ikm []byte) {
	o := hkdf(s.ck[:], ikm, 3)
	s.ck = o[0]
	s.mixHash(o[1][:])
	s.cs = newCipherState(o[2][:])
}

func (s *symmetricState) encryptAndHash(plaintext []byte) ([]byte, error) {
	out := plaintext
	if s.cs != nil {
		var err error
		if out, err = s.cs.encryptWithAD(s.h[:], plaintext); err != nil {
			return nil, err
		}
	}
	s.mixHash(out)
	return out, nil
}

func (s *symmetricState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	out := ciphertext
	if s.cs != nil {
		var err error
		if out, err = s.cs.decryptWithAD(s.h[:], ciphertext); err != nil {
			return nil, err
		}
	}
	s.mixHash(ciphertext)
	return out, nil
}

// Handshake runs NNpsk0 for one side: the initiator writes the first message, the responder
// answers. Not safe for concurrent use.
type Handshake struct {
	ss        symmetricState
	initiator bool
	psk       []byte
	e         *ecdh.PrivateKey
	reBytes   []byte // the initiator's ephemeral key, which the responder's ee needs
	step      int
}

// NewHandshake starts a handshake. Both sides must use the same prologue and 32-byte psk.
// ephemeral is nil except in tests, which fix it to reproduce known messages.
func NewHandshake(initiator bool, prologue, psk []byte, ephemeral *ecdh.PrivateKey) (*Handshake, error) {
	if len(psk) != keySize {
		return nil, errors.New("noise: the pre-shared key must be 32 bytes")
	}
	if ephemeral == nil {
		var err error
		if ephemeral, err = ecdh.X25519().GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	h := &Handshake{initiator: initiator, psk: psk, e: ephemeral}
	copy(h.ss.h[:], ProtocolName)
	h.ss.ck = h.ss.h
	h.ss.mixHash(prologue)
	return h, nil
}

// WriteMessage writes this side's next message (the first for an initiator, the second for a
// responder) with payload encrypted in it.
func (h *Handshake) WriteMessage(payload []byte) ([]byte, error) {
	if h.step != h.myStep() {
		return nil, errors.New("noise: not this side's turn to write")
	}
	if h.step == 0 {
		h.ss.mixKeyAndHash(h.psk) // psk
	}
	pub := h.e.PublicKey().Bytes() // e
	h.ss.mixHash(pub)
	h.ss.mixKey(pub)
	if h.step == 1 {
		if err := h.mixEE(); err != nil {
			return nil, err
		}
	}
	sealed, err := h.ss.encryptAndHash(payload)
	if err != nil {
		return nil, err
	}
	h.step++
	return append(pub, sealed...), nil
}

// ReadMessage reads the other side's message and returns its payload.
func (h *Handshake) ReadMessage(msg []byte) ([]byte, error) {
	if h.step == h.myStep() || h.step > 1 {
		return nil, errors.New("noise: not this side's turn to read")
	}
	if len(msg) < keySize+tagSize || len(msg) > MaxMessage {
		return nil, ErrDecrypt
	}
	if h.step == 0 {
		h.ss.mixKeyAndHash(h.psk)
	}
	re := msg[:keySize]
	h.ss.mixHash(re)
	h.ss.mixKey(re)
	if h.step == 1 {
		if err := h.mixEEWith(re); err != nil {
			return nil, err
		}
	} else {
		h.reBytes = append([]byte(nil), re...)
	}
	payload, err := h.ss.decryptAndHash(msg[keySize:])
	if err != nil {
		return nil, err
	}
	h.step++
	return payload, nil
}

func (h *Handshake) myStep() int {
	if h.initiator {
		return 0
	}
	return 1
}

// mixEE is the responder's ee, with the initiator's key from the first message.
func (h *Handshake) mixEE() error { return h.mixEEWith(h.reBytes) }

func (h *Handshake) mixEEWith(re []byte) error {
	remote, err := ecdh.X25519().NewPublicKey(re)
	if err != nil {
		return ErrDecrypt
	}
	shared, err := h.e.ECDH(remote)
	if err != nil {
		return ErrDecrypt // a low-order point
	}
	h.ss.mixKey(shared)
	return nil
}

// Complete reports whether both messages are done.
func (h *Handshake) Complete() bool { return h.step == 2 }

// Split ends the handshake: send and receive cipher states for this side, and the handshake
// hash, which both sides share.
func (h *Handshake) Split() (send, recv *CipherState, hash []byte, err error) {
	if !h.Complete() {
		return nil, nil, nil, errors.New("noise: handshake not complete")
	}
	o := hkdf(h.ss.ck[:], nil, 2)
	c1, c2 := newCipherState(o[0][:]), newCipherState(o[1][:])
	if h.initiator {
		return c1, c2, append([]byte(nil), h.ss.h[:]...), nil
	}
	return c2, c1, append([]byte(nil), h.ss.h[:]...), nil
}
