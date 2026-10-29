package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// usageError is a mistake on the command line; run exits with exitUsage and sends nothing.
type usageError string

func (e usageError) Error() string { return string(e) }

func usagef(format string, a ...any) error { return usageError(fmt.Sprintf(format, a...)) }

// What the one positional argument of each command that takes one is.
var argument = map[string]string{
	"note":           "give exactly one note ID, as `zubi notes` lists them",
	"note edit":      "give exactly one note ID, as `zubi notes` lists them",
	"reminders edit": "give exactly one reminder ID, as `zubi reminders --ids` lists them",
	"reminders done": "give exactly one reminder ID, as `zubi reminders --ids` lists them",
	"events edit":    "give exactly one event ID, as `zubi events --ids` lists them",
	"reminders add":  "give the title as one argument, quoted if it has spaces",
	"events add":     "give the title as one argument, quoted if it has spaces",
	"notes add":      "give the title as one argument, quoted if it has spaces",
	"feed":           "give exactly one calendar, by name or ID, as `zubi calendars` lists them",
}

// buildRequest turns a command's flags and arguments into what it sends, or explains what is
// wrong. Only `--text -` and `--html -` read stdin.
func buildRequest(command string, o options, positional []string, stdin io.Reader) (request, error) {
	req := request{Query: url.Values{}}
	var arg string
	if problem, ok := argument[command]; ok {
		if len(positional) != 1 {
			return req, usageError(problem)
		}
		arg = positional[0]
		if strings.TrimSpace(arg) == "" {
			return req, usageError(problem)
		}
	} else if len(positional) > 0 {
		if subs := subcommands(command); len(subs) > 0 {
			return req, usagef("unexpected argument %q; `%s` takes flags only, or one of: %s", positional[0], command, strings.Join(subs, ", "))
		}
		return req, usagef("unexpected argument %q; run `zubi help`", positional[0])
	}

	var err error
	switch command {
	case "reminders":
		if o.due != "" && o.due != "today" && o.due != "overdue" {
			return req, usageError("--due must be today or overdue")
		}
		set(req.Query, "list", o.list)
		set(req.Query, "due", o.due)
		if o.completed {
			req.Query.Set("completed", "true")
		}
		set(req.Query, "q", o.search)
	case "events":
		set(req.Query, "from", o.from)
		set(req.Query, "to", o.to)
		set(req.Query, "calendar", o.calendar)
		set(req.Query, "q", o.search)
	case "notes":
		set(req.Query, "folder", o.folder)
		set(req.Query, "q", o.search)
		if o.limit < 0 {
			return req, usageError("--limit must be positive")
		}
		if o.limit > 0 {
			req.Query.Set("limit", strconv.Itoa(o.limit))
		}
	case "note":
		req.Query.Set("id", arg)

	case "watch":
		if o.set["exec"] {
			if _, err := splitWords(o.exec); err != nil {
				return req, err
			}
		}

	case "feed":
		req.Path = map[string]string{"id": arg}
		for name, day := range map[string]string{"from": o.from, "to": o.to} {
			if day != "" && !isDate(day) {
				return req, usagef("--%s %q: use YYYY-MM-DD", name, day)
			}
			set(req.Query, name, day)
		}

	case "reminders add":
		if o.list == "" {
			return req, usageError("--list is required: the list to add to, by name or ID (see `zubi lists`)")
		}
		body := ReminderCreate{Title: arg, List: o.list}
		if o.set["notes"] {
			body.Notes = ptr(o.notes)
		}
		if o.set["due"] {
			if body.Due, err = whenPtr("--due", o.due); err != nil {
				return req, err
			}
		}
		if o.set["priority"] {
			if body.Priority, err = priority(o.priority); err != nil {
				return req, err
			}
		}
		req.Body = body

	case "reminders edit":
		req.Path = map[string]string{"id": arg}
		var body ReminderUpdate
		if body.Title, err = nonEmpty(o, "title", o.title); err != nil {
			return req, err
		}
		if body.List, err = nonEmpty(o, "list", o.list); err != nil {
			return req, err
		}
		if body.Due, err = clearable(o, "due", o.due, o.clearDue, true); err != nil {
			return req, err
		}
		if body.Notes, err = clearable(o, "notes", o.notes, o.clearNotes, false); err != nil {
			return req, err
		}
		if o.set["priority"] {
			if body.Priority, err = priority(o.priority); err != nil {
				return req, err
			}
		}
		switch {
		case o.done && o.undone:
			return req, usageError("give --done or --undone, not both")
		case o.done:
			body.IsCompleted = ptr(true)
		case o.undone:
			body.IsCompleted = ptr(false)
		}
		if body == (ReminderUpdate{}) {
			return req, nothingToChange(command)
		}
		req.Body = body

	case "reminders done":
		req.Path = map[string]string{"id": arg}
		req.Body = ReminderUpdate{IsCompleted: ptr(true)}

	case "events add":
		if o.calendar == "" {
			return req, usageError("--calendar is required: the calendar to add to, by name or ID (see `zubi calendars`)")
		}
		if o.start == "" {
			return req, usageError("--start is required")
		}
		body := EventCreate{Title: arg, Calendar: o.calendar}
		if body.Start, err = when("--start", o.start); err != nil {
			return req, err
		}
		switch {
		case o.end != "":
			if body.End, err = when("--end", o.end); err != nil {
				return req, err
			}
		case o.allDay:
			body.End = body.Start // a one-day event; the end day is inclusive
		default:
			return req, usageError("--end is required for a timed event (an --all-day event without --end lasts one day)")
		}
		if err := checkSpan(o.allDay, !o.allDay, &body.Start, &body.End); err != nil {
			return req, err
		}
		if o.allDay {
			body.IsAllDay = ptr(true)
		}
		if o.set["location"] {
			body.Location = ptr(o.location)
		}
		if o.set["notes"] {
			body.Notes = ptr(o.notes)
		}
		if o.set["link"] {
			body.URL = ptr(o.link)
		}
		req.Body = body

	case "events edit":
		req.Path = map[string]string{"id": arg}
		if o.set["occurrence"] {
			if o.occurrence == "" {
				return req, usageError("--occurrence is empty; give the occurrence's start as `zubi events --ids` shows it")
			}
			req.Query.Set("occurrence", o.occurrence)
		}
		var body EventUpdate
		if body.Title, err = nonEmpty(o, "title", o.title); err != nil {
			return req, err
		}
		if body.Calendar, err = nonEmpty(o, "calendar", o.calendar); err != nil {
			return req, err
		}
		if o.set["start"] {
			if body.Start, err = whenPtr("--start", o.start); err != nil {
				return req, err
			}
		}
		if o.set["end"] {
			if body.End, err = whenPtr("--end", o.end); err != nil {
				return req, err
			}
		}
		switch {
		case o.allDay && o.timed:
			return req, usageError("give --all-day or --timed, not both")
		case o.allDay:
			body.IsAllDay = ptr(true)
		case o.timed:
			body.IsAllDay = ptr(false)
		}
		if err := checkSpan(o.allDay, o.timed, body.Start, body.End); err != nil {
			return req, err
		}
		if body.Location, err = clearable(o, "location", o.location, o.clearLocation, false); err != nil {
			return req, err
		}
		if body.Notes, err = clearable(o, "notes", o.notes, o.clearNotes, false); err != nil {
			return req, err
		}
		if body.URL, err = clearable(o, "link", o.link, o.clearLink, false); err != nil {
			return req, err
		}
		if body == (EventUpdate{}) {
			return req, nothingToChange(command)
		}
		req.Body = body

	case "notes add":
		if o.folder == "" {
			return req, usageError("--folder is required: the folder to add to, by name or ID (see `zubi folders`)")
		}
		body := NoteCreate{Folder: o.folder, Title: arg}
		if body.Text, body.HTML, err = noteBody(o, stdin); err != nil {
			return req, err
		}
		req.Body = body

	case "note edit":
		req.Query.Set("id", arg)
		var body NoteUpdate
		if body.Title, err = nonEmpty(o, "title", o.title); err != nil {
			return req, err
		}
		if body.Text, body.HTML, err = noteBody(o, stdin); err != nil {
			return req, err
		}
		if body == (NoteUpdate{}) {
			return req, nothingToChange(command)
		}
		req.Body = body
	}
	return req, nil
}

