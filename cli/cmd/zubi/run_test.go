package main

import (
	"context"
	"flag"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCommandsHuman(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"lists"}, "● Groceries  iCloud\n● Errands  On My Mac  read-only\n"},
		{[]string{"reminders"}, "" +
			"[ ] Buy milk !!!  due Mon 2 Nov  Groceries\n" +
			"[ ] Call plumber !!  due Tue 3 Nov 08:00  Errands\n" +
			"[x] Return book  Errands\n"},
		{[]string{"reminders", "--ids"}, "" +
			"[ ] Buy milk !!!  due Mon 2 Nov  Groceries\n    R1\n" +
			"[ ] Call plumber !!  due Tue 3 Nov 08:00  Errands\n    R2\n" +
			"[x] Return book  Errands\n    R3\n"},
		{[]string{"calendars"}, "● Work  iCloud\n● Personal  iCloud  read-only\n"},
		{[]string{"events"}, "" +
			"Monday 2 November\n" +
			"  ● all day      Team offsite  @ Lake House  Work\n" +
			"  ● 09:00–09:15  Standup  Work\n" +
			"\n" +
			"Tuesday 3 November\n" +
			"  ● 18:30–20:00  Dinner  Personal\n"},
		{[]string{"events", "--ids"}, "" +
			"Monday 2 November\n" +
			"  ● all day      Team offsite  @ Lake House  Work\n" +
			"    E1 --occurrence 2026-11-02\n" +
			"  ● 09:00–09:15  Standup  Work\n" +
			"    E2 --occurrence 2026-11-02T09:00:00Z\n" +
			"\n" +
			"Tuesday 3 November\n" +
			"  ● 18:30–20:00  Dinner  Personal\n" +
			"    E3 --occurrence 2026-11-03T18:30:00Z\n"},
		{[]string{"folders"}, "" +
			"Notes  iCloud\n" +
			"├── Recipes\n" +
			"│   └── Baking\n" +
			"└── Travel  read-only\n" +
			"Shared  iCloud  read-only\n" + // its parent isn't exposed, so it sits at the top
			"Work  On My Mac\n"},
		{[]string{"notes"}, "" +
			"2026-10-28 09:15  Sourdough\n    " + noteID + "\n" +
			"2026-10-01 20:00  Passwords [locked]\n    " + lockedNoteID + "\n"},
		{[]string{"note", noteID}, "Sourdough\nModified 2026-10-28 09:15\n\nSourdough\n500 g flour\n350 g water\n"},
		{[]string{"note", noteID, "--html"}, "<div><h1>Sourdough</h1></div><div>500 g flour</div><div>350 g water</div>\n"},
		{[]string{"note", lockedNoteID}, "Passwords\nModified 2026-10-01 20:00\n\nThis note is locked; Zubi serves its metadata only.\n"},
		{[]string{"note", lockedNoteID, "--html"}, "Passwords\nModified 2026-10-01 20:00\n\nThis note is locked; Zubi serves its metadata only.\n"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			code, out, errOut := runZubi(t, "", f.args(c.args...)...)
			if code != exitOK {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if out != c.want {
				t.Errorf("stdout:\n%s\nwant:\n%s", out, c.want)
			}
			if strings.Contains(out, "\x1b") {
				t.Errorf("escape codes in output that isn't a terminal: %q", out)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	// No key: health is public.
	code, out, errOut := runZubi(t, "", "health", "--url", f.URL)
	if code != exitOK || out != "ok at "+f.URL+"\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runZubi(t, "", "health", "--json", "--url", f.URL)
	if code != exitOK || out != "ok\n" {
		t.Errorf("--json: exit %d, stdout %q", code, out)
	}
	// With a key, it says whose key it is; a refused key doesn't fail the check.
	code, out, errOut = runZubi(t, "", f.args("health")...)
	if code != exitOK || out != "ok at "+f.URL+"\nkey Laptop, read & write\n" {
		t.Errorf("with a key: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runZubi(t, "", "health", "--url", f.URL, "--key", "wrong")
	if code != exitOK || !strings.Contains(out, "key refused (401)") {
		t.Errorf("with a wrong key: exit %d, stdout %q", code, out)
	}
	code, out, _ = runZubi(t, "", f.args("health", "--json")...)
	if code != exitOK || out != "ok\n" {
		t.Errorf("--json with a key: exit %d, stdout %q", code, out)
	}
}

func TestEmptyResults(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.answer("/api/lists", 200, `{"lists":[]}`)
	f.answer("/api/events", 200, `{"events":[]}`)
	f.answer("/api/notes/folders", 200, `{"folders":[]}`)
	for args, want := range map[string]string{
		"lists":   "No reminder lists exposed",
		"events":  "No events.",
		"folders": "No Notes folders exposed",
	} {
		code, out, _ := runZubi(t, "", f.args(args)...)
		if code != exitOK || !strings.Contains(out, want) {
			t.Errorf("%s: exit %d, stdout %q", args, code, out)
		}
	}
}

func TestCommandsJSON(t *testing.T) {
	cases := map[string][]string{
		"lists.json":       {"lists"},
		"reminders.json":   {"reminders", "--due", "today"},
		"calendars.json":   {"calendars"},
		"events.json":      {"events", "--from", "2026-11-02", "--to", "2026-11-06", "--calendar", "Work"},
		"folders.json":     {"folders"},
		"notes.json":       {"notes", "--folder", "Recipes", "--limit", "20"},
		"note.json":        {"note", noteID},
		"note_locked.json": {"note", lockedNoteID},
		"key.json":         {"key"},
	}
	for fixture, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolate(t)
			t.Setenv("CLICOLOR_FORCE", "1") // --json is never coloured
			f := newFakeZubi(t)
			code, out, errOut := runZubi(t, "", f.args(append(args, "--json")...)...)
			if code != exitOK {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if want := prettyFixture(t, fixture); out != want {
				t.Errorf("stdout:\n%s\nwant the server's body, indented:\n%s", out, want)
			}
			if got := f.seen(); len(got) != 1 {
				t.Errorf("--json should make one request, made %v", got)
			}
		})
	}
}

// TestEveryQueryParameterSent drives each flag, so the fake checks each parameter against the spec.
func TestEveryQueryParameterSent(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	runs := [][]string{
		{"reminders", "--list", "Groceries", "--completed", "--search", "milk"},
		{"reminders", "--due", "overdue", "-q", "milk"},
		{"events", "--from", "2026-11-02", "--to", "2026-11-06", "--calendar", "Work", "-q", "standup"},
		{"notes", "--folder", "Recipes", "--limit", "500", "--search=pancake"},
		{"note", noteID},
		{"feed", "Work", "--from", "2026-10-01", "--to", "2026-12-31", "--download"},
	}
	for _, args := range runs {
		if code, _, errOut := runZubi(t, "", f.args(args...)...); code != exitOK {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	var searched []string
	for _, r := range f.sent() {
		if r.Query.Has("q") {
			searched = append(searched, r.Template+"?q="+r.Query.Get("q"))
		}
	}
	want := []string{"/api/reminders?q=milk", "/api/reminders?q=milk", "/api/events?q=standup", "/api/notes?q=pancake"}
	if !slices.Equal(searched, want) {
		t.Errorf("--search and -q sent %v, want %v", searched, want)
	}
}

func TestColour(t *testing.T) {
	work := "\x1b[38;2;27;173;248m●\x1b[0m"     // #1BADF8, the Work calendar
	groceries := "\x1b[38;2;255;149;0m●\x1b[0m" // #FF9500

	t.Run("forced on", func(t *testing.T) {
		isolate(t)
		t.Setenv("CLICOLOR_FORCE", "1")
		f := newFakeZubi(t)
		_, out, _ := runZubi(t, "", f.args("lists")...)
		if !strings.Contains(out, groceries) {
			t.Errorf("lists: want the list's colour dot, got %q", out)
		}
		if !strings.Contains(out, "\x1b[2mOn My Mac\x1b[0m") || !strings.Contains(out, "\n● Errands") {
			t.Errorf("lists: a list without a colour gets a plain dot, accounts are dimmed; got %q", out)
		}
		if !strings.Contains(out, "  \x1b[2mread-only\x1b[0m\n") || strings.Count(out, "read-only") != 1 {
			t.Errorf("lists: the read-only list is marked, dimmed, and only it; got %q", out)
		}
		_, out, _ = runZubi(t, "", f.args("events")...)
		if !strings.Contains(out, "  "+work+" ") {
			t.Errorf("events: want the calendar's colour dot, got %q", out)
		}
		if !slices.Contains(f.seen(), "/api/calendars") {
			t.Errorf("events in colour should look up calendar colours; requests %v", f.seen())
		}
	})

	off := map[string]func(t *testing.T) []string{
		"not a terminal": func(t *testing.T) []string { return nil },
		"NO_COLOR": func(t *testing.T) []string {
			t.Setenv("CLICOLOR_FORCE", "1")
			t.Setenv("NO_COLOR", "1")
			return nil
		},
		"--no-color": func(t *testing.T) []string {
			t.Setenv("CLICOLOR_FORCE", "1")
			return []string{"--no-color"}
		},
	}
	for name, setup := range off {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			extra := setup(t)
			f := newFakeZubi(t)
			for _, command := range []string{"lists", "reminders", "calendars", "events", "folders", "notes", "events --ids"} {
				_, out, _ := runZubi(t, "", f.args(append(strings.Fields(command), extra...)...)...)
				if strings.Contains(out, "\x1b") {
					t.Errorf("%s: escape codes with colour off: %q", command, out)
				}
			}
			lookups := 0
			for _, path := range f.seen() {
				if path == "/api/calendars" {
					lookups++
				}
			}
			if lookups != 1 { // the calendars command's own
				t.Errorf("events without colour needs no calendar lookup; requests %v", f.seen())
			}
		})
	}
}

func TestGlobalFlagsAnywhere(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	lines := [][]string{
		{"--json", "lists", "--url", f.URL, "--key", testKey},
		{"--url", f.URL, "--key", testKey, "lists", "--json"},
		{"--url=" + f.URL, "lists", "--key=" + testKey, "--json"},
		{"-json", "--timeout", "5s", "--url", f.URL, "lists", "--key", testKey},
	}
	for _, args := range lines {
		code, out, errOut := runZubi(t, "", args...)
		if code != exitOK || out != prettyFixture(t, "lists.json") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	notes := [][]string{
		{"note", noteID, "--json", "--url", f.URL, "--key", testKey},
		{"note", "--json", noteID, "--url", f.URL, "--key", testKey},
		{"--key", testKey, "--json", "note", "--url", f.URL, noteID},
	}
	for _, args := range notes {
		code, out, errOut := runZubi(t, "", args...)
		if code != exitOK || out != prettyFixture(t, "note.json") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	// A command's own flag before the command name.
	code, _, errOut := runZubi(t, "", f.args("--list", "Groceries", "reminders")...)
	if code != exitOK {
		t.Errorf("--list before reminders: exit %d, stderr %q", code, errOut)
	}
	// Subcommands, with flags before, between and after the words.
	writes := [][]string{
		{"reminders", "add", "Buy eggs", "--list", "Groceries", "--json"},
		{"--json", "reminders", "add", "--list", "Groceries", "Buy eggs"},
		{"reminders", "--json", "add", "Buy eggs", "--list", "Groceries"},
		{"--list", "Groceries", "reminders", "add", "--json", "Buy eggs"},
		{"--list=Groceries", "reminders", "add", "Buy eggs", "-json"},
	}
	for _, args := range writes {
		code, out, errOut := runZubi(t, "", f.args(args...)...)
		if code != exitOK || out != prettyFixture(t, "reminder_added.json") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	// --html is a switch for `note` and takes a value for `note edit`.
	for _, args := range [][]string{
		{"--html", "note", noteID},
		{"note", "--html", noteID},
		{"note", noteID, "--html"},
	} {
		code, out, errOut := runZubi(t, "", f.args(args...)...)
		if code != exitOK || !strings.HasPrefix(out, "<div><h1>Sourdough") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{
		{"note", "edit", noteID, "--html", "<div>x</div>"},
		{"note", "edit", "--html", "<div>x</div>", noteID},
		{"--html", "<div>x</div>", "note", "edit", noteID},
	} {
		code, _, errOut := runZubi(t, "", f.args(args...)...)
		last := f.sent()[len(f.sent())-1]
		if code != exitOK || last.Method != "PATCH" || last.Body != `{"html":"<div>x</div>"}` {
			t.Errorf("%v: exit %d, sent %+v, stderr %q", args, code, last, errOut)
		}
	}
}

func TestExitCodes(t *testing.T) {
	t.Run("401 key refused", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		for _, extra := range [][]string{nil, {"--json"}} {
			args := append([]string{"lists", "--url", f.URL, "--key", "wrong"}, extra...)
			code, out, errOut := runZubi(t, "", args...)
			if code != exitUnauthorized {
				t.Errorf("%v: exit %d, want %d", args, code, exitUnauthorized)
			}
			if out != "" || !strings.Contains(errOut, "refused (401)") || !strings.Contains(errOut, "zubi config set key") {
				t.Errorf("%v: stdout %q, stderr %q", args, out, errOut)
			}
		}
	})

	statuses := []struct {
		name, path, body string
		status, want     int
		args             []string
		message          string
	}{
		{"404 hidden list", "/api/reminders", string(must(t, "error_not_found.json")), 404, exitNotFound, []string{"reminders", "--list", "Nope"}, "Unknown or hidden list (404)"},
		{"403 no access", "/api/lists", `{"error":{"message":"Reminders access not granted on the Mac"}}`, 403, exitError, []string{"lists"}, "Grant it in Zubi's Settings"},
		{"503 Notes unavailable", "/api/notes/folders", string(must(t, "error_unavailable.json")), 503, exitError, []string{"folders"}, "Notes could not be read (503)"},
		{"400 bad range", "/api/events", `{"error":{"message":"Range is limited to one year"}}`, 400, exitError, []string{"events", "--from", "2026-01-01", "--to", "2028-01-01"}, "Range is limited to one year (400)"},
	}
	for _, c := range statuses {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			f.answer(c.path, c.status, c.body)
			for _, extra := range [][]string{nil, {"--json"}} {
				code, out, errOut := runZubi(t, "", f.args(append(c.args, extra...)...)...)
				if code != c.want || out != "" || !strings.Contains(errOut, c.message) {
					t.Errorf("%v: exit %d (want %d), stdout %q, stderr %q", extra, code, c.want, out, errOut)
				}
			}
		})
	}

	t.Run("404 unknown note", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		code, _, errOut := runZubi(t, "", f.args("note", "x-coredata://nope")...)
		if code != exitNotFound || !strings.Contains(errOut, "Unknown or hidden note") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		isolate(t)
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closed := "http://" + l.Addr().String()
		l.Close()
		for _, args := range [][]string{{"lists"}, {"lists", "--json"}, {"health"}} {
			code, out, errOut := runZubi(t, "", append(args, "--url", closed, "--key", testKey)...)
			if code != exitUnreachable || out != "" || !strings.Contains(errOut, "no Zubi answered at "+closed) {
				t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
			}
		}
	})

	t.Run("missing key", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		code, _, errOut := runZubi(t, "", "lists", "--url", f.URL)
		if code != exitUsage || !strings.Contains(errOut, "zubi config set key -") || !strings.Contains(errOut, "ZUBI_API_KEY") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
		if len(f.seen()) != 0 {
			t.Errorf("no request should be made without a key: %v", f.seen())
		}
	})

	usage := [][]string{
		{"frobnicate"},
		{"--json"},
		{"lists", "--bogus"},
		{"lists", "extra"},
		{"reminders", "--due", "tomorrow"},
		{"note"},
		{"note", "a", "b"},
		{"notes", "--limit", "-3"},
		{"notes", "--limit", "many"},
		{"events", "--timeout", "soon"},
		{"reminders", "frob"},
		{"lists", "add", "x"},
		{"key", "extra"},
		{"watch", "now"},
		{"watch", "--exec", ""},
		{"watch", "--exec", "'unclosed"},
		{"feed"},
		{"feed", "Work", "Home"},
		{"feed", "Work", "--from", "tomorrow"},
		{"feed", "Work", "--download", "--json"},
		{"mcp", "serve"},
		{"config", "--save"},
		{"config", "set", "url", "http://x:1", "--pick", "2"},
	}
	for _, args := range usage {
		t.Run("usage "+strings.Join(args, " "), func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			code, out, errOut := runZubi(t, "", f.args(args...)...)
			if code != exitUsage || out != "" || errOut == "" {
				t.Errorf("exit %d (want %d), stdout %q, stderr %q", code, exitUsage, out, errOut)
			}
			if len(f.seen()) != 0 {
				t.Errorf("bad usage should send nothing, sent %v", f.seen())
			}
		})
	}
}

