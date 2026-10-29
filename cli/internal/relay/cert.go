package relay

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncrypt is Let's Encrypt's production ACME directory.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// shortlived is Let's Encrypt's profile for six-day certificates, the only one that covers an IP
// address.
const shortlived = "shortlived"

// CertOptions configure a CertManager.
type CertOptions struct {
	// Domain is the name to certify. Empty means an IP address: IP, or else the one DiscoverIP
	// finds, checked again every CheckInterval so a changed address gets a new certificate.
	Domain string
	IP     net.IP
	// DiscoverIP finds this network's public address; used when Domain and IP are both empty.
	DiscoverIP func(context.Context) (net.IP, error)

	// StateDir keeps the ACME account key and the certificate across restarts.
	StateDir string
	// Directory is the ACME directory URL; default LetsEncrypt.
	Directory string
	// HTTPClient talks to the ACME server; default http.DefaultClient.
	HTTPClient *http.Client
	UserAgent  string
	// CheckInterval is how often Run checks the certificate's age and the public address;
	// default an hour. After a failure it checks again in a tenth of that.
	CheckInterval time.Duration
	Logf          func(format string, args ...any)
}

// CertManager gets a certificate over ACME's TLS-ALPN-01 challenge, answering the challenge on
// the same listener that serves the relay, and keeps it renewed.
type CertManager struct {
	opts CertOptions

	mu         sync.Mutex
	identifier string // what the certificate is for: the domain, or the IP address as text
	cert       *tls.Certificate
	challenge  *tls.Certificate
	client     *acme.Client
}

// NewCertManager makes a CertManager. Nothing is requested until Obtain.
func NewCertManager(opts CertOptions) *CertManager {
	if opts.Directory == "" {
		opts.Directory = LetsEncrypt
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.CheckInterval <= 0 {
		opts.CheckInterval = time.Hour
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &CertManager{opts: opts}
}

// TLSConfig serves the certificate, and the challenge certificate to an ACME validator, which
// asks for the acme-tls/1 protocol. Neither depends on the server name: a client connecting to an
// IP address sends none, and a validator sends the address's reverse-DNS name (RFC 8738).
func (m *CertManager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1", acme.ALPNProto},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if slices.Contains(hello.SupportedProtos, acme.ALPNProto) {
				if m.challenge == nil {
					return nil, errors.New("no ACME challenge in progress")
				}
				return m.challenge, nil
			}
			if m.cert == nil {
				return nil, errors.New("no certificate yet")
			}
			return m.cert, nil
		},
	}
}

// URL is the relay's wss:// address, once there is a certificate.
func (m *CertManager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ip := net.ParseIP(m.identifier); ip != nil && ip.To4() == nil {
		return "wss://[" + m.identifier + "]"
	}
	return "wss://" + m.identifier
}

// Obtain makes sure there is a valid certificate: the one saved in StateDir if it covers the
// identifier and isn't due for renewal, otherwise a new one. The listener serving TLSConfig must
// already be up, on the address's port 443, for the CA to reach the challenge.
func (m *CertManager) Obtain(ctx context.Context) error {
	identifier, err := m.wantedIdentifier(ctx)
	if err != nil {
		return err
	}
	if cert := m.loadSaved(identifier); cert != nil {
		m.mu.Lock()
		m.identifier, m.cert = identifier, cert
		m.mu.Unlock()
		m.opts.Logf("using the saved certificate for %s, valid until %s", identifier,
			cert.Leaf.NotAfter.Local().Format(time.DateTime))
		return nil
	}
	return m.issue(ctx, identifier)
}

// Run renews the certificate when two thirds of its lifetime have passed, and replaces it when
// the public address changes, until ctx ends. A failure is logged and retried; the old
// certificate stays in use meanwhile.
func (m *CertManager) Run(ctx context.Context) {
	wait := m.opts.CheckInterval
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = m.opts.CheckInterval
		if err := m.check(ctx); err != nil && ctx.Err() == nil {
			m.opts.Logf("certificate: %v; trying again in %s", err, m.opts.CheckInterval/10)
			wait = m.opts.CheckInterval / 10
		}
	}
}