func set(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// subcommands lists the second words of the commands named "command <something>".
func subcommands(command string) []string {
	var subs []string
	for _, c := range apiCommands {
		if sub, ok := strings.CutPrefix(c.Name, command+" "); ok {
			subs = append(subs, sub)
		}
	}
	return subs
}

func nothingToChange(command string) error {
	var o options
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	defineFlags(fs, command, &o)
	var names []string
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name != "occurrence" {
			names = append(names, "--"+f.Name)
		}
	})
	return usagef("nothing to change; give at least one of %s", strings.Join(names, ", "))
}

// nonEmpty is an optional flag whose value, when given, may not be empty (a title, a list).
func nonEmpty(o options, name, value string) (*string, error) {
	if !o.set[name] {
		return nil, nil
	}
	if strings.TrimSpace(value) == "" {
		return nil, usagef("--%s is empty", name)
	}
	return ptr(value), nil
}

// clearable is a field an edit can set with --name or empty with --clear-name (sent as null).
func clearable(o options, name, value string, clear, isTime bool) (Nullable[string], error) {
	switch {
	case o.set[name] && clear:
		return Nullable[string]{}, usagef("give --%s or --clear-%s, not both", name, name)
	case clear:
		return Null[string](), nil
	case !o.set[name]:
		return Nullable[string]{}, nil
	case isTime:
		v, err := when("--"+name, value)
		return Some(v), err
	}
	return Some(value), nil
}

