package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestMCPToolsMatchSpec: each tool's arguments are its operation's parameters and request body
// properties, with the same names, types, enums, bounds and required-ness.
func TestMCPToolsMatchSpec(t *testing.T) {
	api := loadSpec(t)
	names := map[string]bool{}
	for _, tool := range mcpTools {
		if names[tool.Name] {
			t.Errorf("two tools are called %s", tool.Name)
		}
		names[tool.Name] = true
		if !regexp.MustCompile(`^[a-z][a-z_]*$`).MatchString(tool.Name) || tool.Title == "" || len(tool.Desc) < 40 {
			t.Errorf("tool %s needs a snake_case name, a title and a real description", tool.Name)
		}
		cmd, ok := lookup(tool.Command)
		if !ok {
			t.Errorf("tool %s sends command %q, which apiCommands doesn't have", tool.Name, tool.Command)
			continue
		}
		op, params, ok := api.operation(cmd.Method, cmd.Path)
		if !ok {
			t.Errorf("tool %s sends %s %s, which the spec doesn't define", tool.Name, cmd.Method, cmd.Path)
			continue
		}
		// What the spec takes, by name.
		want := map[string]*schema{}
		required := map[string]bool{}
		for _, p := range params {
			want[p.Name] = p.Schema
			required[p.Name] = p.Required
		}
		if body := op.bodySchema(); body != nil {
			s := body
			if s.Ref != "" {
				s, _, _ = api.resolve(s.Ref)
			}
			for name, prop := range s.Properties {
				if _, clash := want[name]; clash {
					t.Errorf("spec: %s %s has %q both as a parameter and in its body; a tool argument can't be both", cmd.Method, cmd.Path, name)
				}
				want[name] = prop
				required[name] = slices.Contains(s.Required, name)
			}
		}
		got := map[string]mcpProp{}
		for _, p := range tool.Props {
			got[p.Name] = p
			if p.Desc == "" {
				t.Errorf("tool %s: argument %s has no description", tool.Name, p.Name)
			}
		}
		for _, name := range keys(got) {
			if _, ok := want[name]; !ok {
				t.Errorf("MCP drift: tool %s takes %q, which %s %s doesn't", tool.Name, name, cmd.Method, cmd.Path)
			}
		}
		for _, name := range keys(want) {
			p, ok := got[name]
			if !ok {
				t.Errorf("MCP drift: %s %s takes %q, which tool %s doesn't offer", cmd.Method, cmd.Path, name, tool.Name)
				continue
			}
			s := want[name]
			wantTypes := slices.Clone(s.types())
			slices.Sort(wantTypes)
			gotTypes := []string{p.Type}
			if p.Nullable {
				gotTypes = append(gotTypes, "null")
			}
			slices.Sort(gotTypes)
			if !slices.Equal(gotTypes, wantTypes) {
				t.Errorf("MCP drift: %s.%s is %v in the tool, %v in the spec", tool.Name, name, gotTypes, wantTypes)
			}
			var wantEnum []string
			for _, e := range s.Enum {
				var v string
				json.Unmarshal(e, &v)
				wantEnum = append(wantEnum, v)
			}
			if !slices.Equal(p.Enum, wantEnum) {
				t.Errorf("MCP drift: %s.%s has enum %v, the spec %v", tool.Name, name, p.Enum, wantEnum)
			}
			bound := func(b *int) *float64 {
				if b == nil {
					return nil
				}
				f := float64(*b)
				return &f
			}
			if !reflect.DeepEqual(bound(p.Min), s.Minimum) || !reflect.DeepEqual(bound(p.Max), s.Maximum) {
				t.Errorf("MCP drift: %s.%s bounds differ from the spec's", tool.Name, name)
			}
			if p.Required != required[name] {
				t.Errorf("MCP drift: %s.%s required=%v, the spec says %v", tool.Name, name, p.Required, required[name])
			}
		}
	}
}