func (m *CertManager) check(ctx context.Context) error {
	identifier, err := m.wantedIdentifier(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	current, cert := m.identifier, m.cert
	m.mu.Unlock()
	switch {
	case identifier != current:
		m.opts.Logf("the public address is now %s; getting a certificate for it", identifier)
	case cert == nil || dueForRenewal(cert.Leaf, time.Now()):
		m.opts.Logf("renewing the certificate for %s", identifier)
	default:
		return nil
	}
	if err := m.issue(ctx, identifier); err != nil {
		return err
	}
	if identifier != current {
		m.opts.Logf("the relay's address is now %s", m.URL())
	}
	return nil
}

func (m *CertManager) wantedIdentifier(ctx context.Context) (string, error) {
	switch {
	case m.opts.Domain != "":
		return strings.ToLower(m.opts.Domain), nil
	case m.opts.IP != nil:
		return m.opts.IP.String(), nil
	case m.opts.DiscoverIP == nil:
		return "", errors.New("no domain, IP address or way to find one")
	}
	ip, err := m.opts.DiscoverIP(ctx)
	if err != nil {
		return "", fmt.Errorf("finding this network's public IP address: %w (pass --ip or --domain)", err)
	}
	return ip.String(), nil
}

// dueForRenewal: two thirds of the lifetime are gone, as Let's Encrypt suggests; for a six-day
// certificate that leaves two days to retry in.
func dueForRenewal(leaf *x509.Certificate, now time.Time) bool {
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	return now.After(leaf.NotBefore.Add(lifetime * 2 / 3))
}

// issue runs one ACME order for identifier and installs and saves the certificate.
func (m *CertManager) issue(ctx context.Context, identifier string) error {
	client, err := m.acmeClient(ctx)
	if err != nil {
		return err
	}
	ip := net.ParseIP(identifier)
	var order *acme.Order
	if ip != nil {
		order, err = client.AuthorizeOrder(ctx, acme.IPIDs(identifier), acme.WithOrderIssuanceProfile(shortlived))
	} else {
		order, err = client.AuthorizeOrder(ctx, acme.DomainIDs(identifier))
	}
	if err != nil {
		return fmt.Errorf("ordering a certificate for %s: %w", identifier, err)
	}
	for _, url := range order.AuthzURLs {
		if err := m.authorize(ctx, client, url, identifier); err != nil {
			return err
		}
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return fmt.Errorf("waiting for the certificate order: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.CertificateRequest{}
	if ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{identifier}
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return err
	}
	// CreateCertFromOrder, not CreateOrderCert: a CA that finalizes asynchronously sends no order
	// URL with its answer, and only this one follows the order's own.
	chain, _, err := client.CreateCertFromOrder(ctx, order, csr, true)
	if err != nil {
		return fmt.Errorf("finalizing the certificate: %w", err)
	}
	cert, err := certificate(chain, key)
	if err != nil {
		return err
	}
	if err := cert.Leaf.VerifyHostname(identifier); err != nil {
		return fmt.Errorf("the CA's certificate doesn't cover %s: %w", identifier, err)
	}
	m.mu.Lock()
	m.identifier, m.cert = identifier, cert
	m.mu.Unlock()
	if err := m.save(chain, key); err != nil {
		m.opts.Logf("could not save the certificate (it is in use, but a restart will request another): %v", err)
	}
	m.opts.Logf("got a certificate for %s, valid until %s", identifier, cert.Leaf.NotAfter.Local().Format(time.DateTime))
	return nil
}

// authorize answers the authorization's TLS-ALPN-01 challenge, unless it is already valid.
func (m *CertManager) authorize(ctx context.Context, client *acme.Client, url, identifier string) error {
	authz, err := client.GetAuthorization(ctx, url)
	if err != nil {
		return fmt.Errorf("reading the authorization: %w", err)
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	var chal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "tls-alpn-01" {
			chal = c
		}
	}
	if chal == nil {
		return fmt.Errorf("the CA offers no tls-alpn-01 challenge for %s", identifier)
	}
	challengeCert, err := client.TLSALPN01ChallengeCert(chal.Token, identifier)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.challenge = &challengeCert
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.challenge = nil
		m.mu.Unlock()
	}()
	if _, err := client.Accept(ctx, chal); err != nil {
		return fmt.Errorf("accepting the challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, url); err != nil {
		var authzErr *acme.AuthorizationError
		if errors.As(err, &authzErr) {
			return fmt.Errorf("the CA could not reach this relay at %s port 443 (%v). The relay must answer "+
				"on port 443 of that address from the internet: forward TCP 443 on the router to this "+
				"machine's --listen port, and allow it through the firewall", identifier, err)
		}
		return fmt.Errorf("waiting for the challenge: %w", err)
	}
	return nil
}

// acmeClient loads or creates the account key and registers it once per process.
func (m *CertManager) acmeClient(ctx context.Context) (*acme.Client, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client != nil {
		return client, nil
	}
	key, err := m.accountKey()
	if err != nil {
		return nil, err
	}
	client = &acme.Client{
		Key:          key,
		DirectoryURL: m.opts.Directory,
		HTTPClient:   m.opts.HTTPClient,
		UserAgent:    m.opts.UserAgent,
	}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("registering with %s: %w", m.opts.Directory, err)
	}
	m.mu.Lock()
	m.client = client
	m.mu.Unlock()
	return client, nil
}

func certificate(chain [][]byte, key crypto.Signer) (*tls.Certificate, error) {
	if len(chain) == 0 {
		return nil, errors.New("the CA sent no certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf}, nil
}

// The files in StateDir. The directory is the account's: a different ACME directory gets its own
// subdirectory, so Let's Encrypt's staging and production never share an account.
func (m *CertManager) path(name string) string {
	dir := m.opts.StateDir
	if m.opts.Directory != LetsEncrypt {
		dir = filepath.Join(dir, strings.NewReplacer("://", "_", "/", "_", ":", "_").Replace(m.opts.Directory))
	}
	return filepath.Join(dir, name)
}

func (m *CertManager) accountKey() (crypto.Signer, error) {
	if data, err := os.ReadFile(m.path("account-key.pem")); err == nil {
		return parseKey(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFile(m.path("account-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		return nil, fmt.Errorf("saving the ACME account key: %w", err)
	}
	return key, nil
}

func parseKey(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("not a PEM EC private key")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// loadSaved returns the saved certificate if it covers identifier and isn't due for renewal.
func (m *CertManager) loadSaved(identifier string) *tls.Certificate {
	certPEM, err := os.ReadFile(m.path("certificate.pem"))
	if err != nil {
		return nil
	}
	keyPEM, err := os.ReadFile(m.path("certificate-key.pem"))
	if err != nil {
		return nil
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil
	}
	var chain [][]byte
	for block, rest := pem.Decode(certPEM); block != nil; block, rest = pem.Decode(rest) {
		chain = append(chain, block.Bytes)
	}
	cert, err := certificate(chain, key)
	if err != nil || cert.Leaf.VerifyHostname(identifier) != nil || dueForRenewal(cert.Leaf, time.Now()) {
		return nil
	}
	return cert
}

func (m *CertManager) save(chain [][]byte, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	var certPEM []byte
	for _, c := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c})...)
	}
	if err := writeFile(m.path("certificate-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		return err
	}
	return writeFile(m.path("certificate.pem"), certPEM)
}

// writeFile replaces path's contents in one step, readable by the owner only.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
