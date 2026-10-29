package main

// `zubi mcp`: a Model Context Protocol server over stdio, so an AI assistant (Claude Desktop,
// Claude Code, any MCP client) can use Zubi. Messages are JSON-RPC 2.0, one per line. Each tool
// is an entry in apiCommands with arguments named as in openapi.json, sent through the same
// Client as the commands; the tests hold both to the spec.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const mcpProtocolVersion = "2025-06-18"

// mcpProtocolVersions are the versions the server can speak, newest first. Tools work the same in
// all of them; older clients ignore the fields newer versions added.
var mcpProtocolVersions = []string{mcpProtocolVersion, "2025-03-26", "2024-11-05"}

const mcpInstructions = `Zubi shares the Apple Reminders, Calendar and Notes of the user's Mac.

- The user decides in Zubi's Settings on the Mac what you can see. Each reminder list, calendar and Notes folder is Off (invisible: for you it doesn't exist), Read (list and read) or Read & Write (also create and edit). The list tools give each one's "writable".
- Creating or editing needs both a writable list, calendar or folder and a key with readWrite access (get_key). Otherwise Zubi answers 403; retrying won't help, so tell the user what to change instead.
- Nothing can ever be deleted through Zubi. To get rid of a reminder, complete it; events and notes can only be removed by the user on the Mac.
- IDs come from the list tools. Lists, calendars and folders can also be named by their title.
- Dates you send: ISO 8601 date-times with a time zone (2026-11-02T09:00:00Z, 2026-11-02T10:00:00+01:00), or YYYY-MM-DD for a whole day in the Mac's time zone. Dates Zubi returns are in UTC, or YYYY-MM-DD for all-day items.`

// mcpProp is one argument of a tool: a path or query parameter of its request, or a property of
// its request body, under the same name.
type mcpProp struct {
	Name     string
	Type     string // the JSON Schema type: string, integer or boolean
	Nullable bool   // null is allowed, and clears the field
	Enum     []string
	Min, Max *int
	Required bool
	Desc     string
}

type mcpTool struct {
	Name    string
	Title   string
	Command string // the entry in apiCommands that makes its request
	Desc    string
	Props   []mcpProp
}

func intp(v int) *int { return &v }

// Descriptions of arguments several tools share.
const (
	descWhen = "ISO 8601 date-time with a time zone (2026-11-02T09:00:00Z, 2026-11-02T10:00:00+01:00), " +
		"or YYYY-MM-DD for a whole day in the Mac's time zone."
	descPriority = "0 none, 1–4 high (1 is the usual high), 5 medium, 6–9 low (9 is the usual low)."
	descSearch   = "Only items whose title or notes contain this text, ignoring case."
)

