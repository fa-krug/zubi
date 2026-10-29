package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// watchContext ends when the user presses Ctrl-C or the process is told to stop. Tests replace it.
	watchContext = func() (context.Context, context.CancelFunc) {
		return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	}
	// watchBackoff is how long to wait before each reconnection attempt in a row; the last repeats.
	watchBackoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	// watchIdle is how long a silent stream is trusted. Zubi sends a keep-alive every 30 seconds,
	// so a longer silence means the connection died without closing (a sleeping Mac, a dropped
	// tunnel).
	watchIdle = 75 * time.Second
)

// runWatch follows /api/changes until interrupted, reconnecting when the stream drops.
func runWatch(client *Client, cmd apiCommand, o options, g globals, stdout, stderr io.Writer) int {
	ctx, stop := watchContext()
	defer stop()
	stderr = &syncWriter{w: stderr} // the --exec command writes there too

	// A request the CLI itself gets wrong would fail the same way on every attempt.
	if _, err := client.target(cmd, request{}); err != nil {
		return fail(stderr, err)
	}
	var runner *execRunner
	if o.set["exec"] {
		argv, _ := splitWords(o.exec) // buildRequest checked it
		runner = startExec(ctx, argv, stderr)
		defer runner.wait()
	}
	onChange := func(c Change, data []byte) {
		if g.json {
			var line bytes.Buffer
			if json.Compact(&line, data) == nil {
				fmt.Fprintln(stdout, line.String())
			}
		} else {
			fmt.Fprintf(stdout, "%s  %s\n", time.Now().Format("15:04:05"), describeChange(c))
		}
		if runner != nil {
			runner.trigger(c)
		}
	}
	if !g.json {
		fmt.Fprintf(stderr, "zubi watch: watching %s for changes; Ctrl-C stops\n", client.Where)
	}

	failures := 0
	for {
		connected, err := watchOnce(ctx, client, cmd, stderr, onChange)
		if ctx.Err() != nil {
			return exitOK
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status < 500 && apiErr.Status != 429 {
			if apiErr.Status == 404 {
				fmt.Fprintf(stderr, "zubi watch: Zubi at %s doesn't report changes (404); update Zubi on the Mac\n", client.Where)
				return exitNotFound
			}
			return fail(stderr, err) // a refused key or a refusal won't change by retrying
		}
		if connected {
			failures = 0
		}
		delay := watchBackoff[min(failures, len(watchBackoff)-1)]
		failures++
		fmt.Fprintf(stderr, "zubi watch: %v; reconnecting in %s\n", err, delay)
		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(delay):
		}
	}
}

// watchOnce reads one connection's events until it ends. connected says whether Zubi accepted
// the connection, which resets the backoff.
func watchOnce(ctx context.Context, client *Client, cmd apiCommand, stderr io.Writer, onChange func(Change, []byte)) (connected bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := client.send(ctx, cmd, request{})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var silent sync.Once
	var timedOut bool
	idle := time.AfterFunc(watchIdle, func() { silent.Do(func() { timedOut = true; cancel() }) })
	defer idle.Stop()

	reader := bufio.NewReader(resp.Body)
	var event string
	var data []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			idle.Stop()
			silent.Do(func() {}) // timedOut is settled from here on
			switch {
			case timedOut:
				return true, fmt.Errorf("Zubi sent nothing for %s", watchIdle)
			case errors.Is(err, io.EOF):
				return true, errors.New("Zubi closed the connection")
			}
			return true, err
		}
		idle.Reset(watchIdle)
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "": // the end of an event
			if event == "change" && len(data) > 0 {
				payload := []byte(strings.Join(data, "\n"))
				var c Change
				if err := json.Unmarshal(payload, &c); err != nil {
					fmt.Fprintf(stderr, "zubi watch: ignoring a change Zubi sent that isn't JSON: %q\n", payload)
				} else {
					onChange(c, payload)
				}
			}
			event, data = "", nil
		case strings.HasPrefix(line, ":"): // a keep-alive
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
		}
	}
}

func describeChange(c Change) string {
	origin := c.Origin
	switch c.Origin {
	case "zubi":
		origin = "through Zubi"
	case "elsewhere":
		origin = "elsewhere"
	}
	return fmt.Sprintf("%s changed (%s)", c.Source, origin)
}

// execRunner runs the --exec command after changes, one run at a time. Changes that arrive
// during a run are folded into one more run after it, which is what refreshing something needs.
type execRunner struct {
	pending chan Change
	done    chan struct{}
}

func startExec(ctx context.Context, argv []string, stderr io.Writer) *execRunner {
	r := &execRunner{pending: make(chan Change, 1), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-r.pending:
				// The command line is the user's own, split into words without a shell. Zubi's data
				// reaches the command only as these two variables, restricted to plain words.
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Env = append(os.Environ(), "ZUBI_CHANGE_SOURCE="+plainWord(c.Source), "ZUBI_CHANGE_ORIGIN="+plainWord(c.Origin))
				cmd.Stdout = stderr // stdout stays the change lines, for scripts
				cmd.Stderr = stderr
				if err := cmd.Run(); err != nil && ctx.Err() == nil {
					fmt.Fprintf(stderr, "zubi watch: --exec %s: %v\n", argv[0], err)
				}
			}
		}
	}()
	return r
}

func (r *execRunner) trigger(c Change) {
	select {
	case r.pending <- c:
	default: // a run is already queued
	}
}

func (r *execRunner) wait() { <-r.done }

// plainWord keeps a value from the server to letters, or says it wasn't one.
func plainWord(s string) string {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return "unknown"
		}
	}
	return s
}

// splitWords splits a command line into words at spaces and tabs; '…' and "…" keep spaces in a
// word. There is no shell: no variables, globs, pipes or escapes. (Backslashes stay as they are,
// for Windows paths.)
func splitWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, usagef("--exec %q: a %c quote isn't closed", line, quote)
	}
	if inWord {
		words = append(words, word.String())
	}
	if len(words) == 0 {
		return nil, usageError("--exec is empty; give the command to run, such as --exec \"./refresh.sh\"")
	}
	return words, nil
}

// syncWriter lets the --exec command and the watch loop share stderr.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
