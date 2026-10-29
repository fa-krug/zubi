package main

// Tests for the commands that change things: the requests they build (checked against the spec
// by the fake), their output, their usage errors and their exit codes.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWriteCommandsHuman(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"reminders", "add", "Buy eggs", "--list", "Groceries"}, "[ ] Buy eggs !!  due Wed 4 Nov  Groceries\n    R9\n"},
		{[]string{"reminders", "edit", "R1", "--done"}, "[x] Buy milk !!!  due Mon 2 Nov  Groceries\n    R1\n"},
		{[]string{"reminders", "done", "R1"}, "[x] Buy milk !!!  due Mon 2 Nov  Groceries\n    R1\n"},
		{[]string{"events", "add", "Dentist", "--calendar", "Work", "--start", "2026-11-03T09:00", "--end", "2026-11-03T09:30"}, "" +
			"Tuesday 3 November\n" +
			"  ● 09:00–09:30  Dentist  @ Main St 1  Work\n" +
			"    E9 --occurrence 2026-11-03T09:00:00Z\n"},
		{[]string{"events", "edit", "E2", "--occurrence", "2026-11-02T09:00:00Z", "--title", "Standup (moved)"}, "" +
			"Monday 2 November\n" +
			"  ● 09:30–09:45  Standup (moved)  Work\n" +
			"    E2 --occurrence 2026-11-02T09:30:00Z\n"},
		{[]string{"notes", "add", "Pancakes", "--folder", "Recipes", "--text", "2 eggs"}, "" +
			"2026-10-29 08:00  Pancakes\n    x-coredata://00000000-0000-0000-0000-000000000000/ICNote/p9\n"},
		{[]string{"note", "edit", noteID, "--text", "375 g water"}, "2026-10-29 09:00  Sourdough\n    " + noteID + "\n"},
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
			if got := f.seen(); len(got) != 1 {
				t.Errorf("want one request, made %v", got)
			}
		})
	}
}

