package main

// A fake Zubi for the command tests. It serves the testdata fixtures and checks every request
// the CLI sends against openapi.json: the method and path exist, path and query parameters are
// declared and valid, required ones are present, the body is JSON that validates against the
// operation's requestBody schema (and is absent where the spec takes none), and the key is sent
// where the spec needs it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "test-key-0123456789"

func TestMain(m *testing.M) {
	// `zubi watch --exec` runs this test binary as its command: it records what it was given.
	if out := os.Getenv("ZUBI_EXEC_HELPER_OUT"); out != "" {
		f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(1)
		}
		fmt.Fprintf(f, "%s %s %v\n", os.Getenv("ZUBI_CHANGE_SOURCE"), os.Getenv("ZUBI_CHANGE_ORIGIN"), os.Args[1:])
		f.Close()
		fmt.Println("helper ran")
		os.Exit(0)
	}
	// Human output shows local times; pin the zone so expectations don't depend on the machine.
	time.Local = time.UTC
	os.Exit(m.Run())
}

// isolate keeps the tests away from the real config file, environment and terminal.
func isolate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zubi", "config.json")
	t.Setenv("ZUBI_CONFIG", path)
	t.Setenv("ZUBI_URL", "")
	t.Setenv("ZUBI_API_KEY", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("TERM", "xterm-256color")
	return path
}

type canned struct {
	status int
	body   string
}

// sent is one request as the fake received it.
type sent struct {
	Method   string
	Template string            // the spec's path, such as /api/reminders/{id}
	Path     map[string]string // the placeholders' values, decoded
	Query    url.Values
	Body     string
}

type fakeZubi struct {
	*httptest.Server
	t   *testing.T
	api *openAPI

	mu       sync.Mutex
	key      string
	override map[string]canned // by "METHOD /path" or by "/path" for any method
	requests []string          // paths, in order
	received []sent
	// streams scripts /api/changes: connection i writes streams[i] and closes; the connection
	// after the last script stays open, silent, until the client goes.
	streams []string
	// connected is signalled each time a client connects to /api/changes.
	connected chan struct{}
}

func newFakeZubi(t *testing.T) *fakeZubi {
	t.Helper()
	f := &fakeZubi{t: t, api: loadSpec(t), key: testKey, override: map[string]canned{}, connected: make(chan struct{}, 100)}
	f.Server = httptest.NewServer(f)
	t.Cleanup(f.Close)
	return f
}

// answer makes the fake answer requests to path ("/api/lists", or "POST /api/reminders" for one
// method only) with status and body.
func (f *fakeZubi) answer(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.override[path] = canned{status, body}
}

func (f *fakeZubi) setStreams(streams ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = streams
}

func (f *fakeZubi) setKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
}

func (f *fakeZubi) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeZubi) sent() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.received...)
}

// args puts the fake's address and key on a command line.
func (f *fakeZubi) args(args ...string) []string {
	return append(args, "--url", f.URL, "--key", testKey)
}