var mcpTools = []mcpTool{
	{
		Name: "list_reminder_lists", Title: "List reminder lists", Command: "lists",
		Desc: "List the reminder lists the user shares through Zubi. Lists set to Off don't appear. " +
			"Each has id, title, account, color and writable: whether reminders can be created or edited in it " +
			"(it is set to Read & Write). Pass a list's id or title as `list` to list_reminders and create_reminder.",
	},
	{
		Name: "list_reminders", Title: "List reminders", Command: "reminders",
		Desc: "List reminders, only incomplete ones unless completed is \"true\". Each has id (for update_reminder), " +
			"title, notes, listID, listTitle, isCompleted, priority (0 none, 1–4 high, 5 medium, 6–9 low), and due: " +
			"an ISO 8601 date-time in UTC, or YYYY-MM-DD when dueAllDay is true. Fields without a value are left out.",
		Props: []mcpProp{
			{Name: "list", Type: "string", Desc: "Only this list, by id or title (from list_reminder_lists)."},
			{Name: "due", Type: "string", Enum: []string{"today", "overdue"}, Desc: "today: incomplete and due today. overdue: incomplete and due before today."},
			{Name: "completed", Type: "string", Enum: []string{"true", "false"}, Desc: "\"true\" includes completed reminders. Ignored with due."},
			{Name: "q", Type: "string", Desc: descSearch},
		},
	},
	{
		Name: "list_calendars", Title: "List calendars", Command: "calendars",
		Desc: "List the calendars the user shares through Zubi. Calendars set to Off don't appear. Each has id, " +
			"title, account, color and writable: whether events can be created or edited in it. A subscribed or " +
			"holiday calendar is never writable. Pass a calendar's id or title as `calendar` to list_events and create_event.",
	},
	{
		Name: "list_events", Title: "List events", Command: "events",
		Desc: "List events in a range of days, from today to six days on unless from and to say otherwise (at most a year). " +
			"A recurring event appears once per occurrence, every occurrence with the same id; to change one, pass its id " +
			"and its start, as `occurrence`, to update_event. start and end are ISO 8601 date-times in UTC, or YYYY-MM-DD " +
			"when isAllDay is true (then end is inclusive). Also: title, location, notes, url, calendarID, calendarTitle.",
		Props: []mcpProp{
			{Name: "from", Type: "string", Desc: "First day, YYYY-MM-DD in the Mac's time zone. Defaults to today."},
			{Name: "to", Type: "string", Desc: "Last day, inclusive, YYYY-MM-DD. Defaults to six days after today; at most a year after from."},
			{Name: "calendar", Type: "string", Desc: "Only this calendar, by id or title (from list_calendars)."},
			{Name: "q", Type: "string", Desc: descSearch},
		},
	},
	{
		Name: "list_note_folders", Title: "List Notes folders", Command: "folders",
		Desc: "List the Notes folders the user shares through Zubi. Folders set to Off don't appear. Each has id, " +
			"name, account, parentID (the folder it is nested in, when that one is shared too) and writable: whether " +
			"notes can be created or edited in it. Pass a folder's id or name as `folder` to list_notes and create_note.",
	},
	{
		Name: "list_notes", Title: "List notes", Command: "notes",
		Desc: "List notes, newest first: id, title, modified (ISO 8601, UTC) and locked. Text is not included; " +
			"get it with get_note. A locked note's text can't be read or changed.",
		Props: []mcpProp{
			{Name: "folder", Type: "string", Desc: "Only this folder, by id or name (from list_note_folders)."},
			{Name: "limit", Type: "integer", Desc: "At most this many notes, 1 to 500; 50 when left out. Values outside that range are clamped."},
			{Name: "q", Type: "string", Desc: "Only notes whose title or text contains this, ignoring case. Searching text reads every note, so it can take a few seconds in a large folder."},
		},
	},
	{
		Name: "get_note", Title: "Read a note", Command: "note",
		Desc: "Read one note: id, title, folderID, created, modified, locked, and its text as plaintext and as html. " +
			"Both hold the whole note, title first; both are absent when the note is locked.",
		Props: []mcpProp{
			{Name: "id", Type: "string", Required: true, Desc: "The note's id, from list_notes (an x-coredata:// URL)."},
		},
	},
	{
		Name: "create_reminder", Title: "Add a reminder", Command: "reminders add",
		Desc: "Add a reminder to a writable list. Returns the new reminder, with its id.",
		Props: []mcpProp{
			{Name: "title", Type: "string", Required: true, Desc: "Not empty."},
			{Name: "list", Type: "string", Required: true, Desc: "The list, by id or title. It must be writable (list_reminder_lists)."},
			{Name: "notes", Type: "string", Desc: "Notes shown under the title."},
			{Name: "due", Type: "string", Desc: "When it is due: " + descWhen + " A day without a time makes an all-day reminder."},
			{Name: "priority", Type: "integer", Min: intp(0), Max: intp(9), Desc: descPriority + " Defaults to 0."},
		},
	},
	{
		Name: "update_reminder", Title: "Change a reminder", Command: "reminders edit",
		Desc: "Change a reminder in a writable list. Only the fields you pass change; null clears notes or due. " +
			"Set isCompleted to true to complete it, which is also the way to get rid of one: nothing can be deleted. " +
			"Returns the reminder as it now is.",
		Props: []mcpProp{
			{Name: "id", Type: "string", Required: true, Desc: "The reminder's id, from list_reminders."},
			{Name: "title", Type: "string", Desc: "New title, not empty."},
			{Name: "list", Type: "string", Desc: "Move it to this list, by id or title. That list must be writable too."},
			{Name: "notes", Type: "string", Nullable: true, Desc: "New notes, or null to clear them."},
			{Name: "due", Type: "string", Nullable: true, Desc: "New due date, " + descWhen + " Or null to clear it."},
			{Name: "priority", Type: "integer", Min: intp(0), Max: intp(9), Desc: descPriority},
			{Name: "isCompleted", Type: "boolean", Desc: "true completes it, false reopens it."},
		},
	},
	{
		Name: "create_event", Title: "Add an event", Command: "events add",
		Desc: "Add an event to a writable calendar. Returns the new event, with its id.",
		Props: []mcpProp{
			{Name: "title", Type: "string", Required: true, Desc: "Not empty."},
			{Name: "calendar", Type: "string", Required: true, Desc: "The calendar, by id or title. It must be writable (list_calendars)."},
			{Name: "start", Type: "string", Required: true, Desc: descWhen + " Use days (YYYY-MM-DD) for an all-day event, date-times otherwise."},
			{Name: "end", Type: "string", Required: true, Desc: "As start, and not before it. For an all-day event the day is inclusive: a one-day event ends on the day it starts."},
			{Name: "isAllDay", Type: "boolean", Desc: "true for an all-day event; then start and end are days. Defaults to false."},
			{Name: "location", Type: "string", Desc: "Where it takes place."},
			{Name: "notes", Type: "string", Desc: "Notes for the event."},
			{Name: "url", Type: "string", Desc: "A link for the event."},
		},
	},
	{
		Name: "update_event", Title: "Change an event", Command: "events edit",
		Desc: "Change an event in a writable calendar. Only the fields you pass change; null clears location, notes or url. " +
			"For a recurring event, pass the occurrence's start as `occurrence`: only that occurrence changes. " +
			"Events can't be deleted. Returns the event as it now is.",
		Props: []mcpProp{
			{Name: "id", Type: "string", Required: true, Desc: "The event's id, from list_events."},
			{Name: "occurrence", Type: "string", Desc: "The occurrence's start, exactly as list_events returned it. Required for a recurring event; for one that doesn't repeat it may be left out."},
			{Name: "title", Type: "string", Desc: "New title, not empty."},
			{Name: "calendar", Type: "string", Desc: "Move it to this calendar, by id or title. That calendar must be writable too."},
			{Name: "start", Type: "string", Desc: "New start, " + descWhen},
			{Name: "end", Type: "string", Desc: "New end, as start and not before it. For an all-day event the day is inclusive."},
			{Name: "isAllDay", Type: "boolean", Desc: "Turn it into an all-day event or back. Then pass start and end too, as days for all-day and date-times otherwise."},
			{Name: "location", Type: "string", Nullable: true, Desc: "New location, or null to clear it."},
			{Name: "notes", Type: "string", Nullable: true, Desc: "New notes, or null to clear them."},
			{Name: "url", Type: "string", Nullable: true, Desc: "New link, or null to clear it."},
		},
	},
	{
		Name: "create_note", Title: "Add a note", Command: "notes add",
		Desc: "Add a note to a writable Notes folder. Give its body as text or as html, not both. Returns the new note, with its id.",
		Props: []mcpProp{
			{Name: "folder", Type: "string", Required: true, Desc: "The folder, by id or name. It must be writable (list_note_folders)."},
			{Name: "title", Type: "string", Required: true, Desc: "Not empty. Notes shows it as the first line."},
			{Name: "text", Type: "string", Desc: "The body below the title, as plain text; line breaks are kept. Don't repeat the title."},
			{Name: "html", Type: "string", Desc: "The body below the title, as HTML."},
		},
	},
	{
		Name: "update_note", Title: "Change a note", Command: "note edit",
		Desc: "Change a note in a writable folder. text or html replaces the whole body below the title (not both); " +
			"the title stays unless you pass title. To add to a note, read it with get_note first and send the full " +
			"new body: get_note's plaintext without its first line (the title). A locked note can't be changed, " +
			"and notes can't be deleted. Returns the note as it now is.",
		Props: []mcpProp{
			{Name: "id", Type: "string", Required: true, Desc: "The note's id, from list_notes."},
			{Name: "title", Type: "string", Desc: "New title, not empty."},
			{Name: "text", Type: "string", Desc: "The new body below the title, as plain text."},
			{Name: "html", Type: "string", Desc: "The new body below the title, as HTML."},
		},
	},
	{
		Name: "get_key", Title: "Check the API key", Command: "key",
		Desc: "Tell which Zubi API key this server uses: its name and access. access \"read\" can only read; " +
			"\"readWrite\" may also create and edit where a list, calendar or folder is writable. " +
			"If writes answer 403, check this first.",
	},
}