// TestMCPCoversOperations: every operation an assistant could use has a tool. The stream, the
// calendar feed and the service pages are for other clients.
func TestMCPCoversOperations(t *testing.T) {
	api := loadSpec(t)
	notTools := []string{"GET /api/changes", "GET /api/calendars/{id}/events.ics", "GET /health", "GET /openapi.json", "GET /docs"}
	for _, path := range keys(api.Paths) {
		for _, method := range []string{"GET", "PUT", "POST", "PATCH", "DELETE"} {
			if api.Paths[path].operation(method) == nil || slices.Contains(notTools, method+" "+path) {
				continue
			}
			if !slices.ContainsFunc(mcpTools, func(tool mcpTool) bool {
				cmd, _ := lookup(tool.Command)
				return cmd.Method == method && cmd.Path == path
			}) {
				t.Errorf("MCP drift: no tool sends %s %s", method, path)
			}
		}
	}
}

// mcpSession runs `zubi mcp` on the given messages and returns its answers by ID, and the
// answers without one.
func mcpSession(t *testing.T, args []string, messages ...string) (byID map[string]map[string]any, other []map[string]any, stderr string) {
	t.Helper()
	code, out, errOut := runZubi(t, strings.Join(messages, "\n")+"\n", append([]string{"mcp"}, args...)...)
	if code != exitOK {
		t.Fatalf("zubi mcp exited %d, stderr %q", code, errOut)
	}
	byID = map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout has a line that isn't JSON: %q", line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("answer without jsonrpc 2.0: %s", line)
		}
		if id, ok := msg["id"]; ok && id != nil {
			byID[fmt.Sprint(id)] = msg
		} else {
			other = append(other, msg)
		}
	}
	return byID, other, errOut
}

func call(id int, tool string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

// toolText returns a tools/call answer's text and isError.
func toolText(t *testing.T, msg map[string]any) (string, bool) {
	t.Helper()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", msg)
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("want one content item: %v", content)
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Fatalf("content type %v", item["type"])
	}
	isError, _ := result["isError"].(bool)
	return item["text"].(string), isError
}

