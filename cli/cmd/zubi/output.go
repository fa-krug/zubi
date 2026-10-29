package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Style colours output only on a terminal, and never when NO_COLOR is set (no-color.org).
// CLICOLOR_FORCE colours output that isn't a terminal, such as a pipe into `less -R`.
type Style struct{ enabled bool }

func newStyle(w io.Writer, forceOff bool) Style {
	if forceOff || os.Getenv("NO_COLOR") != "" {
		return Style{}
	}
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return Style{enabled: true}
	}
	if os.Getenv("TERM") == "dumb" {
		return Style{}
	}
	f, ok := w.(*os.File)
	if !ok {
		return Style{}
	}
	info, err := f.Stat()
	return Style{enabled: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func (s Style) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Dim(text string) string  { return s.wrap("2", text) }
func (s Style) Bold(text string) string { return s.wrap("1", text) }
func (s Style) Red(text string) string  { return s.wrap("31", text) }

// Dot is a bullet in the item's own #RRGGBB colour, the colour Reminders and Calendar give it.
func (s Style) Dot(hex string) string {
	r, g, b, ok := parseHex(hex)
	if !s.enabled || !ok {
		return "●"
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm●\x1b[0m", r, g, b)
}

func parseHex(hex string) (r, g, b int64, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseInt(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return v >> 16 & 0xFF, v >> 8 & 0xFF, v & 0xFF, true
}

func printLists(w io.Writer, s Style, lists []ReminderList) {
	if len(lists) == 0 {
		fmt.Fprintln(w, s.Dim("No reminder lists exposed. Tick some under Settings › Reminders in Zubi."))
		return
	}
	for _, l := range lists {
		fmt.Fprintf(w, "%s %s  %s%s\n", s.Dot(deref(l.Color)), l.Title, s.Dim(l.Account), readOnly(s, l.Writable))
	}
}

// printReminders lists reminders; with ids, each is followed by its ID, for `reminders edit`.
func printReminders(w io.Writer, s Style, reminders []Reminder, ids bool) {
	if len(reminders) == 0 {
		fmt.Fprintln(w, s.Dim("No reminders."))
		return
	}
	now := time.Now()
	for _, r := range reminders {
		box := "[ ]"
		if r.IsCompleted {
			box = "[x]"
		}
		line := box + " " + r.Title
		if r.Priority > 0 {
			line += " " + s.Red(strings.Repeat("!", priorityMarks(r.Priority)))
		}
		if r.Due != nil {
			due, overdue := formatDue(*r.Due, r.DueAllDay, now)
			if overdue {
				due = s.Red(due)
			}
			line += "  " + due
		}
		fmt.Fprintf(w, "%s  %s\n", line, s.Dim(r.ListTitle))
		if ids {
			fmt.Fprintf(w, "    %s\n", s.Dim(r.ID))
		}
	}
}

// EventKit priorities: 1–4 high, 5 medium, 6–9 low; Reminders shows them as !!!, !!, !.
func priorityMarks(p int) int {
	switch {
	case p >= 1 && p <= 4:
		return 3
	case p == 5:
		return 2
	default:
		return 1
	}
}

func formatDue(value string, allDay bool, now time.Time) (string, bool) {
	if allDay {
		day, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return "due " + value, false
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		return "due " + day.Format("Mon 2 Jan"), day.Before(today)
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "due " + value, false
	}
	return "due " + at.Local().Format("Mon 2 Jan 15:04"), at.Before(now)
}

func printCalendars(w io.Writer, s Style, calendars []Calendar) {
	if len(calendars) == 0 {
		fmt.Fprintln(w, s.Dim("No calendars exposed. Tick some under Settings › Calendars in Zubi."))
		return
	}
	for _, c := range calendars {
		fmt.Fprintf(w, "%s %s  %s%s\n", s.Dot(deref(c.Color)), c.Title, s.Dim(c.Account), readOnly(s, c.Writable))
	}
}

// printEvents groups by local day; colours come from the calendars list when the caller has it.
// With ids, each event is followed by what `events edit` needs to name it: its ID and, since every
// occurrence of a recurring event shares that ID, the occurrence's start.
func printEvents(w io.Writer, s Style, events []Event, colors map[string]string, ids bool) {
	if len(events) == 0 {
		fmt.Fprintln(w, s.Dim("No events."))
		return
	}
	lastDay := ""
	for _, e := range events {
		start, startOK := eventTime(e.Start, e.IsAllDay)
		day := e.Start
		if startOK {
			day = start.Format("Monday 2 January")
		}
		if day != lastDay {
			if lastDay != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, s.Bold(day))
			lastDay = day
		}
		when := "all day    "
		if !e.IsAllDay {
			end, endOK := eventTime(e.End, false)
			if startOK && endOK {
				when = start.Format("15:04") + "–" + end.Format("15:04")
			}
		}
		line := fmt.Sprintf("  %s %s  %s", s.Dot(colors[e.CalendarID]), s.Dim(when), e.Title)
		if e.Location != nil && *e.Location != "" {
			line += "  " + s.Dim("@ "+firstLine(*e.Location))
		}
		fmt.Fprintf(w, "%s  %s\n", line, s.Dim(e.CalendarTitle))
		if ids {
			fmt.Fprintf(w, "    %s\n", s.Dim(e.ID+" --occurrence "+e.Start))
		}
	}
}

func eventTime(value string, allDay bool) (time.Time, bool) {
	if allDay {
		t, err := time.ParseInLocation("2006-01-02", value, time.Local)
		return t, err == nil
	}
	t, err := time.Parse(time.RFC3339, value)
	return t.Local(), err == nil
}

// printFolders draws the tree from parentID, the way Notes nests folders in its sidebar.
func printFolders(w io.Writer, s Style, folders []NoteFolder) {
	if len(folders) == 0 {
		fmt.Fprintln(w, s.Dim("No Notes folders exposed. Tick some under Settings › Notes in Zubi."))
		return
	}
	present := map[string]bool{}
	children := map[string][]NoteFolder{}
	for _, f := range folders {
		present[f.ID] = true
	}
	var roots []NoteFolder
	for _, f := range folders {
		// A parent that isn't exposed is not listed, so its exposed child is shown at the top.
		if f.ParentID != nil && present[*f.ParentID] {
			children[*f.ParentID] = append(children[*f.ParentID], f)
		} else {
			roots = append(roots, f)
		}
	}
	var walk func(f NoteFolder, prefix string, last bool, depth int)
	walk = func(f NoteFolder, prefix string, last bool, depth int) {
		branch, next := "", ""
		if depth > 0 {
			branch, next = "├── ", "│   "
			if last {
				branch, next = "└── ", "    "
			}
		}
		account := ""
		if depth == 0 {
			account = "  " + s.Dim(f.Account)
		}
		fmt.Fprintf(w, "%s%s%s%s\n", s.Dim(prefix+branch), f.Name, account, readOnly(s, f.Writable))
		kids := children[f.ID]
		for i, child := range kids {
			walk(child, prefix+next, i == len(kids)-1, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, "", true, 0)
	}
}

func printNotes(w io.Writer, s Style, notes []NoteSummary) {
	if len(notes) == 0 {
		fmt.Fprintln(w, s.Dim("No notes."))
		return
	}
	for _, n := range notes {
		modified := n.Modified
		if t, err := time.Parse(time.RFC3339, n.Modified); err == nil {
			modified = t.Local().Format("2006-01-02 15:04")
		}
		lock := ""
		if n.Locked {
			lock = " " + s.Dim("[locked]")
		}
		fmt.Fprintf(w, "%s  %s%s\n    %s\n", s.Dim(modified), n.Title, lock, s.Dim(n.ID))
	}
}

func printNote(w io.Writer, s Style, n Note) {
	fmt.Fprintln(w, s.Bold(n.Title))
	if t, err := time.Parse(time.RFC3339, n.Modified); err == nil {
		fmt.Fprintln(w, s.Dim("Modified "+t.Local().Format("2006-01-02 15:04")))
	}
	fmt.Fprintln(w)
	switch {
	case n.Locked:
		fmt.Fprintln(w, s.Dim("This note is locked; Zubi serves its metadata only."))
	case n.Plaintext != nil:
		fmt.Fprintln(w, strings.TrimRight(*n.Plaintext, "\n"))
	}
}

// readOnly marks a list, calendar or folder Zubi shares for reading only.
func readOnly(s Style, writable bool) string {
	if writable {
		return ""
	}
	return "  " + s.Dim("read-only")
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// colorIndex maps calendar IDs to colours, for tinting events.
func colorIndex(calendars []Calendar) map[string]string {
	index := make(map[string]string, len(calendars))
	for _, c := range calendars {
		index[c.ID] = deref(c.Color)
	}
	return index
}

// deref reads an optional field, which the server omits rather than sending empty.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