func (t mcpTool) inputSchema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.Props {
		s := map[string]any{"description": p.Desc}
		if p.Nullable {
			s["type"] = []string{p.Type, "null"}
		} else {
			s["type"] = p.Type
		}
		if len(p.Enum) > 0 {
			s["enum"] = p.Enum
		}
		if p.Min != nil {
			s["minimum"] = *p.Min
		}
		if p.Max != nil {
			s["maximum"] = *p.Max
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func (t mcpTool) describe() map[string]any {
	cmd, _ := lookup(t.Command)
	return map[string]any{
		"name":        t.Name,
		"title":       t.Title,
		"description": t.Desc,
		"inputSchema": t.inputSchema(),
		"annotations": map[string]any{
			"title":        t.Title,
			"readOnlyHint": !cmd.writes(),
			// An edit can overwrite what was there (a note's whole body); adding only adds.
			"destructiveHint": cmd.Method == http.MethodPatch,
			"idempotentHint":  cmd.Method != http.MethodPost,
			"openWorldHint":   false,
		},
	}
}

// request turns a tool's arguments into its command's request: each argument becomes the path
// parameter, query parameter or body property of the same name.
func (t mcpTool) request(cmd apiCommand, args map[string]json.RawMessage) (request, error) {
	req := request{Query: url.Values{}}
	byName := map[string]mcpProp{}
	var names []string
	for _, p := range t.Props {
		byName[p.Name] = p
		names = append(names, p.Name)
	}
	for _, name := range keysOf(args) {
		if _, ok := byName[name]; !ok {
			if len(names) == 0 {
				return req, fmt.Errorf("unknown argument %q; %s takes no arguments", name, t.Name)
			}
			return req, fmt.Errorf("unknown argument %q; %s takes %s", name, t.Name, strings.Join(names, ", "))
		}
	}
	body := map[string]json.RawMessage{}
	for _, p := range t.Props {
		raw, given := args[p.Name]
		if !given {
			if p.Required {
				return req, fmt.Errorf("%s is required", p.Name)
			}
			continue
		}
		value, err := p.check(raw)
		if err != nil {
			return req, err
		}
		switch {
		case strings.Contains(cmd.Path, "{"+p.Name+"}"):
			if value == "" {
				return req, fmt.Errorf("%s is empty", p.Name)
			}
			if req.Path == nil {
				req.Path = map[string]string{}
			}
			req.Path[p.Name] = value
		case slices.Contains(cmd.Query, p.Name):
			req.Query.Set(p.Name, value)
		case cmd.Body != nil:
			body[p.Name] = raw
		default:
			return req, fmt.Errorf("internal error: %s's argument %q is no parameter of %s %s", t.Name, p.Name, cmd.Method, cmd.Path)
		}
	}
	if cmd.Body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return req, err
		}
		out := reflect.New(reflect.TypeOf(cmd.Body))
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(out.Interface()); err != nil {
			return req, fmt.Errorf("invalid arguments: %v", err)
		}
		req.Body = out.Elem().Interface()
	}
	return req, nil
}

