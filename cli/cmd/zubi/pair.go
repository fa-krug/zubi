package main

// Pairing and the client service. A pairing code from Zubi's Settings names the relay, the Mac's
// room on it and a key; once paired, every command reaches the Mac through the relay, end-to-end
// encrypted (internal/tunnel). `zubi serve`, run by the zubi-client service, keeps that connection
// at a socket only its owner can reach, and the commands go through it when it runs.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

// allowPlainRelay lets the tests pair with a ws:// relay; Zubi only ever hands out wss:// ones.
var allowPlainRelay = false

// pairWith checks a code by asking the Mac, through the relay, which key it is, and saves it only
// when that works. It returns the key's name.
func pairWith(code string, timeout time.Duration) (tunnel.Pairing, string, error) {
	p, err := tunnel.ParsePairing(code, allowPlainRelay)
	if err != nil {
		return tunnel.Pairing{}, "", usageError(err.Error())
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return tunnel.Pairing{}, "", err
	}
	keyCmd, _ := lookup("key")
	res, err := NewClient(endpoint{Relay: &p}, p.Secret, timeout).Do(context.Background(), keyCmd, request{})
	if err != nil {
		return tunnel.Pairing{}, "", err
	}
	cfg.Relay = &p
	if err := saveConfig(path, cfg); err != nil {
		return tunnel.Pairing{}, "", err
	}
	return p, res.(*KeyInfo).Name, nil
}

// readCode reads a pairing code, a line, from stdin.
func readCode(stdin io.Reader) (string, error) {
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func runPair(args []string, g globals, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "zubi pair: give the pairing code from Zubi's menu bar icon › Copy Pairing Code, or - to read it from stdin")
		return exitUsage
	}
	code := args[0]
	if code == "-" {
		var err error
		if code, err = readCode(stdin); err != nil {
			fmt.Fprintln(stderr, "zubi pair:", err)
			return exitError
		}
	}
	p, name, err := pairWith(code, g.timeout)
	if err != nil {
		fmt.Fprintln(stderr, "zubi pair: not paired.")
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "✓ Paired with your Mac as %q, through %s.\n", name, p.Relay)
	return exitOK
}

// runUnpair forgets the pairing and removes the client service, if there is one.
func runUnpair(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "zubi unpair: unexpected argument %q; it takes none\n", args[0])
		return exitUsage
	}
	cfg, path, err := loadConfig()
	if err != nil {
		fmt.Fprintln(stderr, "zubi:", err)
		return exitError
	}
	if clientServiceInstalled() {
		if code := removeClientService(stdout, stderr); code != exitOK {
			return code
		}
	}
	if cfg.Relay == nil {
		fmt.Fprintln(stdout, "Not paired.")
		return exitOK
	}
	cfg.Relay = nil
	if err := saveConfig(path, cfg); err != nil {
		fmt.Fprintln(stderr, "zubi unpair:", err)
		return exitError
	}
	fmt.Fprintln(stdout, "Unpaired: this computer no longer reaches your Mac. `zubi` pairs it again.")
	return exitOK
}

// runServe is the client service: it listens on the socket and carries each connection to the
// Mac as a session through the relay. It reads the pairing for every session, so pairing again
// takes effect without a restart.
func runServe(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "zubi serve: unexpected argument %q; it takes none\n", args[0])
		return exitUsage
	}
	logger := log.New(stderr, "", log.LstdFlags)
	path, err := socketPath()
	if err != nil {
		logger.Printf("no place for the socket: %v", err)
		return exitError
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logger.Printf("%v", err)
		return exitError
	}
	// A socket left behind by a service that didn't stop cleanly.
	if conn, err := net.Dial("unix", path); err == nil {
		conn.Close()
		logger.Printf("another zubi serve already listens on %s", path)
		return exitError
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		logger.Printf("can't listen on %s: %v", path, err)
		return exitError
	}
	defer os.Remove(path)
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		logger.Printf("%v", err)
		return exitError
	}
	ctx, stop := signalContext()
	defer stop()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	fmt.Fprintf(stdout, "Serving Zubi on %s, through the relay; Ctrl-C stops\n", path)

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
			cfg, _, err := loadConfig()
			if err != nil || cfg.Relay == nil {
				logger.Printf("not paired; `zubi` pairs this computer with a Mac")
				conn.Close()
				return
			}
			dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			session, err := tunnel.Dial(dialCtx, *cfg.Relay, nil)
			cancel()
			if err != nil {
				logger.Printf("%v", err)
				conn.Close()
				return
			}
			tunnel.Pipe(session, conn.(*net.UnixConn))
		}()
	}
}
