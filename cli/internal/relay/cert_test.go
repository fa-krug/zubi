package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDueForRenewal(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: start, NotAfter: start.Add(6 * 24 * time.Hour)}
	if dueForRenewal(leaf, start.Add(95*time.Hour)) {
		t.Error("due before four days")
	}
	if !dueForRenewal(leaf, start.Add(97*time.Hour)) {
		t.Error("not due after four days")
	}
}

// pebble runs Let's Encrypt's test CA, Pebble, from $ZUBI_PEBBLE, validating TLS-ALPN-01 on
// tlsPort. CI installs it; locally: go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1.
type pebble struct {
	directory  string
	management string
	client     *http.Client // trusts Pebble's API certificate
	stop       func()
}

func startPebble(t *testing.T, tlsPort int) *pebble {
	t.Helper()
	bin := os.Getenv("ZUBI_PEBBLE")
	if bin == "" {
		t.Skip("set ZUBI_PEBBLE to a pebble binary to test certificates against an ACME server")
	}
	dir := t.TempDir()

	// Pebble's own HTTPS API needs a certificate; this one is trusted by the test's client only.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pebble"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	roots := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(der)
	roots.AddCert(leaf)

	api, management := freePort(t), freePort(t)
	config := map[string]any{"pebble": map[string]any{
		"listenAddress":                  fmt.Sprintf("127.0.0.1:%d", api),
		"managementListenAddress":        fmt.Sprintf("127.0.0.1:%d", management),
		"certificate":                    certPath,
		"privateKey":                     keyPath,
		"httpPort":                       freePort(t),
		"tlsPort":                        tlsPort,
		"externalAccountBindingRequired": false,
		"profiles": map[string]any{
			"default":    map[string]any{"description": "90 days", "validityPeriod": 7776000},
			"shortlived": map[string]any{"description": "6 days", "validityPeriod": 518400},
		},
	}}
	configJSON, _ := json.Marshal(config)
	configPath := filepath.Join(dir, "pebble.json")
	os.WriteFile(configPath, configJSON, 0o600)

	cmd := exec.Command(bin, "-config", configPath)
	cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0")
	var log strings.Builder
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &pebble{
		directory:  fmt.Sprintf("https://127.0.0.1:%d/dir", api),
		management: fmt.Sprintf("https://127.0.0.1:%d", management),
		client:     &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}},
	}
	p.stop = sync1(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	t.Cleanup(func() {
		p.stop()
		if t.Failed() {
			t.Logf("pebble:\n%s", log.String())
		}
	})
	for i := 0; ; i++ {
		res, err := p.client.Get(p.directory)
		if err == nil {
			res.Body.Close()
			break
		}
		if i == 100 {
			t.Fatalf("pebble didn't start: %v\n%s", err, log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return p
}

func sync1(f func()) func() {
	done := false
	return func() {
		if !done {
			done = true
			f()
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// issuingRoots is what a client would trust: Pebble's root, which it makes anew on every start.
func (p *pebble) issuingRoots(t *testing.T) *x509.CertPool {
	t.Helper()
	res, err := p.client.Get(p.management + "/roots/0")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(body) {
		t.Fatalf("no root in %q", body)
	}
	return roots
}

// serveRelay serves the relay over the manager's TLS config on ln, as `zubi relay` does.
func serveRelay(t *testing.T, ln net.Listener, m *CertManager) {
	t.Helper()
	srv := &http.Server{Handler: New(Options{}).Handler(), TLSConfig: m.TLSConfig()}
	go srv.Serve(tls.NewListener(ln, srv.TLSConfig))
	t.Cleanup(func() { srv.Close() })
}

func TestCertificateForAnIPAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := startPebble(t, ln.Addr().(*net.TCPAddr).Port)
	state := t.TempDir()
	newManager := func() *CertManager {
		return NewCertManager(CertOptions{
			DiscoverIP: func(context.Context) (net.IP, error) { return net.IPv4(127, 0, 0, 1), nil },
			StateDir:   state,
			Directory:  p.directory,
			HTTPClient: p.client,
			Logf:       t.Logf,
		})
	}
	m := newManager()
	serveRelay(t, ln, m)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Obtain(ctx); err != nil {
		t.Fatal(err)
	}
	if m.URL() != "wss://127.0.0.1" {
		t.Errorf("URL %q", m.URL())
	}
	m.mu.Lock()
	leaf := m.cert.Leaf
	m.mu.Unlock()
	if lifetime := leaf.NotAfter.Sub(leaf.NotBefore); lifetime > 7*24*time.Hour {
		t.Errorf("lifetime %s: not the shortlived profile", lifetime)
	}

	// A client that trusts the CA verifies it by the bare address, which sends no server name.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: p.issuingRoots(t)}}}
	res, err := client.Get("https://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "ok\n" {
		t.Fatalf("/health: %q", body)
	}

	// After a restart the saved certificate is used: with the CA gone, a new order would fail.
	p.stop()
	again := newManager()
	if err := again.Obtain(ctx); err != nil {
		t.Fatalf("the saved certificate wasn't used: %v", err)
	}
	for _, name := range []string{"account-key.pem", "certificate.pem", "certificate-key.pem"} {
		info, err := os.Stat(again.path(name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v, want mode 0600", name, info, err)
		}
	}
}

func TestCertificateNeedsTheChallengeAnswered(t *testing.T) {
	// Nothing listens on the port the CA validates on.
	p := startPebble(t, freePort(t))
	m := NewCertManager(CertOptions{
		IP:         net.IPv4(127, 0, 0, 1),
		StateDir:   t.TempDir(),
		Directory:  p.directory,
		HTTPClient: p.client,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := m.Obtain(ctx)
	if err == nil || !strings.Contains(err.Error(), "forward TCP 443") {
		t.Fatalf("got %v, want an explanation of the port", err)
	}
	if testing.Verbose() {
		t.Log(err)
	}
}

func TestCertificateForADomain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := startPebble(t, ln.Addr().(*net.TCPAddr).Port)
	m := NewCertManager(CertOptions{
		Domain:     "LocalHost",
		StateDir:   t.TempDir(),
		Directory:  p.directory,
		HTTPClient: p.client,
	})
	serveRelay(t, ln, m)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Obtain(ctx); err != nil {
		t.Fatal(err)
	}
	if m.URL() != "wss://localhost" {
		t.Errorf("URL %q", m.URL())
	}
}