// noteBody reads --text or --html, either of which may be "-" for stdin.
func noteBody(o options, stdin io.Reader) (text, html *string, err error) {
	if o.set["text"] && o.set["html"] {
		return nil, nil, usageError("give --text or --html, not both")
	}
	read := func(v string) (*string, error) {
		if v != "-" {
			return ptr(v), nil
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return ptr(string(data)), nil
	}
	switch {
	case o.set["text"]:
		text, err = read(o.text)
	case o.set["html"]:
		html, err = read(o.htmlBody)
	}
	return text, html, err
}

const dateLayout = "2006-01-02"

// when checks a date or time and puts it in a form the API takes: a day (YYYY-MM-DD) and an
// ISO 8601 time with a zone go as they are; a time without a zone is local, so it gets this
// machine's offset.
func when(flagName, v string) (string, error) {
	if isDate(v) {
		return v, nil
	}
	if _, err := time.Parse(time.RFC3339, v); err == nil {
		return v, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t.Format(time.RFC3339), nil
		}
	}
	return "", usagef("%s %q: use YYYY-MM-DD for a day, YYYY-MM-DDTHH:MM for a local time, or ISO 8601 with a zone such as 2026-11-02T09:00:00Z", flagName, v)
}

func whenPtr(flagName, v string) (*string, error) {
	out, err := when(flagName, v)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func isDate(v string) bool {
	_, err := time.Parse(dateLayout, v)
	return err == nil && len(v) == len(dateLayout)
}

// checkSpan keeps an event's start and end consistent with --all-day (days) or --timed (times),
// and the end not before the start. Either may be nil on an edit.
func checkSpan(allDay, timed bool, start, end *string) error {
	for _, f := range []struct {
		name  string
		value *string
	}{{"--start", start}, {"--end", end}} {
		switch {
		case f.value == nil:
		case allDay && !isDate(*f.value):
			return usagef("%s %s is a time; an --all-day event takes days, YYYY-MM-DD", f.name, *f.value)
		case timed && isDate(*f.value):
			return usagef("%s %s is a day without a time; add --all-day for a whole-day event, or give a time such as %sT09:00", f.name, *f.value, *f.value)
		}
	}
	if start != nil && end != nil {
		if s, e := instant(*start), instant(*end); !s.IsZero() && !e.IsZero() && e.Before(s) {
			return usageError("--end is before --start")
		}
	}
	return nil
}

func instant(v string) time.Time {
	if t, err := time.ParseInLocation(dateLayout, v, time.Local); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, v)
	return t
}

// priority reads EventKit's 0–9, or the names Reminders shows: high (1), medium (5), low (9).
func priority(v string) (*int, error) {
	names := map[string]int{"none": 0, "high": 1, "medium": 5, "low": 9}
	if p, ok := names[strings.ToLower(v)]; ok {
		return &p, nil
	}
	p, err := strconv.Atoi(v)
	if err != nil || p < 0 || p > 9 {
		return nil, usagef("--priority %q: use 0–9, or none, high, medium or low", v)
	}
	return &p, nil
}
