// Command zubi reads and writes the Reminders, calendars and Notes a Mac running Zubi shares
// over its API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

// Set at release time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes are part of the interface: scripts and agents branch on them.
const (
	exitOK           = 0
	exitError        = 1
	exitUsage        = 2
	exitUnauthorized = 3
	exitNotFound     = 4
	exitUnreachable  = 5
)

const usage = `zubi — your Mac's Reminders, calendars and Notes, from anywhere, through Zubi.

Usage:
  zubi                        The connection's status. The first time, it asks for the pairing
                              code from Zubi's menu bar icon › Copy Pairing Code on your Mac,
                              and sets up the zubi-client service that keeps the connection
  zubi <command> [flags]

Read:
  lists                       Reminder lists; read-only ones are marked
  reminders                   Reminders, incomplete unless --completed
                              [--list NAME|ID] [--due today|overdue] [--completed]
                              [-q|--search TEXT] [--ids]
  calendars                   Calendars; read-only ones are marked
  events                      Events by day, from today to six days on unless --from/--to
                              [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--calendar NAME|ID]
                              [-q|--search TEXT] [--ids]
  folders                     Notes folders, as a tree; read-only ones are marked
  notes                       Notes, newest first, with IDs
                              [--folder NAME|ID] [--limit N] [-q|--search TEXT]
  note ID                     One note's text                 [--html]

Change (only where Zubi's Settings on the Mac say Read & Write; nothing is ever deleted):
  reminders add TITLE         --list NAME|ID [--due WHEN] [--notes TEXT] [--priority P]
  reminders edit ID           [--title TEXT] [--list NAME|ID] [--due WHEN | --clear-due]
                              [--notes TEXT | --clear-notes] [--priority P] [--done | --undone]
  reminders done ID           Mark a reminder completed (edit ID --done)
  events add TITLE            --calendar NAME|ID --start WHEN [--end WHEN] [--all-day]
                              [--location TEXT] [--notes TEXT] [--link URL]
  events edit ID              [--occurrence START] [--title TEXT] [--start WHEN] [--end WHEN]
                              [--all-day | --timed] [--calendar NAME|ID]
                              [--location TEXT | --clear-location] [--notes TEXT | --clear-notes]
                              [--link URL | --clear-link]
  notes add TITLE             --folder NAME|ID [--text TEXT | --html HTML]
  note edit ID                [--title TEXT] [--text TEXT | --html HTML]

Connect:
  pair CODE                   Pair with a Mac, or again with a new code (another key, your own
                              relay); "-" reads the code from stdin
  unpair                      Forget the pairing, and remove the zubi-client service
  watch                       Print a line whenever something changes, until Ctrl-C
                              [--exec COMMAND]  run COMMAND after each change (no shell)
  mcp                         Serve Zubi to AI assistants over stdio (Model Context Protocol)

Your own relay (Zubi uses relay.fa-krug.de unless you set your own on the Mac):
  relay                       Run a relay: wss:// on port 443 with a Let's Encrypt certificate
                              for this network's public IP address, renewed automatically
                              [--listen ADDR] [--ip IP | --domain NAME] [--state DIR]
                              [--acme-directory URL]  another ACME CA, such as a staging one
                              [--behind-tls-proxy]  plain HTTP on $PORT, for a platform or
                                                    tunnel that serves wss:// itself
                              [--no-tls]  plain ws://; only on a network you trust
                              [--install-service]  keep it running, with these flags, as a
                                                   systemd service (Linux; asks for sudo)
                              [--uninstall-service]

Other:
  version                     Print the version

Values:
  WHEN    YYYY-MM-DD for a day, YYYY-MM-DDTHH:MM in local time, or ISO 8601 with a zone
  P       0–9, or none, high, medium, low
  ID      from "reminders --ids", "events --ids" (with its --occurrence), "notes",
          or "id" in --json output
  TEXT    for --text and --html, "-" reads it from stdin
  -q      same as --search: items whose title or notes contain TEXT, ignoring case
  An edit changes only what you pass; --clear-... empties a field.

Flags for every command:
  --json            Print Zubi's JSON instead of text (use this from scripts)
  --timeout DUR     Give up after this long      (default 20s; Notes can be slow)
  --no-color        Plain text, no colour        (also NO_COLOR)

Exit codes: 0 ok, 1 error (including read-only, 403, and locked note, 409), 2 bad usage or
not paired, 3 key refused, 4 not found or hidden, 5 the Mac unreachable.

Examples:
  zubi                           # status; the first time, pairing
  zubi events --from 2026-11-02 --to 2026-11-06
  zubi reminders -q milk
  zubi reminders add "Buy milk" --list Groceries --due 2026-11-02 --priority high
  zubi reminders done "$(zubi reminders --json | jq -r '.reminders[0].id')"
  zubi notes add "Minutes" --folder Work --text - < minutes.txt
  zubi watch --exec "./refresh-dashboard.sh"
`