func TestMCPEndToEnd(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.answer("POST /api/reminders", 403, string(must(t, "error_read_only.json")))
	byID, other, stderr := mcpSession(t, f.args(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, "list_reminders", `{"list":"Groceries","q":"milk"}`),
		call(4, "create_reminder", `{"title":"Milk","list":"Errands"}`),
		`{"jsonrpc":"2.0","id":"five","method":"ping"}`,
	)
	if len(other) != 0 {
		t.Errorf("answers without an ID (the notification must get none): %v", other)
	}
	if !strings.Contains(stderr, "serving Zubi at "+f.URL) {
		t.Errorf("stderr %q", stderr)
	}

	init := byID["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion %v", init["protocolVersion"])
	}
	if info := init["serverInfo"].(map[string]any); info["name"] != "zubi" || info["version"] != "dev" {
		t.Errorf("serverInfo %v", info)
	}
	if _, ok := init["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("capabilities %v", init["capabilities"])
	}
	if !strings.Contains(init["instructions"].(string), "Nothing can ever be deleted") {
		t.Errorf("instructions %v", init["instructions"])
	}

	tools := byID["2"]["result"].(map[string]any)["tools"].([]any)
	var listed []string
	for _, raw := range tools {
		tool := raw.(map[string]any)
		listed = append(listed, tool["name"].(string))
		schema := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Errorf("%s: inputSchema %v", tool["name"], schema)
		}
		if _, ok := schema["required"].([]any); !ok {
			t.Errorf("%s: inputSchema.required should be an array: %v", tool["name"], schema["required"])
		}
	}
	want := []string{"list_reminder_lists", "list_reminders", "list_calendars", "list_events", "list_note_folders", "list_notes",
		"get_note", "create_reminder", "update_reminder", "create_event", "update_event", "create_note", "update_note", "get_key"}
	if !slices.Equal(listed, want) {
		t.Errorf("tools %v, want %v", listed, want)
	}
	reminders := tools[1].(map[string]any)
	if ann := reminders["annotations"].(map[string]any); ann["readOnlyHint"] != true || ann["destructiveHint"] != false {
		t.Errorf("list_reminders annotations %v", ann)
	}
	q := reminders["inputSchema"].(map[string]any)["properties"].(map[string]any)["q"].(map[string]any)
	if q["type"] != "string" || q["description"] == "" {
		t.Errorf("list_reminders.q %v", q)
	}
	update := tools[8].(map[string]any)["inputSchema"].(map[string]any)
	if due := update["properties"].(map[string]any)["due"].(map[string]any); !reflect.DeepEqual(due["type"], []any{"string", "null"}) {
		t.Errorf("update_reminder.due type %v", due["type"])
	}
	if !reflect.DeepEqual(update["required"], []any{"id"}) {
		t.Errorf("update_reminder required %v", update["required"])
	}

	text, isError := toolText(t, byID["3"])
	if isError || text != strings.TrimSpace(string(must(t, "reminders.json"))) {
		t.Errorf("list_reminders: isError %v, text %q", isError, text)
	}
	text, isError = toolText(t, byID["4"])
	if !isError || !strings.Contains(text, "Zubi answered 403: The list Errands is read-only.") || !strings.Contains(text, "readWrite") {
		t.Errorf("create_reminder on a read-only list: isError %v, text %q", isError, text)
	}
	if res, ok := byID["five"]["result"].(map[string]any); !ok || len(res) != 0 {
		t.Errorf("ping: %v", byID["five"])
	}

	var sent []string
	for _, r := range f.sent() {
		sent = append(sent, r.Method+" "+r.Template+" "+r.Query.Encode()+" "+r.Body)
	}
	wantSent := []string{
		"GET /api/reminders list=Groceries&q=milk ",
		`POST /api/reminders  {"title":"Milk","list":"Errands"}`,
	}
	slices.Sort(sent) // tool calls run concurrently
	if !slices.Equal(sent, wantSent) {
		t.Errorf("sent %q, want %q", sent, wantSent)
	}
}

