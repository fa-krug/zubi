package main

// --install-service and --uninstall-service on `zubi relay` and `zubi tunnel`: the command, with
// the flags it was given, as a systemd service that starts at boot, restarts after a failure and
// outlives the terminal it was set up from. Linux with systemd only; the service runs as the user
// who set it up (through sudo), so it reads their pairing and keeps the relay's certificate in
// their config directory.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const systemdDir = "/etc/systemd/system"

// serviceFlags are the flags that ask for a service rather than a run; they aren't recorded in it.
var serviceFlags = []string{"install-service", "uninstall-service"}

func runService(command string, raw, positional []string, o options, stdout, stderr io.Writer) int {
	usageErr := func(msg string) int {
		fmt.Fprintf(stderr, "zubi %s: %s\n", command, msg)
		return exitUsage
	}
	if o.installService && o.uninstallService {
		return usageErr("--install-service and --uninstall-service don't go together")
	}
	args := withoutServiceFlags(raw)
	if o.installService {
		if err := checkServiceCommand(command, positional, o); err != nil {
			return usageErr(err.Error())
		}
	}
	if runtime.GOOS != "linux" {
		return usageErr("--install-service sets up a systemd service, on Linux; elsewhere, keep it running " +
			"with your system's own tools")
	}
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return usageErr("there is no systemctl here; --install-service needs systemd")
	}
	if os.Geteuid() != 0 {
		// Writing the unit takes root; sudo runs this same command again, and SUDO_USER tells that
		// run whose service it is.
		sudo, err := exec.LookPath("sudo")
		if err != nil {
			return usageErr("a service needs root; run this as root")
		}
		fmt.Fprintln(stderr, "Setting up the service needs sudo.")
		cmd := exec.Command(sudo, append([]string{executable(), command}, raw...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
		if err := cmd.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode()
			}
			fmt.Fprintf(stderr, "zubi %s: %v\n", command, err)
			return exitError
		}
		return exitOK
	}

	name := "zubi-" + command
	path := filepath.Join(systemdDir, name+".service")
	systemd := func(args ...string) error {
		out, err := exec.Command(systemctl, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if o.uninstallService {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stdout, "There is no %s service.\n", name)
			return exitOK
		}
		_ = systemd("disable", "--now", name)
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(stderr, "zubi %s: %v\n", command, err)
			return exitError
		}
		_ = systemd("daemon-reload")
		fmt.Fprintf(stdout, "Stopped and removed the %s service.\n", name)
		return exitOK
	}

	user := os.Getenv("SUDO_USER")
	if user == "" {
		user = "root"
	}
	unit := serviceUnit(command, executable(), user, args, o)
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		fmt.Fprintf(stderr, "zubi %s: writing %s: %v\n", command, path, err)
		return exitError
	}
	for _, step := range [][]string{{"daemon-reload"}, {"enable", name}, {"restart", name}} {
		if err := systemd(step...); err != nil {
			fmt.Fprintf(stderr, "zubi %s: wrote %s, but %v\n", command, path, err)
			return exitError
		}
	}
	fmt.Fprintf(stdout, "Installed the %s service: it runs `zubi %s` as %s, starts at boot and restarts\n"+
		"after a failure. If the same command still runs in a terminal, stop it there (Ctrl-C).\n"+
		"  journalctl -u %s -f              its log, as it runs\n"+
		"  systemctl status %s              whether it runs\n"+
		"  zubi %s --uninstall-service    stop and remove it\n",
		name, strings.TrimSpace(command+" "+strings.Join(args, " ")), user, name, name, command)
	return exitOK
}

// checkServiceCommand refuses, before anything is installed, a command that couldn't start.
func checkServiceCommand(command string, positional []string, o options) error {
	switch command {
	case "relay":
		_, _, err := relaySettings(positional, o)
		return err
	case "tunnel":
		if len(positional) > 0 {
			return usageError(fmt.Sprintf("unexpected argument %q; it takes only --listen", positional[0]))
		}
		// Under sudo this run reads root's config, not the user's; the run before sudo checked theirs.
		if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
			return nil
		}
		cfg, _, err := loadConfig()
		if err != nil {
			return err
		}
		if cfg.Relay == nil {
			return usageError("not paired with a relay; run `zubi pair` first, as the user the service will run as")
		}
	}
	return nil
}

// withoutServiceFlags is the command line to record in the service.
func withoutServiceFlags(args []string) []string {
	var kept []string
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if strings.HasPrefix(arg, "-") && slices.Contains(serviceFlags, name) {
			continue
		}
		kept = append(kept, arg)
	}
	return kept
}

// serviceUnit is the systemd unit running `zubi command args` as user.
func serviceUnit(command, exe, user string, args []string, o options) string {
	description := "Zubi relay"
	if command == "tunnel" {
		description = "Zubi tunnel: Zubi's API through the relay, on this machine"
	}
	line := []string{systemdQuote(exe), command}
	for _, arg := range args {
		line = append(line, systemdQuote(arg))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by `zubi %s --install-service`; `zubi %s --uninstall-service` removes it.\n", command, command)
	fmt.Fprintf(&b, "[Unit]\nDescription=%s\nWants=network-online.target\nAfter=network-online.target\n\n", description)
	fmt.Fprintf(&b, "[Service]\nExecStart=%s\nUser=%s\nRestart=on-failure\n", strings.Join(line, " "), user)
	if command == "relay" && !o.noTLS && !o.behindProxy {
		// A failed certificate counts against Let's Encrypt's limit of five failed checks an hour.
		b.WriteString("RestartSec=15min\n")
	} else {
		b.WriteString("RestartSec=5s\n")
	}
	if command == "relay" && user != "root" && privilegedPort(o) {
		b.WriteString("AmbientCapabilities=CAP_NET_BIND_SERVICE\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// privilegedPort reports whether the relay listens below 1024, which needs a capability.
func privilegedPort(o options) bool {
	listen, _, err := relaySettings(nil, o)
	if err != nil {
		return false
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 1024
}

// systemdQuote writes one ExecStart argument: quoted when it has spaces or quotes, with the
// specifiers systemd expands (% and $) escaped.
func systemdQuote(arg string) string {
	arg = strings.ReplaceAll(arg, "%", "%%")
	arg = strings.ReplaceAll(arg, "$", "$$")
	if arg != "" && !strings.ContainsAny(arg, " \t\"'\\;") {
		return arg
	}
	arg = strings.ReplaceAll(arg, `\`, `\\`)
	arg = strings.ReplaceAll(arg, `"`, `\"`)
	return `"` + arg + `"`
}