// globals are the flags every command accepts, wherever they appear on the line.
type globals struct {
	json    bool
	url     string
	key     string
	timeout time.Duration
	noColor bool
}

// options are the commands' own flags.
type options struct {
	// Reading.
	list, due, calendar, folder, from, to, search string
	completed, html, ids                          bool
	limit                                         int
	// Watching.
	exec string
	// Running a relay or a tunnel.
	listen, ip, domain, stateDir, acmeDirectory string
	noTLS, behindProxy                          bool
	installService, uninstallService            bool
	// Writing.
	title, notes, priority, start, end, location, link, occurrence, text, htmlBody string
	allDay, timed, done, undone, clearDue, clearNotes, clearLocation, clearLink    bool
	// set holds the flags the command line gave, so `--notes ""` differs from no --notes.
	set map[string]bool
}

func newFlagSet(name string, g *globals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&g.json, "json", false, "")
	fs.StringVar(&g.url, "url", "", "")
	fs.StringVar(&g.key, "key", "", "")
	fs.DurationVar(&g.timeout, "timeout", 20*time.Second, "")
	fs.BoolVar(&g.noColor, "no-color", false, "")
	return fs
}

// defineFlags adds command's own flags to fs.
func defineFlags(fs *flag.FlagSet, command string, o *options) {
	str := func(p *string, names ...string) {
		for _, name := range names {
			fs.StringVar(p, name, "", "")
		}
	}
	boolean := func(p *bool, name string) { fs.BoolVar(p, name, false, "") }
	switch command {
	case "reminders":
		str(&o.list, "list")
		str(&o.due, "due")
		boolean(&o.completed, "completed")
		str(&o.search, "search", "q")
		boolean(&o.ids, "ids")
	case "reminders add":
		str(&o.list, "list")
		str(&o.due, "due")
		str(&o.notes, "notes")
		str(&o.priority, "priority")
	case "reminders edit":
		str(&o.title, "title")
		str(&o.list, "list")
		str(&o.due, "due")
		boolean(&o.clearDue, "clear-due")
		str(&o.notes, "notes")
		boolean(&o.clearNotes, "clear-notes")
		str(&o.priority, "priority")
		boolean(&o.done, "done")
		boolean(&o.undone, "undone")
	case "events":
		str(&o.from, "from")
		str(&o.to, "to")
		str(&o.calendar, "calendar")
		str(&o.search, "search", "q")
		boolean(&o.ids, "ids")
	case "events add":
		str(&o.calendar, "calendar")
		str(&o.start, "start")
		str(&o.end, "end")
		boolean(&o.allDay, "all-day")
		str(&o.location, "location")
		str(&o.notes, "notes")
		str(&o.link, "link")
	case "events edit":
		str(&o.occurrence, "occurrence")
		str(&o.title, "title")
		str(&o.start, "start")
		str(&o.end, "end")
		boolean(&o.allDay, "all-day")
		boolean(&o.timed, "timed")
		str(&o.calendar, "calendar")
		str(&o.location, "location")
		boolean(&o.clearLocation, "clear-location")
		str(&o.notes, "notes")
		boolean(&o.clearNotes, "clear-notes")
		str(&o.link, "link")
		boolean(&o.clearLink, "clear-link")
	case "notes":
		str(&o.folder, "folder")
		fs.IntVar(&o.limit, "limit", 0, "")
		str(&o.search, "search", "q")
	case "notes add":
		str(&o.folder, "folder")
		str(&o.text, "text")
		str(&o.htmlBody, "html")
	case "note":
		boolean(&o.html, "html")
	case "note edit":
		str(&o.title, "title")
		str(&o.text, "text")
		str(&o.htmlBody, "html")
	case "watch":
		str(&o.exec, "exec")
	case "serve":
		boolean(&o.installService, "install-service")
		boolean(&o.uninstallService, "uninstall-service")
	case "relay":
		str(&o.listen, "listen")
		boolean(&o.installService, "install-service")
		boolean(&o.uninstallService, "uninstall-service")
		str(&o.ip, "ip")
		str(&o.domain, "domain")
		str(&o.stateDir, "state")
		str(&o.acmeDirectory, "acme-directory")
		boolean(&o.noTLS, "no-tls")
		boolean(&o.behindProxy, "behind-tls-proxy")
	}
}