func TestWriteCommandsJSON(t *testing.T) {
	cases := map[string][]string{
		"reminder_added.json":  {"reminders", "add", "Buy eggs", "--list", "Groceries", "--due", "2026-11-04", "--notes", "a dozen", "--priority", "medium"},
		"reminder_edited.json": {"reminders", "done", "R1"},
		"event_added.json":     {"events", "add", "Dentist", "--calendar", "Work", "--start", "2026-11-03T09:00:00Z", "--end", "2026-11-03T09:30:00Z", "--location", "Main St 1"},
		"event_edited.json":    {"events", "edit", "E2", "--occurrence", "2026-11-02T09:00:00Z", "--start", "2026-11-02T09:30:00Z", "--end", "2026-11-02T09:45:00Z"},
		"note_added.json":      {"notes", "add", "Pancakes", "--folder", "Recipes", "--text", "2 eggs\n250 ml milk"},
		"note_edited.json":     {"note", "edit", noteID, "--text", "500 g flour\n375 g water"},
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

// TestRequestBodies drives every flag of every write command, absent, given and cleared, and
// checks what reaches the server. The fake validates each body against the spec as well.
func TestRequestBodies(t *testing.T) {
	cases := []struct {
		args     []string
		stdin    string
		method   string
		template string
		path     map[string]string
		query    string
		body     string
	}{
		// reminders add
		{args: []string{"reminders", "add", "Buy eggs", "--list", "Groceries"},
			method: "POST", template: "/api/reminders", body: `{"title":"Buy eggs","list":"Groceries"}`},
		{args: []string{"reminders", "add", "Buy eggs", "--list", "L1", "--due", "2026-11-04", "--notes", "a dozen", "--priority", "medium"},
			method: "POST", template: "/api/reminders", body: `{"title":"Buy eggs","list":"L1","due":"2026-11-04","notes":"a dozen","priority":5}`},
		{args: []string{"reminders", "add", "Call", "--list", "Errands", "--due", "2026-11-04T09:00", "--priority", "0", "--notes", ""},
			method: "POST", template: "/api/reminders", body: `{"title":"Call","list":"Errands","due":"2026-11-04T09:00:00Z","priority":0,"notes":""}`},
		{args: []string{"reminders", "add", "Call", "--list", "Errands", "--due", "2026-11-04T09:00:00+01:00", "--priority", "HIGH"},
			method: "POST", template: "/api/reminders", body: `{"title":"Call","list":"Errands","due":"2026-11-04T09:00:00+01:00","priority":1}`},
		// reminders edit and done
		{args: []string{"reminders", "edit", "R1", "--title", "Buy oat milk"},
			method: "PATCH", template: "/api/reminders/{id}", path: map[string]string{"id": "R1"}, body: `{"title":"Buy oat milk"}`},
		{args: []string{"reminders", "edit", "R1", "--clear-due", "--clear-notes"},
			method: "PATCH", template: "/api/reminders/{id}", path: map[string]string{"id": "R1"}, body: `{"due":null,"notes":null}`},
		{args: []string{"reminders", "edit", "R1", "--due", "2026-11-05", "--notes", "x", "--list", "Errands", "--priority", "low", "--undone"},
			method: "PATCH", template: "/api/reminders/{id}", path: map[string]string{"id": "R1"}, body: `{"due":"2026-11-05","notes":"x","list":"Errands","priority":9,"isCompleted":false}`},
		{args: []string{"reminders", "edit", "R1", "--done"},
			method: "PATCH", template: "/api/reminders/{id}", path: map[string]string{"id": "R1"}, body: `{"isCompleted":true}`},
		{args: []string{"reminders", "done", "a/b c"},
			method: "PATCH", template: "/api/reminders/{id}", path: map[string]string{"id": "a/b c"}, body: `{"isCompleted":true}`},
		// events add
		{args: []string{"events", "add", "Dentist", "--calendar", "Work", "--start", "2026-11-03T09:00", "--end", "2026-11-03T09:30", "--location", "Main St 1"},
			method: "POST", template: "/api/events", body: `{"title":"Dentist","calendar":"Work","start":"2026-11-03T09:00:00Z","end":"2026-11-03T09:30:00Z","location":"Main St 1"}`},
		{args: []string{"events", "add", "Offsite", "--calendar", "C1", "--start", "2026-11-09", "--all-day"},
			method: "POST", template: "/api/events", body: `{"title":"Offsite","calendar":"C1","start":"2026-11-09","end":"2026-11-09","isAllDay":true}`},
		{args: []string{"events", "add", "Trip", "--calendar", "Work", "--start", "2026-11-09", "--end", "2026-11-11", "--all-day", "--notes", "pack", "--link", "https://example.com/trip"},
			method: "POST", template: "/api/events", body: `{"title":"Trip","calendar":"Work","start":"2026-11-09","end":"2026-11-11","isAllDay":true,"notes":"pack","url":"https://example.com/trip"}`},
		// events edit
		{args: []string{"events", "edit", "E2", "--occurrence", "2026-11-02T09:00:00Z", "--title", "Standup (moved)", "--start", "2026-11-02T09:30:00Z", "--end", "2026-11-02T09:45:00Z"},
			method: "PATCH", template: "/api/events/{id}", path: map[string]string{"id": "E2"}, query: "occurrence=2026-11-02T09%3A00%3A00Z",
			body: `{"title":"Standup (moved)","start":"2026-11-02T09:30:00Z","end":"2026-11-02T09:45:00Z"}`},
		{args: []string{"events", "edit", "E2", "--clear-location", "--clear-notes", "--clear-link"},
			method: "PATCH", template: "/api/events/{id}", path: map[string]string{"id": "E2"}, body: `{"location":null,"notes":null,"url":null}`},
		{args: []string{"events", "edit", "E2", "--location", "Room 4", "--notes", "n", "--link", "https://example.com", "--calendar", "Personal"},
			method: "PATCH", template: "/api/events/{id}", path: map[string]string{"id": "E2"}, body: `{"location":"Room 4","notes":"n","url":"https://example.com","calendar":"Personal"}`},
		{args: []string{"events", "edit", "E1", "--all-day", "--start", "2026-11-02", "--end", "2026-11-03"},
			method: "PATCH", template: "/api/events/{id}", path: map[string]string{"id": "E1"}, body: `{"isAllDay":true,"start":"2026-11-02","end":"2026-11-03"}`},
		{args: []string{"events", "edit", "E1", "--timed", "--start", "2026-11-02 10:00", "--end", "2026-11-02T11:00"},
			method: "PATCH", template: "/api/events/{id}", path: map[string]string{"id": "E1"}, body: `{"isAllDay":false,"start":"2026-11-02T10:00:00Z","end":"2026-11-02T11:00:00Z"}`},
		// notes add
		{args: []string{"notes", "add", "Pancakes", "--folder", "Recipes"},
			method: "POST", template: "/api/notes", body: `{"folder":"Recipes","title":"Pancakes"}`},
		{args: []string{"notes", "add", "Pancakes", "--folder", "Recipes", "--text", "2 eggs\n250 ml milk"},
			method: "POST", template: "/api/notes", body: `{"folder":"Recipes","title":"Pancakes","text":"2 eggs\n250 ml milk"}`},
		{args: []string{"notes", "add", "Pancakes", "--folder", "F2", "--html", "<div>2 eggs</div>"},
			method: "POST", template: "/api/notes", body: `{"folder":"F2","title":"Pancakes","html":"<div>2 eggs</div>"}`},
		{args: []string{"notes", "add", "Pancakes", "--folder", "Recipes", "--text", "-"}, stdin: "from\nstdin\n",
			method: "POST", template: "/api/notes", body: `{"folder":"Recipes","title":"Pancakes","text":"from\nstdin\n"}`},
		// note edit
		{args: []string{"note", "edit", noteID, "--title", "Sourdough", "--html", "-"}, stdin: "<div>375 g water</div>",
			method: "PATCH", template: "/api/note", query: "id=" + strings.NewReplacer(":", "%3A", "/", "%2F").Replace(noteID),
			body: `{"title":"Sourdough","html":"<div>375 g water</div>"}`},
		{args: []string{"note", "edit", noteID, "--text", ""},
			method: "PATCH", template: "/api/note", query: "id=" + strings.NewReplacer(":", "%3A", "/", "%2F").Replace(noteID), body: `{"text":""}`},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			code, _, errOut := runZubi(t, c.stdin, f.args(append(c.args, "--json")...)...)
			if code != exitOK {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			got := f.sent()
			if len(got) != 1 {
				t.Fatalf("want one request, got %+v", got)
			}
			r := got[0]
			if r.Method != c.method || r.Template != c.template {
				t.Errorf("sent %s %s, want %s %s", r.Method, r.Template, c.method, c.template)
			}
			if len(c.path) > 0 && !reflect.DeepEqual(r.Path, c.path) {
				t.Errorf("path values %v, want %v", r.Path, c.path)
			}
			if q := r.Query.Encode(); q != c.query {
				t.Errorf("query %q, want %q", q, c.query)
			}
			var gotBody, wantBody any
			if err := json.Unmarshal([]byte(r.Body), &gotBody); err != nil {
				t.Fatalf("body %q: %v", r.Body, err)
			}
			if err := json.Unmarshal([]byte(c.body), &wantBody); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotBody, wantBody) {
				t.Errorf("body %s, want %s", r.Body, c.body)
			}
		})
	}
}

func TestWriteUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"reminders", "add", "--list", "Groceries"}, "give the title"},
		{[]string{"reminders", "add", "", "--list", "Groceries"}, "give the title"},
		{[]string{"reminders", "add", "Buy", "milk", "--list", "Groceries"}, "quoted if it has spaces"},
		{[]string{"reminders", "add", "Milk"}, "--list is required"},
		{[]string{"reminders", "add", "Milk", "--list", "G", "--priority", "urgent"}, "--priority"},
		{[]string{"reminders", "add", "Milk", "--list", "G", "--priority", "10"}, "--priority"},
		{[]string{"reminders", "add", "Milk", "--list", "G", "--due", "tomorrow"}, `--due "tomorrow"`},
		{[]string{"reminders", "edit", "R1"}, "nothing to change"},
		{[]string{"reminders", "edit", "--done"}, "reminder ID"},
		{[]string{"reminders", "edit", "R1", "--due", "2026-11-02", "--clear-due"}, "--due or --clear-due"},
		{[]string{"reminders", "edit", "R1", "--notes", "x", "--clear-notes"}, "--notes or --clear-notes"},
		{[]string{"reminders", "edit", "R1", "--done", "--undone"}, "--done or --undone"},
		{[]string{"reminders", "edit", "R1", "--title", ""}, "--title is empty"},
		{[]string{"reminders", "edit", "R1", "--clear-title"}, "flag provided but not defined"},
		{[]string{"reminders", "done"}, "reminder ID"},
		{[]string{"reminders", "done", "R1", "R2"}, "reminder ID"},
		{[]string{"reminders", "done", "R1", "--title", "x"}, "flag provided but not defined"},
		{[]string{"reminders", "frob"}, "add, edit, done"},
		{[]string{"events", "add", "X", "--calendar", "Work", "--start", "2026-11-03T09:00"}, "--end is required"},
		{[]string{"events", "add", "X", "--start", "2026-11-03T09:00", "--end", "2026-11-03T10:00"}, "--calendar is required"},
		{[]string{"events", "add", "X", "--calendar", "Work"}, "--start is required"},
		{[]string{"events", "add", "X", "--calendar", "Work", "--start", "2026-11-03", "--end", "2026-11-03"}, "add --all-day"},
		{[]string{"events", "add", "X", "--calendar", "Work", "--all-day", "--start", "2026-11-03T09:00"}, "an --all-day event takes days"},
		{[]string{"events", "add", "X", "--calendar", "Work", "--start", "2026-11-03T10:00", "--end", "2026-11-03T09:00"}, "--end is before --start"},
		{[]string{"events", "add", "X", "--calendar", "Work", "--url", "https://x", "--start", "2026-11-03T10:00"}, "--end is required"}, // --url is Zubi's address
		{[]string{"events", "edit", "E1"}, "nothing to change"},
		{[]string{"events", "edit", "E1", "--occurrence", "2026-11-02"}, "nothing to change"},
		{[]string{"events", "edit", "E1", "--occurrence", "", "--title", "x"}, "--occurrence is empty"},
		{[]string{"events", "edit", "E1", "--all-day", "--timed"}, "--all-day or --timed"},
		{[]string{"events", "edit", "E1", "--timed", "--start", "2026-11-02"}, "add --all-day"},
		{[]string{"events", "edit", "E1", "--location", "x", "--clear-location"}, "--location or --clear-location"},
		{[]string{"events", "edit", "E1", "--link", "x", "--clear-link"}, "--link or --clear-link"},
		{[]string{"events", "edit", "E1", "--start", "soon"}, `--start "soon"`},
		{[]string{"notes", "add", "Pancakes"}, "--folder is required"},
		{[]string{"notes", "add", "Pancakes", "--folder", "R", "--text", "a", "--html", "b"}, "--text or --html, not both"},
		{[]string{"note", "edit", noteID}, "nothing to change"},
		{[]string{"note", "edit", noteID, "--text", "a", "--html", "b"}, "--text or --html, not both"},
		{[]string{"note", "edit", "--title", "x"}, "note ID"},
		{[]string{"note", "edit", noteID, "--title", " "}, "--title is empty"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			code, out, errOut := runZubi(t, "", f.args(c.args...)...)
			if code != exitUsage || out != "" || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d (want %d), stdout %q, stderr %q (want it to contain %q)", code, exitUsage, out, errOut, c.want)
			}
			if len(f.seen()) != 0 {
				t.Errorf("bad usage should send nothing, sent %v", f.seen())
			}
		})
	}
}

func TestWriteExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		override string
		status   int
		body     string
		args     []string
		want     int
		messages []string
	}{
		{"400 bad body", "POST /api/events", 400, `{"error":{"message":"Bad date in start"}}`,
			[]string{"events", "add", "X", "--calendar", "Work", "--start", "2026-11-03T09:00:00Z", "--end", "2026-11-03T10:00:00+01:00"}, exitError,
			[]string{"Bad date in start (400)"}},
		{"403 read-only", "POST /api/reminders", 403, string(must(t, "error_read_only.json")),
			[]string{"reminders", "add", "Milk", "--list", "Errands"}, exitError,
			[]string{"The list Errands is read-only (403)", "Read & Write", "Zubi's Settings"}},
		{"403 read-only edit", "", 0, "",
			[]string{"reminders", "done", "R2"}, exitError,
			[]string{"read-only (403)", "Read & Write"}},
		{"404 unknown reminder", "/api/reminders/nope", 404, `{"error":{"message":"Unknown or hidden reminder"}}`,
			[]string{"reminders", "done", "nope"}, exitNotFound,
			[]string{"Unknown or hidden reminder (404)"}},
		{"404 unknown note", "", 0, "",
			[]string{"note", "edit", "x-coredata://nope", "--title", "x"}, exitNotFound,
			[]string{"Unknown or hidden note (404)"}},
		{"409 locked note", "", 0, "",
			[]string{"note", "edit", lockedNoteID, "--text", "x"}, exitError,
			[]string{"The note is locked (409)", "never changes a locked note"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			if c.override != "" {
				f.answer(c.override, c.status, c.body)
			}
			if c.name == "403 read-only edit" {
				f.answer("PATCH /api/reminders/R2", 403, string(must(t, "error_read_only.json")))
			}
			for _, extra := range [][]string{nil, {"--json"}} {
				code, out, errOut := runZubi(t, "", f.args(append(c.args, extra...)...)...)
				if code != c.want || out != "" {
					t.Errorf("%v: exit %d (want %d), stdout %q, stderr %q", extra, code, c.want, out, errOut)
				}
				for _, m := range c.messages {
					if !strings.Contains(errOut, m) {
						t.Errorf("%v: stderr %q lacks %q", extra, errOut, m)
					}
				}
			}
		})
	}

	t.Run("401 key refused", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		code, out, errOut := runZubi(t, "", "reminders", "add", "Milk", "--list", "Groceries", "--url", f.URL, "--key", "wrong")
		if code != exitUnauthorized || out != "" || !strings.Contains(errOut, "refused (401)") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("a create answered 200 still succeeded", func(t *testing.T) {
		isolate(t)
		f := newFakeZubi(t)
		f.answer("POST /api/reminders", 200, string(must(t, "reminder_added.json")))
		code, out, errOut := runZubi(t, "", f.args("reminders", "add", "Buy eggs", "--list", "Groceries")...)
		if code != exitOK || !strings.Contains(out, "R9") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
}

// TestNullableEncoding: absent leaves the field out, Null sends null, Some sends the value.
func TestNullableEncoding(t *testing.T) {
	for want, body := range map[string]ReminderUpdate{
		`{}`:                         {},
		`{"due":null}`:               {Due: Null[string]()},
		`{"notes":""}`:               {Notes: Some("")},
		`{"priority":0}`:             {Priority: ptr(0)},
		`{"isCompleted":false}`:      {IsCompleted: ptr(false)},
		`{"notes":"n","due":"2026"}`: {Notes: Some("n"), Due: Some("2026")},
	} {
		got, err := json.Marshal(body)
		if err != nil || string(got) != want {
			t.Errorf("got %s (%v), want %s", got, err, want)
		}
	}
}