// check holds an argument to its schema and returns it as a parameter's text.
func (p mcpProp) check(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("%s: %v", p.Name, err)
	}
	kind := map[string]string{"string": "a string", "integer": "an integer", "boolean": "true or false"}[p.Type]
	if p.Nullable {
		kind += " or null"
	}
	wrongType := fmt.Errorf("%s must be %s", p.Name, kind)
	switch v := v.(type) {
	case nil:
		if !p.Nullable {
			return "", wrongType
		}
		return "", nil
	case string:
		if p.Type != "string" {
			return "", wrongType
		}
		if len(p.Enum) > 0 && !slices.Contains(p.Enum, v) {
			return "", fmt.Errorf("%s must be one of %s", p.Name, strings.Join(p.Enum, ", "))
		}
		return v, nil
	case json.Number:
		n, err := strconv.ParseInt(v.String(), 10, 64)
		if p.Type != "integer" || err != nil {
			return "", wrongType
		}
		if p.Min != nil && n < int64(*p.Min) {
			return "", fmt.Errorf("%s must be at least %d", p.Name, *p.Min)
		}
		if p.Max != nil && n > int64(*p.Max) {
			return "", fmt.Errorf("%s must be at most %d", p.Name, *p.Max)
		}
		return v.String(), nil
	case bool:
		if p.Type != "boolean" {
			return "", wrongType
		}
		return strconv.FormatBool(v), nil
	}
	return "", wrongType
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func findTool(name string) (mcpTool, bool) {
	for _, t := range mcpTools {
		if t.Name == name {
			return t, true
		}
	}
	return mcpTool{}, false
}

