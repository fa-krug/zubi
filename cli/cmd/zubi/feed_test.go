package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFeedURL(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	code, out, errOut := runZubi(t, "", f.args("feed", "Work")...)
	want := f.URL + "/api/calendars/C1/events.ics?key=" + testKey + "\n"
	if code != exitOK || out != want {
		t.Fatalf("exit %d, stdout %q (want %q), stderr %q", code, out, want, errOut)
	}
	if !strings.Contains(errOut, `the key "Laptop", which can also write`) || !strings.Contains(errOut, "Settings › Keys") {
		t.Errorf("want a warning about the read & write key: %q", errOut)
	}

	// A calendar app fetches the URL with no header: the key in the query is enough.
	resp, err := http.Get(strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != string(must(t, "feed.ics")) {
		t.Errorf("fetching the URL: %d %q", resp.StatusCode, body)
	}

	// By title, ignoring case, with a fixed range; the ID goes in the URL.
	code, out, _ = runZubi(t, "", f.args("feed", "work", "--from", "2026-10-01", "--to", "2026-12-31")...)
	if want := f.URL + "/api/calendars/C1/events.ics?from=2026-10-01&key=" + testKey + "&to=2026-12-31\n"; code != exitOK || out != want {
		t.Errorf("exit %d, stdout %q, want %q", code, out, want)
	}
	// By ID.
	if code, out, _ = runZubi(t, "", f.args("feed", "C1")...); code != exitOK || !strings.Contains(out, "/api/calendars/C1/events.ics") {
		t.Errorf("by ID: exit %d, stdout %q", code, out)
	}
}

func TestFeedJSON(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	code, out, errOut := runZubi(t, "", f.args("feed", "Work", "--from", "2026-10-01", "--json")...)
	if code != exitOK || strings.Contains(out, "\\u0026") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	var got struct {
		URL      string   `json:"url"`
		Calendar Calendar `json:"calendar"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.URL != f.URL+"/api/calendars/C1/events.ics?from=2026-10-01&key="+testKey || got.Calendar.ID != "C1" || got.Calendar.Title != "Work" {
		t.Errorf("got %+v", got)
	}
}

func TestFeedReadOnlyKey(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.answer("/api/key", 200, `{"name":"Calendar app","access":"read"}`)
	code, _, errOut := runZubi(t, "", f.args("feed", "Work")...)
	if code != exitOK || !strings.Contains(errOut, `the read-only key "Calendar app"`) {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	// An older Zubi without /api/key still gets the URL, with the general warning.
	f.answer("/api/key", 404, ``)
	code, out, errOut := runZubi(t, "", f.args("feed", "Work")...)
	if code != exitOK || out == "" || !strings.Contains(errOut, "its own read-only key") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestFeedDownload(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	code, out, errOut := runZubi(t, "", f.args("feed", "Work", "--download")...)
	if code != exitOK || out != string(must(t, "feed.ics")) || errOut != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	sent := f.sent()
	if len(sent) != 1 || sent[0].Query.Has(feedKeyParam) || sent[0].Path["id"] != "Work" {
		t.Errorf("--download sends the key as a header, never in the URL: %+v", sent)
	}
}

func TestFeedErrors(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	code, out, errOut := runZubi(t, "", f.args("feed", "Holidays")...)
	if code != exitNotFound || out != "" || !strings.Contains(errOut, `No shared calendar is called "Holidays"`) {
		t.Errorf("unknown calendar: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, _, errOut = runZubi(t, "", f.args("feed", "Holidays", "--download")...)
	if code != exitNotFound || !strings.Contains(errOut, "Unknown or hidden calendar (404)") {
		t.Errorf("--download of an unknown calendar: exit %d, stderr %q", code, errOut)
	}
	f.answer("/api/calendars", 200, `{"calendars":[{"id":"C1","title":"Work","account":"iCloud","writable":true},{"id":"C9","title":"Work","account":"Exchange","writable":false}]}`)
	code, out, errOut = runZubi(t, "", f.args("feed", "Work")...)
	if code != exitUsage || out != "" || !strings.Contains(errOut, "C1 (iCloud), C9 (Exchange)") {
		t.Errorf("two calendars called Work: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, _, _ = runZubi(t, "", "feed", "Work", "--url", f.URL, "--key", "wrong")
	if code != exitUnauthorized {
		t.Errorf("wrong key: exit %d", code)
	}
}
