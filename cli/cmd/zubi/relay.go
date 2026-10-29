package main

// `zubi relay`: runs the relay a Mac running Zubi and its clients meet at when they are on
// different networks. It copies end-to-end encrypted messages between them and holds no key.
// By default it serves wss:// with a Let's Encrypt certificate for this network's public IP
// address; plain ws:// takes --no-tls, and a platform that serves wss:// itself takes
// --behind-tls-proxy. internal/relay is the server; docs/features/relay.md the protocol.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fa-krug/zubi/cli/internal/relay"
)

func runRelay(args []string, o options, stdout, stderr io.Writer) int {
	listen, ip, err := relaySettings(args, o)
	if err != nil {
		fmt.Fprintln(stderr, "zubi relay:", err)
		return exitUsage
	}

	logger := log.New(stderr, "", log.LstdFlags)
	ctx, stop := signalContext()
	defer stop()

	server := relay.New(relay.Options{BehindProxy: o.behindProxy, Logf: logger.Printf})
	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Scanners' failed TLS handshakes would fill the log.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		logger.Printf("can't listen on %s: %v", listen, err)
		if errors.Is(err, syscall.EACCES) {
			logger.Printf("ports below 1024 need privileges: run `sudo setcap cap_net_bind_service=+ep %s`, "+
				"or listen on --listen :8443 and forward the router's port 443 there", executable())
		}
		return exitError
	}
	served := make(chan error, 1)

	if o.noTLS || o.behindProxy {
		go func() { served <- httpServer.Serve(ln) }()
		if o.noTLS {
			logger.Printf("serving plain ws:// on %s, as --no-tls asks. What the relay copies stays end-to-end "+
				"encrypted, but which room is used, when and by which address is readable on the way", ln.Addr())
		} else {
			logger.Printf("serving on %s for a proxy that terminates TLS; the relay's address is the proxy's wss:// URL", ln.Addr())
			fmt.Fprintf(stdout, "Next:\n"+
				"  1. Point the proxy's public hostname at http://%s (in a Cloudflare Tunnel: a public\n"+
				"     hostname whose service is HTTP, URL %s; not localhost, which can be another program).\n"+
				"  2. Check it: https://<that hostname>/health answers ok.\n"+
				"  3. On the Mac: Zubi › Settings › Server › Relay, enter wss://<that hostname>, click Apply.\n"+
				"  4. Pair each device with a code from Zubi › Settings › Keys: zubi pair -\n"+
				"To keep the relay running after you log out, run this command again with --install-service.\n",
				localAddr(ln.Addr()), localAddr(ln.Addr()))
		}
	} else {
		stateDir := o.stateDir
		if stateDir == "" {
			config, err := os.UserConfigDir()
			if err != nil {
				logger.Printf("no place for the certificate: %v; pass --state DIR", err)
				return exitError
			}
			stateDir = filepath.Join(config, "zubi", "relay")
		}
		certs := relay.NewCertManager(relay.CertOptions{
			Domain:     o.domain,
			IP:         ip,
			DiscoverIP: func(ctx context.Context) (net.IP, error) { return relay.PublicIPv4(ctx, relay.STUNServers) },
			StateDir:   stateDir,
			Directory:  o.acmeDirectory,
			UserAgent:  "zubi/" + version,
			Logf:       logger.Printf,
		})
		httpServer.TLSConfig = certs.TLSConfig()
		go func() { served <- httpServer.Serve(tls.NewListener(ln, httpServer.TLSConfig)) }()
		obtainCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := certs.Obtain(obtainCtx)
		cancel()
		if err != nil {
			logger.Printf("no certificate, so the relay won't start: %v", err)
			httpServer.Close()
			return exitError
		}
		go certs.Run(ctx)
		fmt.Fprintf(stdout, "Relay ready: %s\n", certs.URL())
		fmt.Fprintf(stdout, "Next: enter it under Zubi › Settings › Server › Relay on the Mac and click Apply, then pair\n"+
			"each device with a code from Zubi › Settings › Keys (zubi pair -). To keep the relay running\n"+
			"after you log out, run this command again with --install-service.\n")
	}

	select {
	case err := <-served:
		logger.Printf("the server stopped: %v", err)
		return exitError
	case <-ctx.Done():
	}
	logger.Printf("shutting down")
	server.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
	return exitOK
}

// relaySettings checks the relay's flags and works out where it listens.
func relaySettings(args []string, o options) (listen string, ip net.IP, err error) {
	if len(args) > 0 {
		return "", nil, usageError(fmt.Sprintf("unexpected argument %q; it takes only flags", args[0]))
	}
	ownCert := o.ip != "" || o.domain != "" || o.stateDir != "" || o.acmeDirectory != ""
	switch {
	case o.noTLS && o.behindProxy:
		return "", nil, usageError("--no-tls and --behind-tls-proxy don't go together")
	case (o.noTLS || o.behindProxy) && ownCert:
		return "", nil, usageError("--ip, --domain, --state and --acme-directory are for the relay's own certificate; " +
			"leave them out with --no-tls or --behind-tls-proxy")
	case o.ip != "" && o.domain != "":
		return "", nil, usageError("pass --ip or --domain, not both")
	}
	if o.ip != "" {
		if ip = net.ParseIP(o.ip); ip == nil {
			return "", nil, usageError(fmt.Sprintf("%q is not an IP address", o.ip))
		}
	}
	listen = o.listen
	if listen == "" {
		switch {
		case !o.noTLS && !o.behindProxy:
			listen = ":443"
		case os.Getenv("PORT") != "": // what hosting platforms tell a service to listen on
			listen = ":" + os.Getenv("PORT")
		default:
			listen = ":8080"
		}
	}
	return listen, ip, nil
}

// signalContext ends on Ctrl-C or SIGTERM, which stop the relay and the tunnel; tests replace it.
var signalContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// localAddr is where a proxy on this machine reaches a listener: loopback for one on all interfaces.
func localAddr(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || tcp.IP.IsUnspecified() {
		_, port, _ := net.SplitHostPort(addr.String())
		return net.JoinHostPort("127.0.0.1", port)
	}
	return tcp.String()
}

func executable() string {
	if path, err := os.Executable(); err == nil {
		return path
	}
	return "zubi"
}