// JSON-RPC 2.0.

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func textResult(text string, isError bool) toolResult {
	return toolResult{Content: []toolContent{{Type: "text", Text: text}}, IsError: isError}
}

type mcpServer struct {
	client *Client

	writeMu sync.Mutex
	out     io.Writer

	mu       sync.Mutex
	inFlight map[string]context.CancelFunc // by request ID, for notifications/cancelled
	wg       sync.WaitGroup
}

func runMCP(g globals, stdin io.Reader, stdout, stderr io.Writer) int {
	ep, key, _, err := resolve(g.url, g.key)
	if err != nil {
		fmt.Fprintln(stderr, "zubi mcp:", err)
		return exitError
	}
	s := &mcpServer{client: NewClient(ep, key, g.timeout), out: stdout, inFlight: map[string]context.CancelFunc{}}
	// stdout carries the protocol alone; what people read goes to stderr, the client's log.
	fmt.Fprintf(stderr, "zubi mcp %s: serving Zubi at %s over stdio\n", version, ep)
	if key == "" {
		fmt.Fprintln(stderr, "zubi mcp: "+notPaired+". Until then every tool answers with an error.")
	}
	reader := bufio.NewReader(stdin)
	code := exitOK
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.handle(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr, "zubi mcp: reading stdin:", err)
				code = exitError
			}
			break
		}
	}
	s.wg.Wait() // answer what is in flight before exiting
	return code
}

func (s *mcpServer) write(r rpcResponse) {
	r.JSONRPC = "2.0"
	if len(r.ID) == 0 {
		r.ID = json.RawMessage("null")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		buf.Reset()
		enc.Encode(rpcResponse{JSONRPC: "2.0", ID: r.ID, Error: &rpcError{Code: -32603, Message: err.Error()}})
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.out.Write(buf.Bytes()) // Encode ends it with the newline that delimits messages
}

func (s *mcpServer) handle(line []byte) {
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("[")) && json.Valid(line) {
			s.write(rpcResponse{Error: &rpcError{rpcInvalidRequest, "batches are not supported; send one message per line"}})
			return
		}
		s.write(rpcResponse{Error: &rpcError{rpcParseError, "parse error: " + err.Error()}})
		return
	}
	isRequest := len(msg.ID) > 0
	if isRequest && !validID(msg.ID) {
		s.write(rpcResponse{Error: &rpcError{rpcInvalidRequest, "the id must be a string or an integer"}})
		return
	}
	if msg.JSONRPC != "2.0" {
		if isRequest {
			s.write(rpcResponse{ID: msg.ID, Error: &rpcError{rpcInvalidRequest, `jsonrpc must be "2.0"`}})
		}
		return
	}
	if msg.Method == "" {
		return // a response; this server sends no requests, so there is nothing to match it to
	}
	if !isRequest {
		if msg.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(msg.Params, &p) == nil {
				s.mu.Lock()
				if cancel, ok := s.inFlight[string(p.RequestID)]; ok {
					cancel()
				}
				s.mu.Unlock()
			}
		}
		return // notifications/initialized and the rest need nothing
	}

	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(msg.Params, &p)
		v := mcpProtocolVersion
		if slices.Contains(mcpProtocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		s.write(rpcResponse{ID: msg.ID, Result: map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "zubi", "title": "Zubi", "version": version},
			"instructions":    mcpInstructions,
		}})
	case "ping":
		s.write(rpcResponse{ID: msg.ID, Result: struct{}{}})
	case "tools/list":
		var tools []map[string]any
		for _, t := range mcpTools {
			tools = append(tools, t.describe())
		}
		s.write(rpcResponse{ID: msg.ID, Result: map[string]any{"tools": tools}})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			s.write(rpcResponse{ID: msg.ID, Error: &rpcError{rpcInvalidParams, "invalid params: " + err.Error()}})
			return
		}
		tool, ok := findTool(p.Name)
		if !ok {
			s.write(rpcResponse{ID: msg.ID, Error: &rpcError{rpcInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}})
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.inFlight[string(msg.ID)] = cancel
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			result := s.call(ctx, tool, p.Arguments)
			s.mu.Lock()
			delete(s.inFlight, string(msg.ID))
			s.mu.Unlock()
			cancelled := ctx.Err() != nil
			cancel()
			if !cancelled { // a cancelled request gets no answer
				s.write(rpcResponse{ID: msg.ID, Result: result})
			}
		}()
	default:
		s.write(rpcResponse{ID: msg.ID, Error: &rpcError{rpcMethodNotFound, "method not found: " + msg.Method}})
	}
}

