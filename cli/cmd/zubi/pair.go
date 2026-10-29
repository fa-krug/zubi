package main

// `zubi pair` and `zubi tunnel`: reaching a Mac through a relay. A pairing code from Zubi's
// Settings names the relay, the Mac's room on it and a key; every command then reaches Zubi
// through the relay, end-to-end encrypted (internal/tunnel), and `zubi tunnel` lends that to other
// programs at a local address.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

// allowPlainRelay lets the tests pair with a ws:// relay; Zubi only ever hands out wss:// ones.
var allowPlainRelay = false

const defaultTunnelAddr = "127.0.0.1:8787"

// runPair checks a pairing code by asking Zubi, through the relay, which key it is, and saves it
// only when that works.
func runPair(args []string, g globals, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "zubi pair: give the pairing code from Zubi › Settings › Keys, or - to read it from stdin")
		return exitUsage
	}
	code := args[0]
	if code == "-" {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintln(stderr, "zubi pair:", err)
			return exitError
		}
		code = line
	}
	p, err := tunnel.ParsePairing(code, allowPlainRelay)
	if err != nil {
		fmt.Fprintln(stderr, "zubi pair:", err)
		return exitUsage
	}
	cfg, path, err := loadConfig()
	if err != nil {
		fmt.Fprintln(stderr, "zubi:", err)
		return exitError
	}

	ep := endpoint{Relay: &p}
	keyCmd, _ := lookup("key")
	res, err := NewClient(ep, p.Secret, g.timeout).Do(context.Background(), keyCmd, request{})
	if err != nil {
		fmt.Fprintln(stderr, "zubi pair: not saved, as Zubi didn't answer through the relay.")
		return fail(stderr, err)
	}
	info := res.(*KeyInfo)

	cfg.Relay, cfg.APIKey, cfg.URL = &p, p.Secret, ""
	if err := saveConfig(path, cfg); err != nil {
		fmt.Fprintln(stderr, "zubi pair:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Paired: Zubi answers through %s, end-to-end encrypted, with the key %q (%s).\nSaved to %s\n",
		p.Relay, info.Name, describeAccess(info.Access), path)
	return exitOK
}

// runTunnel serves Zubi's API at a local address, each connection a session through the relay.
func runTunnel(args []string, o options, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "zubi tunnel: unexpected argument %q; it takes only --listen\n", args[0])
		return exitUsage
	}
	cfg, _, err := loadConfig()
	if err != nil {
		fmt.Fprintln(stderr, "zubi:", err)
		return exitError
	}
	if cfg.Relay == nil {
		fmt.Fprintln(stderr, "zubi tunnel: not paired with a relay; run `zubi pair` with a code from Zubi › Settings › Keys")
		return exitUsage
	}
	p := *cfg.Relay
	addr := o.listen
	if addr == "" {
		addr = defaultTunnelAddr
	}
	logger := log.New(stderr, "", log.LstdFlags)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Printf("can't listen on %s: %v", addr, err)
		return exitError
	}
	ctx, stop := signalContext()
	defer stop()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	fmt.Fprintf(stdout, "Serving Zubi at http://%s, through %s; Ctrl-C stops\n", ln.Addr(), p.Relay)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return exitOK
			}
			logger.Printf("accepting: %v", err)
			return exitError
		}
		go func() {
			dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			session, err := tunnel.Dial(dialCtx, p, nil)
			cancel()
			if err != nil {
				logger.Printf("%s: %v", conn.RemoteAddr(), err)
				conn.Close()
				return
			}
			tunnel.Pipe(session, conn.(*net.TCPConn))
		}()
	}
}