func must(t *testing.T, fixture string) []byte { return readFixture(t, fixture) }

func TestHelpAndVersion(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}, {"lists", "--help"}, {"reminders", "add", "--help"}, {"note", "edit", "-h"}, {"watch", "-h"}, {"mcp", "--help"}} {
		code, out, _ := runZubi(t, "", args...)
		if code != exitOK || out != usage {
			t.Errorf("%v: exit %d, want the usage text", args, code)
		}
	}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, out, _ := runZubi(t, "", args...)
		if code != exitOK || out != "zubi dev\n" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
	}
}

// usageBlocks maps each command the usage text documents to its lines: the line naming it
// ("reminders add TITLE   --list ...") and the indented lines that continue it.
func usageBlocks(t *testing.T) map[string]string {
	t.Helper()
	from, to := strings.Index(usage, "\nRead:"), strings.Index(usage, "\nValues:")
	if from < 0 || to < 0 {
		t.Fatal("the usage text has no Read: or Values: section")
	}
	blocks := map[string]string{}
	name := ""
	for _, line := range strings.Split(usage[from:to], "\n") {
		switch {
		case strings.HasPrefix(line, "   "): // a continuation
			if name != "" {
				blocks[name] += line + "\n"
			}
		case strings.HasPrefix(line, "  "):
			// The command is the lowercase words before its placeholders (TITLE, ID) and flags.
			var words []string
			for _, w := range strings.Fields(line) {
				if w != strings.ToLower(w) || strings.HasPrefix(w, "-") || strings.HasPrefix(w, "[") {
					break
				}
				words = append(words, w)
				if strings.Contains(line, w+"  ") {
					break // the description follows two spaces
				}
			}
			name = strings.Join(words, " ")
			if _, dup := blocks[name]; dup {
				t.Errorf("the usage text lists %q twice", name)
			}
			blocks[name] = line + "\n"
		default:
			name = ""
		}
	}
	return blocks
}