// TestMCPEveryTool calls each tool once with every argument it takes; the fake checks each
// request against the spec.
func TestMCPEveryTool(t *testing.T) {
	examples := map[string]string{
		"list_reminder_lists": `{}`,
		"list_reminders":      `{"list":"Groceries","due":"today","completed":"true","q":"milk"}`,
		"list_calendars":      `{}`,
		"list_events":         `{"from":"2026-11-02","to":"2026-11-06","calendar":"Work","q":"standup"}`,
		"list_note_folders":   `{}`,
		"list_notes":          `{"folder":"Recipes","limit":20,"q":"pancake"}`,
		"get_note":            `{"id":"` + noteID + `"}`,
		"create_reminder":     `{"title":"Buy eggs","list":"Groceries","notes":"a dozen","due":"2026-11-04","priority":5}`,
		"update_reminder":     `{"id":"R1","title":"Buy oat milk","list":"Errands","notes":null,"due":"2026-11-05T09:00:00Z","priority":1,"isCompleted":true}`,
		"create_event":        `{"title":"Dentist","calendar":"Work","start":"2026-11-03T09:00:00Z","end":"2026-11-03T09:30:00Z","isAllDay":false,"location":"Main St 1","notes":"n","url":"https://example.com"}`,
		"update_event":        `{"id":"E2","occurrence":"2026-11-02T09:00:00Z","title":"Standup","calendar":"Work","start":"2026-11-02T09:30:00Z","end":"2026-11-02T09:45:00Z","isAllDay":false,"location":null,"notes":"n","url":null}`,
		"create_note":         `{"folder":"Recipes","title":"Pancakes","text":"2 eggs"}`,
		"update_note":         `{"id":"` + noteID + `","title":"Sourdough","html":"<div>375 g water</div>"}`,
		"get_key":             `{}`,
	}
	wantBody := map[string]string{
		"update_reminder": `{"title":"Buy oat milk","list":"Errands","notes":null,"due":"2026-11-05T09:00:00Z","priority":1,"isCompleted":true}`,
		"update_event":    `{"title":"Standup","calendar":"Work","start":"2026-11-02T09:30:00Z","end":"2026-11-02T09:45:00Z","isAllDay":false,"location":null,"notes":"n","url":null}`,
		"update_note":     `{"title":"Sourdough","html":"<div>375 g water</div>"}`,
	}
	for _, tool := range mcpTools {
		args, ok := examples[tool.Name]
		if !ok {
			t.Errorf("tool %s has no example here", tool.Name)
			continue
		}
		t.Run(tool.Name, func(t *testing.T) {
			isolate(t)
			f := newFakeZubi(t)
			byID, _, _ := mcpSession(t, f.args(), call(1, tool.Name, args))
			text, isError := toolText(t, byID["1"])
			if isError {
				t.Fatalf("isError: %s", text)
			}
			if !json.Valid([]byte(text)) {
				t.Errorf("not the API's JSON: %q", text)
			}
			sent := f.sent()
			if len(sent) != 1 {
				t.Fatalf("want one request, sent %+v", sent)
			}
			cmd, _ := lookup(tool.Command)
			if sent[0].Method != cmd.Method || sent[0].Template != cmd.Path {
				t.Errorf("sent %s %s", sent[0].Method, sent[0].Template)
			}
			// Every argument went somewhere: the query, the path or the body.
			var given map[string]any
			json.Unmarshal([]byte(args), &given)
			var body map[string]any
			json.Unmarshal([]byte(sent[0].Body), &body)
			for name := range given {
				_, inBody := body[name]
				if !sent[0].Query.Has(name) && sent[0].Path[name] == "" && !inBody {
					t.Errorf("argument %s was not sent", name)
				}
			}
			if want, ok := wantBody[tool.Name]; ok {
				var w any
				json.Unmarshal([]byte(want), &w)
				if !reflect.DeepEqual(any(body), w) {
					t.Errorf("body %s, want %s", sent[0].Body, want)
				}
			}
		})
	}
}

