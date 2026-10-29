package main

// `zubi` on its own: the connection's status, and the first time, pairing and the client service.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

// The client service: its systemd unit, and how it is set up and removed. Variables, so the tests
// never touch systemd.
var (
	clientUnitPath = systemdDir + "/zubi-client.service"

	setupClientService = func(stdout, stderr io.Writer) int {
		return runService("serve", []string{"--install-service"}, nil, options{installService: true}, stdout, stderr)
	}
	removeClientService = func(stdout, stderr io.Writer) int {
		return runService("serve", []string{"--uninstall-service"}, nil, options{uninstallService: true}, stdout, stderr)
	}
	// canHaveService: a systemd service is what keeps the connection; elsewhere commands connect
	// each time, which works as well, only slower to start.
	canHaveService = func() bool {
		if runtime.GOOS != "linux" {
			return false
		}
		_, err := exec.LookPath("systemctl")
		return err == nil
	}
)

func clientServiceInstalled() bool {
	_, err := os.Stat(clientUnitPath)
	return err == nil
}

func runStatus(stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig()
	if err != nil {
		fmt.Fprintln(stderr, "zubi:", err)
		return exitError
	}
	if cfg.Relay == nil {
		fmt.Fprint(stdout, "This computer isn't paired with a Mac yet.\n"+
			"On your Mac, click Zubi's menu bar icon › Copy Pairing Code, and paste it here.\n\n"+
			"Pairing code: ")
		code, err := readCode(stdin)
		fmt.Fprintln(stdout)
		if err != nil || code == "" {
			fmt.Fprintln(stderr, "zubi: no code given; run `zubi` again to pair, or `zubi pair CODE`")
			return exitUsage
		}
		p, name, err := pairWith(code, 20*time.Second)
		if err != nil {
			fmt.Fprintln(stderr, "zubi: not paired.")
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "✓ Paired with your Mac as %q, through %s.\n\n", name, p.Relay)
		if canHaveService() && !clientServiceInstalled() {
			fmt.Fprintln(stdout, "Setting up the zubi-client service, which keeps the connection, also after you log out.")
			if setupClientService(stdout, stderr) != exitOK {
				fmt.Fprintln(stderr, "zubi: the service isn't set up; commands still work, connecting each time.")
			}
			fmt.Fprintln(stdout)
		}
		cfg.Relay = &p
	}
	return printStatus(*cfg.Relay, stdout)
}

// printStatus checks each link: the pairing, the service, and the Mac through the relay.
func printStatus(p tunnel.Pairing, stdout io.Writer) int {
	line := func(ok bool, format string, args ...any) {
		mark := "✓"
		if !ok {
			mark = "✗"
		}
		fmt.Fprintf(stdout, "%s %s\n", mark, fmt.Sprintf(format, args...))
	}
	name := p.Name
	if name == "" {
		name = p.KeyID
	}
	line(true, "Paired as %q, through %s", name, p.Relay)

	socket, _ := socketPath()
	serviceUp := false
	if conn, err := net.DialTimeout("unix", socket, 200*time.Millisecond); err == nil {
		conn.Close()
		serviceUp = true
	}
	switch {
	case serviceUp:
		line(true, "The zubi-client service keeps the connection")
	case clientServiceInstalled():
		line(false, "The zubi-client service isn't running: `systemctl status zubi-client` says why")
	case canHaveService():
		fmt.Fprintln(stdout, "· No zubi-client service: commands connect each time. `zubi serve --install-service` sets it up")
	default:
		fmt.Fprintln(stdout, "· Commands connect each time; this system has no service to keep the connection")
	}

	ep := endpoint{Relay: &p}
	if serviceUp {
		ep.Socket = socket
	}
	keyCmd, _ := lookup("key")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := NewClient(ep, p.Secret, 20*time.Second).Do(ctx, keyCmd, request{})
	var refused tunnel.RefusedError
	var unreachable *UnreachableError
	switch {
	case err == nil:
		line(true, "Your Mac answers")
		fmt.Fprintln(stdout, "\nTry `zubi reminders`, `zubi events` or `zubi notes`; `zubi help` lists everything.")
		return exitOK
	case errors.As(err, &refused):
		line(false, "Your Mac refused the key: it was deleted or regenerated. `zubi pair` with a new code")
		return exitUnauthorized
	case errors.As(err, &unreachable):
		line(false, "Your Mac doesn't answer: %v", unreachable.Err)
		return exitUnreachable
	default:
		line(false, "Your Mac doesn't answer: %v", err)
		return exitError
	}
}