func validID(id json.RawMessage) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(id))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return false
	}
	switch v := v.(type) {
	case string:
		return true
	case json.Number:
		_, err := v.Int64()
		return err == nil
	}
	return false
}

// call runs a tool. Whatever goes wrong is a tool result with isError, which the model sees and
// can act on, not a protocol error.
func (s *mcpServer) call(ctx context.Context, tool mcpTool, rawArgs json.RawMessage) toolResult {
	args := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(rawArgs)) > 0 && string(bytes.TrimSpace(rawArgs)) != "null" {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return textResult("arguments must be a JSON object", true)
		}
	}
	cmd, _ := lookup(tool.Command)
	req, err := tool.request(cmd, args)
	if err != nil {
		return textResult(err.Error(), true)
	}
	if s.client.APIKey == "" && !cmd.Public {
		return textResult("This computer isn't paired with the user's Mac yet. The user should run `zubi` in a terminal "+
			"here and paste the code from Zubi's menu bar icon › Copy Pairing Code on the Mac.", true)
	}
	body, err := s.client.Raw(ctx, cmd, req)
	if err != nil {
		return textResult(mcpErrorText(err, s.client.Where), true)
	}
	return textResult(string(bytes.TrimSpace(body)), false)
}

// mcpErrorText explains a failed request to a model: what Zubi said, and what to do about it.
func mcpErrorText(err error, baseURL string) string {
	var apiErr *APIError
	var unreachable *UnreachableError
	switch {
	case errors.As(err, &apiErr):
		msg := apiErr.Message
		if msg == "" {
			msg = http.StatusText(apiErr.Status)
		}
		text := fmt.Sprintf("Zubi answered %d: %s.", apiErr.Status, strings.TrimSuffix(msg, "."))
		switch {
		case apiErr.Status == http.StatusUnauthorized:
			text += " The Mac refused this computer's key. The user should pair it again: `zubi pair` with a new code from Zubi › Settings › Clients."
		case apiErr.Status == http.StatusForbidden && apiErr.Write:
			text += " Changing it needs a key with readWrite access (get_key) and the list, calendar or folder set to Read & Write " +
				"in Zubi's Settings on the Mac (writable in the list tools). Retrying won't help; tell the user."
		case apiErr.Status == http.StatusForbidden:
			text += " Access isn't granted on the Mac; tell the user."
		case apiErr.Status == http.StatusNotFound:
			text += " It doesn't exist, or the user set it to Off in Zubi's Settings. Take IDs and names from the list tools."
		case apiErr.Status == http.StatusConflict:
			text += " Zubi never changes a locked note; the user has to unlock it in Notes first."
		case apiErr.Status == http.StatusServiceUnavailable:
			text += " The Mac couldn't do it right now; trying again shortly may work."
		}
		return text
	case errors.As(err, &unreachable):
		return fmt.Sprintf("Zubi isn't reachable at %s (%v). It may not be running, or the Mac may be asleep or off the network.", baseURL, unreachable.Err)
	case errors.Is(err, context.DeadlineExceeded):
		return "Zubi took too long to answer; Notes in particular can be slow. Try again, or narrow the request."
	}
	return err.Error()
}