func TestMCPArgumentErrors(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	cases := []struct{ tool, args, want string }{
		{"list_reminders", `{"lists":"Groceries"}`, `unknown argument "lists"; list_reminders takes list, due, completed, q`},
		{"list_calendars", `{"x":1}`, "takes no arguments"},
		{"get_note", `{}`, "id is required"},
		{"get_note", `{"id":5}`, "id must be a string"},
		{"update_reminder", `{"id":""}`, "id is empty"},
		{"update_reminder", `{"id":"R1","title":null}`, "title must be a string"},
		{"update_reminder", `{"id":"R1","due":7}`, "due must be a string or null"},
		{"list_reminders", `{"due":"tomorrow"}`, "due must be one of today, overdue"},
		{"list_reminders", `{"completed":true}`, "completed must be a string"},
		{"list_notes", `{"limit":2.5}`, "limit must be an integer"},
		{"create_reminder", `{"title":"x","list":"L","priority":-1}`, "priority must be at least 0"},
		{"list_lists", `{}`, ""}, // an unknown tool is a protocol error, below
		{"get_key", `[1]`, "arguments must be a JSON object"},
	}
	var messages []string
	for i, c := range cases {
		messages = append(messages, call(i, c.tool, c.args))
	}
	byID, _, _ := mcpSession(t, f.args(), messages...)
	for i, c := range cases {
		msg := byID[fmt.Sprint(i)]
		if c.want == "" {
			if e, ok := msg["error"].(map[string]any); !ok || e["code"] != float64(rpcInvalidParams) {
				t.Errorf("%s: want a -32602 error, got %v", c.tool, msg)
			}
			continue
		}
		text, isError := toolText(t, msg)
		if !isError || !strings.Contains(text, c.want) {
			t.Errorf("%s %s: isError %v, text %q, want %q", c.tool, c.args, isError, text, c.want)
		}
	}
	if len(f.sent()) != 0 {
		t.Errorf("invalid arguments should send nothing, sent %+v", f.sent())
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	byID, other, _ := mcpSession(t, f.args(),
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`not json`,
		`[{"jsonrpc":"2.0","id":2,"method":"ping"}]`,
		`{"id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":"nope"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`,
		`{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":5,"result":{}}`,
		`{"jsonrpc":"2.0","id":6,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	)
	code := func(msg map[string]any) any {
		if e, ok := msg["error"].(map[string]any); ok {
			return e["code"]
		}
		return nil
	}
	for id, want := range map[string]int{"1": rpcMethodNotFound, "3": rpcInvalidRequest, "4": rpcInvalidParams} {
		if got := code(byID[id]); got != float64(want) {
			t.Errorf("id %s: error code %v, want %d (%v)", id, got, want, byID[id])
		}
	}
	var codes []any
	for _, msg := range other {
		codes = append(codes, code(msg))
	}
	if !slices.Equal(codes, []any{float64(rpcParseError), float64(rpcInvalidRequest), float64(rpcInvalidRequest)}) {
		t.Errorf("answers without an ID: %v", other)
	}
	if _, answered := byID["5"]; answered {
		t.Error("a response from the client got an answer")
	}
	if v := byID["6"]["result"].(map[string]any)["protocolVersion"]; v != "2024-11-05" {
		t.Errorf("an older supported version should be kept, got %v", v)
	}
	if v := byID["7"]["result"].(map[string]any)["protocolVersion"]; v != mcpProtocolVersion {
		t.Errorf("an unknown version should get ours, got %v", v)
	}
}

func TestMCPWithoutKey(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	byID, _, stderr := mcpSession(t, []string{"--url", f.URL},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		call(2, "list_reminder_lists", `{}`),
	)
	if _, ok := byID["1"]["result"]; !ok {
		t.Errorf("initialize should work without a key: %v", byID["1"])
	}
	text, isError := toolText(t, byID["2"])
	if !isError || !strings.Contains(text, "ZUBI_API_KEY") {
		t.Errorf("isError %v, text %q", isError, text)
	}
	if !strings.Contains(stderr, "no API key") || len(f.seen()) != 0 {
		t.Errorf("stderr %q, sent %v", stderr, f.seen())
	}
}

func TestMCPErrorTexts(t *testing.T) {
	isolate(t)
	f := newFakeZubi(t)
	f.answer("/api/notes/folders", 503, string(must(t, "error_unavailable.json")))
	byID, _, _ := mcpSession(t, []string{"--url", f.URL, "--key", "wrong"}, call(1, "get_key", `{}`))
	if text, isError := toolText(t, byID["1"]); !isError || !strings.Contains(text, "401") || !strings.Contains(text, "Settings › Keys") {
		t.Errorf("401: %v %q", isError, text)
	}
	byID, _, _ = mcpSession(t, f.args(),
		call(1, "get_note", `{"id":"x-coredata://nope"}`),
		call(2, "update_note", `{"id":"`+lockedNoteID+`","text":"x"}`),
		call(3, "list_note_folders", `{}`),
	)
	for id, want := range map[string]string{"1": "Zubi answered 404: Unknown or hidden note. It doesn't exist, or the user set it to Off", "2": "locked", "3": "503"} {
		if text, isError := toolText(t, byID[id]); !isError || !strings.Contains(text, want) {
			t.Errorf("%s: isError %v, text %q, want %q", id, isError, text, want)
		}
	}
	byID, _, _ = mcpSession(t, []string{"--url", "http://127.0.0.1:1", "--key", testKey}, call(1, "get_key", `{}`))
	if text, isError := toolText(t, byID["1"]); !isError || !strings.Contains(text, "isn't reachable") {
		t.Errorf("unreachable: %v %q", isError, text)
	}
}
