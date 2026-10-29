package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is an output the test reads while the command still writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// watching runs `zubi watch` in the background with fast reconnects; stop interrupts it, as
// Ctrl-C would, and returns its exit code.
func watching(t *testing.T, args ...string) (stdout, stderr *lockedBuffer, stop func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	savedContext, savedBackoff, savedIdle := watchContext, watchBackoff, watchIdle
	watchContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	watchBackoff = []time.Duration{10 * time.Millisecond}
	t.Cleanup(func() { cancel(); watchContext, watchBackoff, watchIdle = savedContext, savedBackoff, savedIdle })
	stdout, stderr = &lockedBuffer{}, &lockedBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(append([]string{"watch"}, args...), strings.NewReader(""), stdout, stderr) }()
	return stdout, stderr, func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(5 * time.Second):
			t.Fatal("zubi watch didn't stop when interrupted")
			return -1
		}
	}
}

// eventually waits until cond holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const (
	changeReminders = `{"source":"reminders","origin":"elsewhere"}`
	changeNotes     = `{"source":"notes","origin":"zubi"}`
)

func TestWatchHuman(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	// The first connection sends a keep-alive and a change, then drops; the second sends a change
	// split over two data lines, an event of another kind and a keep-alive; the third stays open.
	f.setStreams(
		": keep-alive\n\nevent: change\ndata: "+changeReminders+"\n\n",
		"event: change\r\ndata: {\"source\": \"notes\",\r\ndata: \"origin\": \"zubi\"}\r\n\r\nevent: other\ndata: {}\n\n: ping\n\n",
	)
	stdout, stderr, stop := watching(t, f.args()...)
	eventually(t, "two changes", func() bool { return strings.Count(stdout.String(), "\n") >= 2 })
	eventually(t, "the third connection", func() bool { return len(f.seen()) >= 3 })
	if code := stop(); code != exitOK {
		t.Errorf("exit %d after Ctrl-C, want 0", code)
	}
	if !regexp.MustCompile(`^\d\d:\d\d:\d\d  reminders changed \(elsewhere\)\n\d\d:\d\d:\d\d  notes changed \(through Zubi\)\n$`).MatchString(stdout.String()) {
		t.Errorf("stdout %q", stdout.String())
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "watching "+f.URL) || !strings.Contains(errOut, "Zubi closed the connection; reconnecting in 10ms") {
		t.Errorf("stderr %q", errOut)
	}
}

func TestWatchJSON(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.setStreams("event: change\ndata: " + changeReminders + "\n\nevent: change\ndata: {\"source\": \"notes\", \"origin\": \"zubi\"}\n\nevent: change\ndata: not json\n\n")
	stdout, stderr, stop := watching(t, f.args("--json")...)
	eventually(t, "two changes", func() bool { return strings.Count(stdout.String(), "\n") >= 2 })
	stop()
	if want := changeReminders + "\n" + changeNotes + "\n"; stdout.String() != want {
		t.Errorf("stdout %q, want NDJSON %q", stdout.String(), want)
	}
	if !strings.Contains(stderr.String(), "isn't JSON") || strings.Contains(stderr.String(), "watching") {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestWatchGivesUp(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		answer int
		want   int
		says   string
	}{
		{"key refused", "wrong", 0, exitUnauthorized, "refused (401)"},
		{"no stream on an old Zubi", testKey, 404, exitNotFound, "doesn't report changes"},
		{"forbidden", testKey, 403, exitError, "(403)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			if c.answer != 0 {
				f.answer("/api/changes", c.answer, `{"error":{"message":"Nope"}}`)
			}
			code, out, errOut := runZubi(t, "", "watch", "--url", f.URL, "--key", c.key)
			if code != c.want || out != "" || !strings.Contains(errOut, c.says) {
				t.Errorf("exit %d (want %d), stdout %q, stderr %q", code, c.want, out, errOut)
			}
			if len(f.seen()) != 1 {
				t.Errorf("want one attempt, made %v", f.seen())
			}
		})
	}
}

func TestWatchRetries(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		isolate(t)
		_, stderr, stop := watching(t, "--url", "http://127.0.0.1:1", "--key", testKey, "--timeout", "1s")
		eventually(t, "two retries", func() bool { return strings.Count(stderr.String(), "reconnecting") >= 2 })
		if code := stop(); code != exitOK {
			t.Errorf("exit %d", code)
		}
		if !strings.Contains(stderr.String(), "no Zubi answered at http://127.0.0.1:1") {
			t.Errorf("stderr %q", stderr.String())
		}
	})
	t.Run("server error", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		f.answer("/api/changes", 503, `{"error":{"message":"Busy"}}`)
		_, stderr, stop := watching(t, f.args()...)
		eventually(t, "a retry", func() bool { return len(f.seen()) >= 2 })
		stop()
		if !strings.Contains(stderr.String(), "Busy (503); reconnecting") {
			t.Errorf("stderr %q", stderr.String())
		}
	})
	t.Run("silent connection", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t) // with no stream scripted, every connection stays open and silent
		saved := watchIdle
		t.Cleanup(func() { watchIdle = saved })
		watchIdle = 50 * time.Millisecond
		_, stderr, stop := watching(t, f.args()...)
		eventually(t, "a reconnection after silence", func() bool { return len(f.seen()) >= 3 })
		stop()
		if !strings.Contains(stderr.String(), "Zubi sent nothing for 50ms; reconnecting") {
			t.Errorf("stderr %q", stderr.String())
		}
	})
}

func TestWatchExec(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.setStreams("event: change\ndata: " + changeReminders + "\n\n")
	out := filepath.Join(t.TempDir(), "ran")
	t.Setenv("ZUBI_EXEC_HELPER_OUT", out)
	stdout, stderr, stop := watching(t, f.args("--exec", `"`+os.Args[0]+`" 'two words' --flag`)...)
	eventually(t, "the command to run", func() bool { return strings.Contains(stderr.String(), "helper ran") })
	stop()
	data, _ := os.ReadFile(out)
	if got := string(data); got != "reminders elsewhere [two words --flag]\n" {
		t.Errorf("the command saw %q; stderr %q", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "reminders changed (elsewhere)") || strings.Contains(stdout.String(), "helper") {
		t.Errorf("stdout should hold the change lines only: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "helper ran") {
		t.Errorf("the command's output goes to stderr: %q", stderr.String())
	}
}

func TestSplitWords(t *testing.T) {
	for line, want := range map[string][]string{
		`./refresh.sh`:                     {"./refresh.sh"},
		`  notify-send  "Zubi"  'a b'  `:   {"notify-send", "Zubi", "a b"},
		`sh -c 'echo $ZUBI_CHANGE_SOURCE'`: {"sh", "-c", "echo $ZUBI_CHANGE_SOURCE"},
		`C:\Tools\refresh.exe --all`:       {`C:\Tools\refresh.exe`, "--all"},
		`a""b ''`:                          {"ab", ""},
	} {
		got, err := splitWords(line)
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitWords(%q) = %q, %v; want %q", line, got, err, want)
		}
	}
	for _, line := range []string{"", "   ", `"open`, `it's`} {
		if _, err := splitWords(line); err == nil {
			t.Errorf("splitWords(%q) should fail", line)
		}
	}
	if plainWord("reminders") != "reminders" || plainWord("x; rm -rf /") != "unknown" {
		t.Error("plainWord")
	}
}