// TestUsageListsEveryCommand: a command or subcommand in the table can't go undocumented, the
// usage text documents no command that doesn't exist, and each command's lines show its flags.
func TestUsageListsEveryCommand(t *testing.T) {
	blocks := usageBlocks(t)
	known := commandNames()
	for _, name := range known {
		if _, ok := blocks[name]; !ok {
			t.Errorf("command %q is missing from the usage text", name)
		}
	}
	for name := range blocks {
		if !slices.Contains(known, name) {
			t.Errorf("the usage text lists %q, which isn't a command", name)
		}
	}
	// A command's flags: its own, except config's, which belong to config discover.
	flagsOf := map[string]string{"config": "", "config discover": "config"}
	for _, name := range known {
		block := blocks[name]
		defined, ok := flagsOf[name]
		if !ok {
			defined = name
		}
		var o options
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		defineFlags(fs, defined, &o)
		fs.VisitAll(func(f *flag.Flag) {
			shown := "--" + f.Name
			if len(f.Name) == 1 {
				shown = "-" + f.Name // a short alias, -q
			}
			if !strings.Contains(block, shown) {
				t.Errorf("command %q has flag %s, which its lines in the usage text don't show:\n%s", name, shown, block)
			}
		})
	}
	queryFlag := map[string]string{"q": "search"}
	for _, c := range apiCommands {
		block := blocks[c.Name]
		for _, q := range c.Query {
			f := q
			if alias, ok := queryFlag[q]; ok {
				f = alias
			}
			if q != "id" && !strings.Contains(block, "--"+f) { // a note's id is positional
				t.Errorf("command %q sends %q, but its usage shows no --%s flag", c.Name, q, f)
			}
		}
		if _, takesArg := argument[c.Name]; takesArg && !regexp.MustCompile(`^  `+c.Name+` [A-Z]+ `).MatchString(block) {
			t.Errorf("command %q takes an argument, which its usage line doesn't name: %q", c.Name, block)
		}
	}
}

// TestClientRefusesUndeclaredRequests: Client sends nothing the command's entry doesn't describe.
func TestClientRefusesUndeclaredRequests(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	lists, _ := lookup("lists")
	edit, _ := lookup("reminders edit")
	add, _ := lookup("reminders add")
	cases := map[string]struct {
		cmd apiCommand
		req request
	}{
		"undeclared query":     {lists, request{Query: url.Values{"due": {"today"}}}},
		"body where none goes": {lists, request{Body: ReminderUpdate{}}},
		"no body":              {add, request{}},
		"wrong body type":      {add, request{Body: ReminderUpdate{}}},
		"missing path value":   {edit, request{Body: ReminderUpdate{}}},
		"unknown path value":   {edit, request{Path: map[string]string{"id": "R1", "list": "L1"}, Body: ReminderUpdate{}}},
	}
	for name, c := range cases {
		_, err := NewClient(endpoint{URL: f.URL}, testKey, 0).Raw(context.Background(), c.cmd, c.req)
		if err == nil || !strings.Contains(err.Error(), "internal error") {
			t.Errorf("%s: want an internal error, got %v", name, err)
		}
	}
	if len(f.seen()) != 0 {
		t.Errorf("nothing should be sent, sent %v", f.seen())
	}
}
