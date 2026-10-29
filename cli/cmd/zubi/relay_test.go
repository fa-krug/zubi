package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRelayUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"relay", "extra"},
		{"relay", "--no-tls", "--behind-tls-proxy"},
		{"relay", "--no-tls", "--ip", "203.0.113.7"},
		{"relay", "--behind-tls-proxy", "--domain", "relay.example.com"},
		{"relay", "--no-tls", "--state", "/tmp/x"},
		{"relay", "--ip", "203.0.113.7", "--domain", "relay.example.com"},
		{"relay", "--ip", "not-an-ip"},
	} {
		code, out, errOut := runZubi(t, "", args...)
		if code != exitUsage || out != "" || !strings.HasPrefix(errOut, "zubi relay: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want a usage error", args, code, out, errOut)
		}
	}
}

// syncBuffer is a log the relay writes while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startRelayCommand runs `zubi relay` with args until the test stops it, and returns the address
// it logged and a function that stops it and returns its exit code.
func startRelayCommand(t *testing.T, args ...string) (addr string, stop func() int, log *syncBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	saved := relayContext
	relayContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	t.Cleanup(func() { relayContext = saved })

	log = &syncBuffer{}
	codes := make(chan int, 1)
	go func() { codes <- run(append([]string{"relay"}, args...), strings.NewReader(""), io.Discard, log) }()
	listening := regexp.MustCompile(`serving .*on \S*:(\d+)`)
	for i := 0; ; i++ {
		if m := listening.FindStringSubmatch(log.String()); m != nil {
			addr = "127.0.0.1:" + m[1]
			break
		}
		if i == 200 {
			cancel()
			t.Fatalf("the relay didn't start:\n%s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return addr, func() int {
		cancel()
		select {
		case code := <-codes:
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("the relay didn't stop")
			return -1
		}
	}, log
}

func TestRelayWithoutTLS(t *testing.T) {
	addr, stop, log := startRelayCommand(t, "--no-tls", "--listen", ":0")
	if !strings.Contains(log.String(), "plain ws://") {
		t.Errorf("no warning about plain ws:// in %q", log.String())
	}
	res, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("/health: %d", res.StatusCode)
	}
	if code := stop(); code != exitOK {
		t.Fatalf("exit %d after Ctrl-C:\n%s", code, log.String())
	}
}

func TestRelayBehindProxyListensOnPORT(t *testing.T) {
	t.Setenv("PORT", "0")
	addr, stop, _ := startRelayCommand(t, "--behind-tls-proxy")
	// A WebSocket the proxy didn't receive over TLS is refused.
	req, _ := http.NewRequest("GET", "http://"+addr+"/v1/host", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("/v1/host without X-Forwarded-Proto: %d, want 403", res.StatusCode)
	}
	if code := stop(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
}
