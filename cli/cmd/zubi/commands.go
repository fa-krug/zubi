package main

import (
	"slices"
	"strings"
)

// apiCommand is one request the CLI can make. The table below is the CLI's whole use of the
// API: run() sends nothing it doesn't describe, Client refuses query parameters, path values and
// bodies it doesn't list, and the tests check every entry against openapi.json.
type apiCommand struct {
	// Name is what the user types: "reminders", or a command and its subcommand, "reminders add".
	Name   string
	Method string
	// Path is the spec's path, {placeholders} included.
	Path string
	// Query is every query parameter the command may send.
	Query []string
	// Body is a zero value of the type the request body encodes from; nil when there is none.
	Body any
	// Status is the success status the spec documents; 0 means 200.
	Status int
	// Public endpoints need no API key.
	Public bool
	// Response is a zero value of the type a success body decodes into; nil when the body isn't JSON.
	Response any
	// Media is the success body's media type, sent as Accept; "" means application/json.
	Media string
	// Internal requests are the CLI's own (`zubi`'s status), never a command someone types.
	Internal bool
}

func (c apiCommand) media() string {
	if c.Media == "" {
		return "application/json"
	}
	return c.Media
}

func (c apiCommand) status() int {
	if c.Status == 0 {
		return 200
	}
	return c.Status
}

// writes reports whether the command changes something on the Mac.
func (c apiCommand) writes() bool { return c.Method != "GET" }

// apiCommands is in the order the usage text lists them.
var apiCommands = []apiCommand{
	{Name: "lists", Method: "GET", Path: "/api/lists", Response: ListsResponse{}},
	{Name: "reminders", Method: "GET", Path: "/api/reminders", Query: []string{"list", "due", "completed", "q"}, Response: RemindersResponse{}},
	{Name: "calendars", Method: "GET", Path: "/api/calendars", Response: CalendarsResponse{}},
	{Name: "events", Method: "GET", Path: "/api/events", Query: []string{"from", "to", "calendar", "q"}, Response: EventsResponse{}},
	{Name: "folders", Method: "GET", Path: "/api/notes/folders", Response: NoteFoldersResponse{}},
	{Name: "notes", Method: "GET", Path: "/api/notes", Query: []string{"folder", "limit", "q"}, Response: NoteSummariesResponse{}},
	{Name: "note", Method: "GET", Path: "/api/note", Query: []string{"id"}, Response: Note{}},

	{Name: "reminders add", Method: "POST", Path: "/api/reminders", Body: ReminderCreate{}, Status: 201, Response: Reminder{}},
	{Name: "reminders edit", Method: "PATCH", Path: "/api/reminders/{id}", Body: ReminderUpdate{}, Response: Reminder{}},
	{Name: "reminders done", Method: "PATCH", Path: "/api/reminders/{id}", Body: ReminderUpdate{}, Response: Reminder{}},
	{Name: "events add", Method: "POST", Path: "/api/events", Body: EventCreate{}, Status: 201, Response: Event{}},
	{Name: "events edit", Method: "PATCH", Path: "/api/events/{id}", Query: []string{"occurrence"}, Body: EventUpdate{}, Response: Event{}},
	{Name: "notes add", Method: "POST", Path: "/api/notes", Body: NoteCreate{}, Status: 201, Response: Note{}},
	{Name: "note edit", Method: "PATCH", Path: "/api/note", Query: []string{"id"}, Body: NoteUpdate{}, Response: Note{}},

	{Name: "watch", Method: "GET", Path: "/api/changes", Media: "text/event-stream"},
	{Name: "key", Method: "GET", Path: "/api/key", Response: KeyInfo{}, Internal: true},
	{Name: "health", Method: "GET", Path: "/health", Public: true, Media: "text/plain", Internal: true},
}

// localCommands are the documented commands that send no request of their own through
// apiCommands. ("help" prints the usage text.) mcp's tools send requests from apiCommands.
var localCommands = []string{"pair", "unpair", "mcp", "relay", "version"}

// hiddenCommands work but aren't documented: `serve` is what the client service runs.
var hiddenCommands = []string{"serve"}

func lookup(name string) (apiCommand, bool) {
	for _, c := range apiCommands {
		if c.Name == name {
			return c, true
		}
	}
	return apiCommand{}, false
}

// hasSubcommands reports whether some command is named "top <something>".
func hasSubcommands(top string) bool {
	for _, c := range apiCommands {
		if strings.HasPrefix(c.Name, top+" ") {
			return true
		}
	}
	return false
}

// topLevel reports whether word is the first word of some command.
func topLevel(word string) bool {
	if word == "help" || slices.Contains(hiddenCommands, word) {
		return true
	}
	for _, name := range commandNames() {
		if first, _, _ := strings.Cut(name, " "); first == word {
			return true
		}
	}
	return false
}

// commandNames lists every documented command, the API's and the local ones.
func commandNames() []string {
	var names []string
	for _, c := range apiCommands {
		if !c.Internal {
			names = append(names, c.Name)
		}
	}
	return append(names, localCommands...)
}