// flagKind says whether a flag name is a switch, takes a value, or (in different commands) both.
type flagKind struct{ boolean, value bool }

// flagKinds collects the flags of every command in family ("" for all), plus the global ones.
func flagKinds(family string) map[string]flagKind {
	kinds := map[string]flagKind{}
	for _, name := range commandNames() {
		if family != "" && name != family && !strings.HasPrefix(name, family+" ") {
			continue
		}
		var g globals
		var o options
		fs := newFlagSet(name, &g)
		defineFlags(fs, name, &o)
		fs.VisitAll(func(f *flag.Flag) {
			k := kinds[f.Name]
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				k.boolean = true
			} else {
				k.value = true
			}
			kinds[f.Name] = k
		})
	}
	return kinds
}

// firstWord finds the first argument that isn't a flag or a flag's value. A flag that is a switch
// in one command and takes a value in another (--html) is read as a switch when a word follows.
func firstWord(args []string, kinds map[string]flagKind, isWord func(string) bool) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) < 2 || arg[0] != '-' {
			return i
		}
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		k := kinds[name]
		if k.value && !(k.boolean && i+1 < len(args) && isWord(args[i+1])) {
			i++ // the flag's value
		}
	}
	return -1
}

// splitCommand finds the command among flags (`zubi --json lists`), skipping each flag's value,
// and its subcommand when it has one (`zubi reminders --json add Milk`).
func splitCommand(args []string) (command string, rest []string) {
	i := firstWord(args, flagKinds(""), topLevel)
	if i < 0 {
		return "", args
	}
	command = args[i]
	drop := map[int]bool{i: true}
	if hasSubcommands(command) {
		isSub := func(w string) bool { _, ok := lookup(command + " " + w); return ok }
		after := args[i+1:]
		if j := firstWord(after, flagKinds(command), isSub); j >= 0 && isSub(after[j]) {
			command += " " + after[j]
			drop[i+1+j] = true
		}
	}
	for k, arg := range args {
		if !drop[k] {
			rest = append(rest, arg)
		}
	}
	return command, rest
}

