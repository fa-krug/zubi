package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/fa-krug/zubi/cli/internal/relay"
	"github.com/fa-krug/zubi/cli/internal/websocket"
)

// Host is the Mac's half of the protocol, in Go: it holds a room on a relay and serves each
// session from a local address. The app has its own, in Swift; this one lets the tests run every
// client against a real relay without a Mac.
type Host struct {
	Relay string
	Key   ed25519.PrivateKey
	// Secret returns a key's secret by its ID; false when there is no such key.
	Secret func(keyID string) (string, bool)
	// Target is the local address each session is connected to, Zubi's API.
	Target string
	TLS    *tls.Config
	// PingInterval is how often the host pings the relay; default 30 seconds.
	PingInterval time.Duration
}

// Room is the room this host registers: its public key, base64url.
func (h *Host) Room() string {
	return base64.RawURLEncoding.EncodeToString(h.Key.Public().(ed25519.PublicKey))
}

// Run registers the room and serves sessions until ctx ends or the control connection does.
// ready is called once the room is registered.
func (h *Host) Run(ctx context.Context, ready func()) error {
	base := strings.TrimRight(h.Relay, "/")
	control, err := websocket.Dial(ctx, base+"/v1/host", h.TLS)
	if err != nil {
		return err
	}
	defer control.Close(websocket.CloseNormal, "")
	stop := context.AfterFunc(ctx, func() { control.Close(websocket.CloseGoingAway, "") })
	defer stop()

	var m relay.Message
	if err := readJSON(control, &m); err != nil || m.Type != "challenge" {
		return errors.New("no challenge from the relay")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(m.Nonce)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(h.Key, append([]byte(relay.RegisterContext), nonce...))
	if err := writeJSON(control, relay.Message{Type: "register", Key: h.Room(),
		Signature: base64.RawURLEncoding.EncodeToString(sig)}); err != nil {
		return err
	}
	if err := readJSON(control, &m); err != nil || m.Type != "registered" {
		return errors.New("the relay refused the registration")
	}
	if ready != nil {
		ready()
	}

	interval := h.PingInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if writeJSON(control, relay.Message{Type: "ping"}) != nil {
					return
				}
			}
		}
	}()
	for {
		if err := readJSON(control, &m); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if m.Type == "session" {
			go h.serve(ctx, base+"/v1/accept/"+m.ID)
		}
	}
}

func (h *Host) serve(ctx context.Context, acceptURL string) {
	ws, err := websocket.Dial(ctx, acceptURL, h.TLS)
	if err != nil {
		return
	}
	session, _, err := Accept(ws, h.Room(), h.Secret)
	if err != nil {
		return
	}
	var d net.Dialer
	local, err := d.DialContext(ctx, "tcp", h.Target)
	if err != nil {
		session.Close()
		return
	}
	Pipe(session, local.(*net.TCPConn))
}

func readJSON(c *websocket.Conn, v any) error {
	_, data, err := c.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.Text, data)
}