func (f *fakeZubi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t := f.t
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("reading the request body: %v", err)
	}
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path)
	key := f.key
	over, hasOver := f.override[r.Method+" "+r.URL.Path]
	if !hasOver {
		over, hasOver = f.override[r.URL.Path]
	}
	f.mu.Unlock()

	op, params, template, pathValues, ok := f.api.match(r.Method, r.URL.EscapedPath())
	if !ok {
		t.Errorf("CLI drift: the CLI sent %s %s, which the spec doesn't define", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	f.mu.Lock()
	f.received = append(f.received, sent{r.Method, template, pathValues, query, string(body)})
	f.mu.Unlock()

	for name, value := range pathValues {
		p := params["path:"+name]
		for _, problem := range f.api.validate(p.Schema, paramValue(p.Schema, value), "path."+name) {
			t.Errorf("%s %s: %s", r.Method, template, problem)
		}
	}
	keyParam := f.api.queryKeyParam(op)
	for name, values := range query {
		if name == keyParam && keyParam != "" {
			continue // the key, which this operation also takes in the query
		}
		p, ok := params["query:"+name]
		if !ok {
			t.Errorf("CLI drift: %s %s sent query parameter %q, which the spec doesn't declare", r.Method, template, name)
			continue
		}
		if len(values) != 1 {
			t.Errorf("%s %s sent %q %d times", r.Method, template, name, len(values))
		}
		for _, problem := range f.api.validate(p.Schema, paramValue(p.Schema, values[0]), "query."+name) {
			t.Errorf("%s %s: %s", r.Method, template, problem)
		}
	}
	for _, p := range params {
		if p.Required && p.In == "query" && !query.Has(p.Name) {
			t.Errorf("CLI drift: %s %s left out required query parameter %q", r.Method, template, p.Name)
		}
	}
	f.checkBody(r, op, template, body)
	f.checkAccept(r, op, template)

	auth := r.Header.Get("Authorization")
	if f.api.needsAuth(op) {
		if auth != "Bearer "+key && (keyParam == "" || query.Get(keyParam) != key) {
			w.WriteHeader(http.StatusUnauthorized) // Zubi answers 401 with an empty body
			return
		}
	} else if auth != "" {
		t.Errorf("the CLI sent the API key to %s, which needs none", r.URL.Path)
	}

	if hasOver {
		writeJSON(w, over.status, over.body)
		return
	}
	switch template {
	case "/api/changes":
		f.stream(w, r)
	case "/api/calendars/{id}/events.ics":
		if pathValues["id"] != "C1" && pathValues["id"] != "Work" {
			writeJSON(w, 404, `{"error":{"message":"Unknown or hidden calendar"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Write(readFixture(t, "feed.ics"))
	case "/health":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok")
	case "/api/note":
		fixture := map[string]map[string]string{
			"GET":   {noteID: "note.json", lockedNoteID: "note_locked.json"},
			"PATCH": {noteID: "note_edited.json"},
		}[r.Method][query.Get("id")]
		switch {
		case fixture != "":
			writeJSON(w, 200, string(readFixture(t, fixture)))
		case r.Method == "PATCH" && query.Get("id") == lockedNoteID:
			writeJSON(w, 409, string(readFixture(t, "error_locked.json")))
		default:
			writeJSON(w, 404, `{"error":{"message":"Unknown or hidden note"}}`)
		}
	default:
		name, cmd := fixtureFor(r.Method, template)
		if name == "" {
			t.Errorf("fake Zubi has no fixture for %s %s", r.Method, template)
			http.NotFound(w, r)
			return
		}
		writeJSON(w, cmd.status(), string(readFixture(t, name)))
	}
}

// stream answers /api/changes as the server-sent event stream the spec describes, following the
// script set with setStreams.
func (f *fakeZubi) stream(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	n := 0
	for _, p := range f.requests {
		if p == "/api/changes" {
			n++
		}
	}
	var script string
	last := n > len(f.streams)
	if !last {
		script = f.streams[n-1]
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, script)
	w.(http.Flusher).Flush()
	f.connected <- struct{}{}
	if last {
		<-r.Context().Done()
	}
}

// checkAccept: the client asks for the media type the spec documents for the success answer.
func (f *fakeZubi) checkAccept(r *http.Request, op *operation, template string) {
	if !strings.HasPrefix(r.Header.Get("User-Agent"), "zubi-cli/") {
		return // a test fetching a URL the CLI printed, as a calendar app would
	}
	accept := r.Header.Get("Accept")
	for code, resp := range op.Responses {
		if _, ok := resp.Content[accept]; ok && strings.HasPrefix(code, "2") {
			return
		}
	}
	f.t.Errorf("CLI drift: %s %s asked for %q, which the spec's success answer isn't", r.Method, template, accept)
}

// checkBody holds the request body to the operation's requestBody.
func (f *fakeZubi) checkBody(r *http.Request, op *operation, template string, body []byte) {
	t := f.t
	if op.RequestBody == nil {
		if len(body) > 0 {
			t.Errorf("CLI drift: the CLI sent a body to %s %s, which takes none: %s", r.Method, template, body)
		}
		return
	}
	if len(body) == 0 {
		if op.RequestBody.Required {
			t.Errorf("CLI drift: %s %s needs a request body, and the CLI sent none", r.Method, template)
		}
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	content, ok := op.RequestBody.Content[media]
	if err != nil || !ok {
		t.Errorf("CLI drift: %s %s sent a body as %q; the spec takes %v", r.Method, template, r.Header.Get("Content-Type"), keys(op.RequestBody.Content))
		return
	}
	for _, problem := range f.api.validateJSON(content.Schema, body) {
		t.Errorf("CLI drift: %s %s body %s: %s", r.Method, template, body, problem)
	}
}

// fixtureFor finds the fixture that answers method+template, the success answer of its command.
func fixtureFor(method, template string) (string, apiCommand) {
	names := keys(fixtures)
	sort.Strings(names)
	for _, name := range names {
		if cmd, _ := lookup(fixtures[name]); cmd.Method == method && cmd.Path == template {
			return name, cmd
		}
	}
	return "", apiCommand{}
}

const (
	noteID       = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p2"
	lockedNoteID = "x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p3"
)

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

// paramValue turns a query string into the JSON value its schema describes, for validation.
func paramValue(s *schema, raw string) any {
	for _, typ := range s.types() {
		switch typ {
		case "integer", "number":
			if _, err := strconv.ParseFloat(raw, 64); err == nil {
				return json.Number(raw)
			}
		case "boolean":
			if b, err := strconv.ParseBool(raw); err == nil {
				return b
			}
		}
	}
	return raw
}

// runZubi runs the CLI in-process and returns its exit code and output.
func runZubi(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// prettyFixture is what --json prints for a fixture: the body, indented.
func prettyFixture(t *testing.T, name string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, readFixture(t, name), "", "  "); err != nil {
		t.Fatal(err)
	}
	return buf.String() + "\n"
}