// parse lets flags follow positional arguments (`zubi note ID --json`), which `flag` alone does not.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runStatus(stdin, stdout, stderr)
	}
	if args[0] == "--version" || args[0] == "-version" {
		fmt.Fprintln(stdout, "zubi "+version)
		return exitOK
	}
	command, rest := splitCommand(args)
	switch command {
	case "":
		for _, arg := range args {
			if arg == "-h" || arg == "-help" || arg == "--help" {
				fmt.Fprint(stdout, usage)
				return exitOK
			}
		}
		fmt.Fprintln(stderr, "zubi: no command given; run `zubi help`")
		return exitUsage
	case "help":
		fmt.Fprint(stdout, usage)
		return exitOK
	case "version":
		fmt.Fprintln(stdout, "zubi "+version)
		return exitOK
	}

	var g globals
	o := options{set: map[string]bool{}}
	fs := newFlagSet(command, &g)
	cmd, isAPI := lookup(command)
	if isAPI && cmd.Internal || !isAPI && !slices.Contains(localCommands, command) && !slices.Contains(hiddenCommands, command) {
		fmt.Fprintf(stderr, "zubi: unknown command %q; run `zubi help`\n", command)
		return exitUsage
	}
	defineFlags(fs, command, &o)
	positional, err := parse(fs, rest)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "zubi %s: %v; run `zubi help`\n", command, err)
		return exitUsage
	}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })
	switch command {
	case "mcp":
		if len(positional) > 0 {
			fmt.Fprintf(stderr, "zubi mcp: unexpected argument %q; it takes no arguments\n", positional[0])
			return exitUsage
		}
		return runMCP(g, stdin, stdout, stderr)
	case "relay", "serve":
		if o.installService || o.uninstallService {
			return runService(command, rest, positional, o, stdout, stderr)
		}
		if command == "relay" {
			return runRelay(positional, o, stdout, stderr)
		}
		return runServe(positional, stdout, stderr)
	case "pair":
		return runPair(positional, g, stdin, stdout, stderr)
	case "unpair":
		return runUnpair(positional, stdout, stderr)
	}

	req, err := buildRequest(command, o, positional, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "zubi %s: %v\n", command, err)
		var usageErr usageError
		if errors.As(err, &usageErr) {
			return exitUsage
		}
		return exitError
	}

	ep, key, paired, err := resolve(g.url, g.key)
	if err != nil {
		fmt.Fprintln(stderr, "zubi:", err)
		return exitError
	}
	if !paired || key == "" {
		fmt.Fprintln(stderr, "zubi: "+notPaired)
		return exitUsage
	}
	client := NewClient(ep, key, g.timeout)
	ctx := context.Background()

	if command == "watch" {
		return runWatch(NewStreamClient(ep, key, g.timeout), cmd, o, g, stdout, stderr)
	}

	if g.json {
		body, err := client.Raw(ctx, cmd, req)
		if err != nil {
			return fail(stderr, err)
		}
		printJSON(stdout, body)
		return exitOK
	}

	res, err := client.Do(ctx, cmd, req)
	if err != nil {
		return fail(stderr, err)
	}
	style := newStyle(stdout, g.noColor)
	// eventColors tints events by calendar. Colour only: a failure here costs the dots, never the events.
	eventColors := func() map[string]string {
		if !style.enabled {
			return nil
		}
		calendars, _ := lookup("calendars")
		cals, err := client.Do(ctx, calendars, request{})
		if err != nil {
			return nil
		}
		return colorIndex(cals.(*CalendarsResponse).Calendars)
	}
	switch res := res.(type) {
	case *ListsResponse:
		printLists(stdout, style, res.Lists)
	case *RemindersResponse:
		printReminders(stdout, style, res.Reminders, o.ids)
	case *CalendarsResponse:
		printCalendars(stdout, style, res.Calendars)
	case *EventsResponse:
		printEvents(stdout, style, res.Events, eventColors(), o.ids)
	case *NoteFoldersResponse:
		printFolders(stdout, style, res.Folders)
	case *NoteSummariesResponse:
		printNotes(stdout, style, res.Notes)
	// A created or edited item is shown as its listing shows it, with its ID to follow up with.
	case *Reminder:
		printReminders(stdout, style, []Reminder{*res}, true)
	case *Event:
		printEvents(stdout, style, []Event{*res}, eventColors(), true)
	case *Note:
		switch {
		case cmd.writes():
			printNotes(stdout, style, []NoteSummary{{ID: res.ID, Title: res.Title, Modified: res.Modified, Locked: res.Locked}})
		case o.html && res.HTML != nil:
			fmt.Fprintln(stdout, *res.HTML)
		default:
			printNote(stdout, style, *res)
		}
	default:
		fmt.Fprintf(stderr, "zubi: internal error: no output for %T\n", res)
		return exitError
	}
	return exitOK
}

const notPaired = "not paired with a Mac yet: run `zubi` and paste the code from Zubi's menu bar icon › Copy Pairing Code"

// printJSON prints a response body as --json promises: the server's JSON, indented.
func printJSON(w io.Writer, body []byte) {
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(bytes.TrimSpace(body))
	}
	fmt.Fprintln(w, pretty.String())
}

// describeAccess puts a key's access in words.
func describeAccess(access string) string {
	switch access {
	case accessRead:
		return "read-only"
	case accessReadWrite:
		return "read & write"
	}
	return access
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "zubi:", err)
	var apiErr *APIError
	var unreachable *UnreachableError
	var usageErr usageError
	var refused tunnel.RefusedError
	switch {
	case errors.As(err, &usageErr):
		return exitUsage
	case errors.As(err, &apiErr) && apiErr.Status == 401, errors.As(err, &refused):
		return exitUnauthorized
	case errors.As(err, &apiErr) && apiErr.Status == 404:
		return exitNotFound
	case errors.As(err, &unreachable):
		return exitUnreachable
	}
	return exitError
}
