package main

// Drift tests: the CLI's request table (apiCommands) and response structs against openapi.json,
// the contract the server publishes. Messages say which side has what, so a failure tells you
// whether to update the CLI or to question the spec.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const apiFile = "cli/cmd/zubi/api.go"

func TestSpecDefinesEveryRequest(t *testing.T) {
	api := loadSpec(t)
	for _, cmd := range apiCommands {
		op, _, ok := api.operation(cmd.Method, cmd.Path)
		if !ok {
			t.Errorf("CLI drift: command %q sends %s %s, which the spec doesn't define", cmd.Name, cmd.Method, cmd.Path)
			continue
		}
		if needs := api.needsAuth(op); needs == cmd.Public {
			t.Errorf("drift: command %q has Public=%v, but the spec's %s %s needs a key=%v", cmd.Name, cmd.Public, cmd.Method, cmd.Path, needs)
		}
	}
}

// TestSpecParameters: every query parameter a command may send is declared; every path
// placeholder is a declared path parameter; every required parameter is sent.
func TestSpecParameters(t *testing.T) {
	api := loadSpec(t)
	for _, cmd := range apiCommands {
		_, params, ok := api.operation(cmd.Method, cmd.Path)
		if !ok {
			continue // TestSpecDefinesEveryRequest reports it
		}
		for _, name := range cmd.Query {
			if _, ok := params["query:"+name]; !ok {
				t.Errorf("CLI drift: command %q may send query parameter %q, which the spec doesn't declare for %s %s", cmd.Name, name, cmd.Method, cmd.Path)
			}
		}
		placeholders := map[string]bool{}
		for _, m := range placeholder.FindAllStringSubmatch(cmd.Path, -1) {
			placeholders[m[1]] = true
			p, ok := params["path:"+m[1]]
			if !ok {
				t.Errorf("spec error: %s %s has {%s} in its path but declares no path parameter %q", cmd.Method, cmd.Path, m[1], m[1])
			} else if !p.Required {
				t.Errorf("spec error: path parameter %q of %s %s must be required", m[1], cmd.Method, cmd.Path)
			}
		}
		for _, p := range params {
			switch {
			case p.In == "path" && !placeholders[p.Name]:
				t.Errorf("spec error: %s %s declares path parameter %q, which its path doesn't contain", cmd.Method, cmd.Path, p.Name)
			case !p.Required || p.In == "path":
			case p.In != "query":
				t.Errorf("spec drift: %s %s requires %s parameter %q; the CLI only sends query and path parameters", cmd.Method, cmd.Path, p.In, p.Name)
			case !slices.Contains(cmd.Query, p.Name):
				t.Errorf("CLI drift: the spec requires query parameter %q on %s %s, but command %q doesn't send it", p.Name, cmd.Method, cmd.Path, cmd.Name)
			}
		}
	}
}

// TestSpecQueryParametersUsed: every query parameter the spec declares is one some command can
// send, so a new filter shows up here instead of going unnoticed.
func TestSpecQueryParametersUsed(t *testing.T) {
	api := loadSpec(t)
	for _, path := range keys(api.Paths) {
		for _, method := range []string{"GET", "PUT", "POST", "PATCH", "DELETE"} {
			_, params, ok := api.operation(method, path)
			if !ok {
				continue
			}
			for _, p := range params {
				if p.In != "query" {
					continue
				}
				used := slices.ContainsFunc(apiCommands, func(c apiCommand) bool {
					return c.Method == method && c.Path == path && slices.Contains(c.Query, p.Name)
				})
				if !used && !slices.Contains([]string{"GET /openapi.json", "GET /docs"}, method+" "+path) {
					t.Errorf("spec drift: %s %s takes query parameter %q, which no command sends", method, path, p.Name)
				}
			}
		}
	}
}

// TestSpecRequestBodies compares each body type the CLI encodes with the operation's requestBody
// schema, by the rules compareBody describes.
func TestSpecRequestBodies(t *testing.T) {
	api := loadSpec(t)
	for _, cmd := range apiCommands {
		op, _, ok := api.operation(cmd.Method, cmd.Path)
		if !ok {
			continue
		}
		switch {
		case cmd.Body == nil && op.RequestBody != nil && op.RequestBody.Required:
			t.Errorf("CLI drift: the spec's %s %s requires a request body, but command %q sends none", cmd.Method, cmd.Path, cmd.Name)
		case cmd.Body == nil:
		case op.RequestBody == nil:
			t.Errorf("CLI drift: command %q sends a body to %s %s, which the spec says takes none", cmd.Name, cmd.Method, cmd.Path)
		case op.bodySchema() == nil:
			t.Errorf("CLI drift: command %q sends JSON to %s %s, whose body is %v", cmd.Name, cmd.Method, cmd.Path, keys(op.RequestBody.Content))
		default:
			for _, problem := range api.compareBody(reflect.TypeOf(cmd.Body), op.bodySchema(), reflect.TypeOf(cmd.Body).Name()) {
				t.Errorf("command %q: %s", cmd.Name, problem)
			}
		}
	}
}

// TestSpecResponseTypes compares each struct the CLI decodes with the schema of the success answer.
//
// The rule enforced, field by field:
//   - The struct's JSON names (from its json tags) equal the schema's properties, exactly.
//   - The Go type fits the schema type: string→string, boolean→bool, integer→an int kind,
//     number→an int or float kind, array→slice (items checked too), object→struct (via $ref,
//     whose schema name must equal the Go type's name) or map.
//   - A property the spec marks required is a value field, never a pointer.
//   - A property the spec doesn't mark required may be omitted by the server, so its field must
//     be a pointer (or a slice or map, whose nil is "absent"); a value field would turn absence
//     into a plausible-looking "" or false.
//
// It also checks that the success status the command expects is the only 2xx the spec documents.
func TestSpecResponseTypes(t *testing.T) {
	api := loadSpec(t)
	for _, cmd := range apiCommands {
		op, _, ok := api.operation(cmd.Method, cmd.Path)
		if !ok {
			continue
		}
		status := strconv.Itoa(cmd.status())
		var documented []string
		for code := range op.Responses {
			if strings.HasPrefix(code, "2") {
				documented = append(documented, code)
			}
		}
		if len(documented) != 1 || documented[0] != status {
			t.Errorf("drift: command %q expects %s from %s %s, but the spec documents success as %v", cmd.Name, status, cmd.Method, cmd.Path, documented)
		}
		success, ok := op.Responses[status]
		if !ok {
			continue
		}
		content, hasMedia := success.Content[cmd.media()]
		switch {
		case !hasMedia:
			t.Errorf("drift: command %q asks for %s, but the spec's %s %s answers %v", cmd.Name, cmd.media(), cmd.Method, cmd.Path, keys(success.Content))
		case cmd.Response == nil && cmd.media() == "application/json":
			t.Errorf("CLI drift: the spec says %s %s answers JSON, but command %q treats the body as text", cmd.Method, cmd.Path, cmd.Name)
		case cmd.Response != nil && cmd.media() != "application/json":
			t.Errorf("CLI drift: command %q decodes JSON, but asks for %s", cmd.Name, cmd.media())
		case cmd.Response != nil:
			for _, problem := range api.compareType(reflect.TypeOf(cmd.Response), content.Schema, reflect.TypeOf(cmd.Response).Name()) {
				t.Errorf("command %q: %s", cmd.Name, problem)
			}
		}
	}
}

// TestSpecChange: the messages `zubi watch` decodes are the spec's Change.
func TestSpecChange(t *testing.T) {
	api := loadSpec(t)
	for _, problem := range api.compareType(reflect.TypeOf(Change{}), &schema{Ref: refPrefix + "Change"}, "Change") {
		t.Error(problem)
	}
	for _, payload := range []string{`{"source":"reminders","origin":"elsewhere"}`, `{"source":"notes","origin":"zubi"}`, `{"source":"calendar","origin":"zubi"}`} {
		if errs := api.validateJSON(&schema{Ref: refPrefix + "Change"}, []byte(payload)); len(errs) > 0 {
			t.Errorf("the watch tests send %s, which the spec's Change refuses: %v", payload, errs)
		}
	}
	for _, access := range []string{accessRead, accessReadWrite} {
		if errs := api.validateJSON(&schema{Ref: refPrefix + "KeyInfo"}, []byte(`{"name":"k","access":"`+access+`"}`)); len(errs) > 0 {
			t.Errorf("describeAccess knows access %q, which the spec's KeyInfo refuses: %v", access, errs)
		}
	}
}

// TestSpecOperationsCovered: every operation has a command or an internal request, so a new
// endpoint in the spec shows up here instead of going unnoticed.
func TestSpecOperationsCovered(t *testing.T) {
	api := loadSpec(t)
	for _, path := range keys(api.Paths) {
		for _, method := range []string{"GET", "PUT", "POST", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			if api.Paths[path].operation(method) == nil {
				continue
			}
			if !slices.ContainsFunc(apiCommands, func(c apiCommand) bool { return c.Method == method && c.Path == path }) {
				t.Errorf("spec drift: the spec defines %s %s, which no command in apiCommands sends", method, path)
			}
		}
	}
}

// TestSpecErrorSchema: errorMessage reads the spec's Error shape.
func TestSpecErrorSchema(t *testing.T) {
	api := loadSpec(t)
	s := api.Components.Schemas["Error"]
	if s == nil {
		t.Fatal("spec drift: components.schemas.Error is gone; errorMessage in " + apiFile + " reads it")
	}
	body := []byte(`{"error":{"message":"Unknown or hidden list"}}`)
	if errs := api.validateJSON(s, body); len(errs) > 0 {
		t.Fatalf("spec drift: the Error shape errorMessage reads no longer validates: %v", errs)
	}
	if got := errorMessage(body); got != "Unknown or hidden list" {
		t.Errorf("errorMessage = %q", got)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// jsonFields maps a struct's JSON names to its fields, as encoding/json sees them.
func jsonFields(t reflect.Type) map[string]reflect.StructField {
	fields := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f
	}
	return fields
}

func (api *openAPI) compareType(goType reflect.Type, s *schema, at string) []string {
	if s == nil {
		return []string{at + ": the spec gives no schema"}
	}
	if s.Ref != "" {
		target, name, err := api.resolve(s.Ref)
		if err != nil {
			return []string{at + ": spec error: " + err.Error()}
		}
		if goType.Kind() == reflect.Struct && goType.Name() != name {
			return []string{fmt.Sprintf("%s: the spec's schema is %s but the CLI decodes it into %s; name the Go type after the schema", at, name, goType.Name())}
		}
		return api.compareType(goType, target, at)
	}
	types := s.types()
	if len(types) != 1 {
		return []string{fmt.Sprintf("%s: the spec's type is %v; the CLI's structs expect exactly one type (the server omits absent fields rather than sending null)", at, types)}
	}
	kind := goType.Kind()
	mismatch := func() []string {
		return []string{fmt.Sprintf("%s: the spec says %s, the CLI decodes it as Go %s", at, types[0], goType)}
	}
	switch types[0] {
	case "string":
		if kind != reflect.String {
			return mismatch()
		}
	case "boolean":
		if kind != reflect.Bool {
			return mismatch()
		}
	case "integer":
		if !isInt(kind) {
			return mismatch()
		}
	case "number":
		if !isInt(kind) && kind != reflect.Float32 && kind != reflect.Float64 {
			return mismatch()
		}
	case "array":
		if kind != reflect.Slice {
			return mismatch()
		}
		return api.compareType(goType.Elem(), s.Items, at+"[]")
	case "object":
		switch kind {
		case reflect.Map:
			return nil
		case reflect.Struct:
			return api.compareStruct(goType, s, at)
		default:
			return mismatch()
		}
	default:
		return []string{fmt.Sprintf("%s: the spec's type %q has no Go counterpart in this test", at, types[0])}
	}
	return nil
}

func (api *openAPI) compareStruct(goType reflect.Type, s *schema, at string) []string {
	var problems []string
	fields := jsonFields(goType)
	for _, name := range keys(fields) {
		if _, ok := s.Properties[name]; !ok {
			problems = append(problems, fmt.Sprintf("CLI drift: %s.%s decodes %q, which the spec's schema doesn't define; remove it from %s, or add it to the spec",
				goType.Name(), fields[name].Name, name, apiFile))
		}
	}
	for _, name := range keys(s.Properties) {
		field, ok := fields[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("spec drift: the spec's schema for %s has property %q, which %s lacks; add a field to %s",
				at, name, goType.Name(), apiFile))
			continue
		}
		where := at + "." + name
		required := slices.Contains(s.Required, name)
		ft := field.Type
		switch {
		case required && ft.Kind() == reflect.Pointer:
			problems = append(problems, fmt.Sprintf("CLI drift: %s: the spec requires %q, so %s.%s should be a value, not %s", where, name, goType.Name(), field.Name, ft))
		case !required && ft.Kind() != reflect.Pointer && ft.Kind() != reflect.Slice && ft.Kind() != reflect.Map:
			problems = append(problems, fmt.Sprintf("CLI drift: %s: the spec doesn't require %q, so the server may omit it; make %s.%s a pointer (*%s)", where, name, goType.Name(), field.Name, ft))
		}
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		problems = append(problems, api.compareType(ft, s.Properties[name], where)...)
	}
	return problems
}

// compareBody checks a request body type against its schema. Field by field:
//   - The struct's JSON names equal the schema's properties, exactly.
//   - A required property is a value field without omitempty, so it is always sent.
//   - An optional property whose type allows null is a Nullable with omitzero: left out when
//     not given, null to clear it, or a value.
//   - Any other optional property is a pointer with omitempty: left out when not given, and
//     never null, which the spec doesn't allow there.
//   - The Go type under the pointer or Nullable fits the schema's (non-null) type.
func (api *openAPI) compareBody(goType reflect.Type, s *schema, at string) []string {
	if s.Ref != "" {
		target, name, err := api.resolve(s.Ref)
		if err != nil {
			return []string{at + ": spec error: " + err.Error()}
		}
		if goType.Name() != name {
			return []string{fmt.Sprintf("%s: the spec's schema is %s but the CLI encodes %s; name the Go type after the schema", at, name, goType.Name())}
		}
		return api.compareBody(goType, target, at)
	}
	if !slices.Equal(s.types(), []string{"object"}) || goType.Kind() != reflect.Struct {
		return []string{fmt.Sprintf("%s: the spec's body is %v, the CLI encodes Go %s", at, s.types(), goType)}
	}
	var problems []string
	fields := jsonFields(goType)
	for _, name := range keys(fields) {
		if _, ok := s.Properties[name]; !ok {
			problems = append(problems, fmt.Sprintf("CLI drift: %s.%s sends %q, which the spec's schema doesn't define (the server refuses unknown fields)", goType.Name(), fields[name].Name, name))
		}
	}
	for _, name := range keys(s.Properties) {
		field, ok := fields[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("spec drift: the spec's %s has property %q, which %s lacks; add a field to %s", at, name, goType.Name(), apiFile))
			continue
		}
		where := at + "." + name
		prop := s.Properties[name]
		_, tagOpts, _ := strings.Cut(field.Tag.Get("json"), ",")
		opts := strings.Split(tagOpts, ",")
		var base []string
		nullable := false
		for _, typ := range prop.types() {
			if typ == "null" {
				nullable = true
			} else {
				base = append(base, typ)
			}
		}
		if len(base) != 1 {
			problems = append(problems, fmt.Sprintf("%s: the spec's type is %v; the CLI encodes one type, optionally nullable", where, prop.types()))
			continue
		}
		ft := field.Type
		isNullable := strings.HasPrefix(ft.Name(), "Nullable[")
		switch {
		case slices.Contains(s.Required, name):
			if ft.Kind() == reflect.Pointer || isNullable || slices.Contains(opts, "omitempty") || slices.Contains(opts, "omitzero") || nullable {
				problems = append(problems, fmt.Sprintf("CLI drift: %s: the spec requires %q, so %s.%s should be a plain value that is always sent", where, name, goType.Name(), field.Name))
			}
		case nullable:
			if !isNullable || !slices.Contains(opts, "omitzero") {
				problems = append(problems, fmt.Sprintf("CLI drift: %s: the spec lets an edit clear %q with null, so %s.%s should be Nullable[...] with omitzero", where, name, goType.Name(), field.Name))
			}
		default:
			if ft.Kind() != reflect.Pointer || !slices.Contains(opts, "omitempty") {
				problems = append(problems, fmt.Sprintf("CLI drift: %s: %q is optional and may not be null, so %s.%s should be a pointer with omitempty", where, name, goType.Name(), field.Name))
			}
		}
		switch {
		case isNullable:
			value, _ := ft.FieldByName("Value")
			ft = value.Type
		case ft.Kind() == reflect.Pointer:
			ft = ft.Elem()
		}
		single := *prop
		single.Type, _ = json.Marshal(base[0])
		problems = append(problems, api.compareType(ft, &single, where)...)
	}
	return problems
}

func isInt(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// fixtures maps each payload in testdata/ to the command whose success answer it is;
// error_*.json files are the spec's Error schema.
var fixtures = map[string]string{
	"lists.json":           "lists",
	"reminders.json":       "reminders",
	"calendars.json":       "calendars",
	"events.json":          "events",
	"folders.json":         "folders",
	"notes.json":           "notes",
	"note.json":            "note",
	"note_locked.json":     "note",
	"reminder_added.json":  "reminders add",
	"reminder_edited.json": "reminders edit",
	"event_added.json":     "events add",
	"event_edited.json":    "events edit",
	"note_added.json":      "notes add",
	"note_edited.json":     "note edit",
	"key.json":             "key",
}

// successSchema is the schema of the answer a command expects.
func successSchema(t *testing.T, api *openAPI, cmd apiCommand) *schema {
	t.Helper()
	op, _, ok := api.operation(cmd.Method, cmd.Path)
	if !ok {
		t.Fatalf("%s %s is not in the spec", cmd.Method, cmd.Path)
	}
	return op.Responses[strconv.Itoa(cmd.status())].Content["application/json"].Schema
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestFixturesMatchSpec validates every file in testdata/ against the spec, so the payloads the
// command tests serve can't drift from what the server sends.
func TestFixturesMatchSpec(t *testing.T) {
	api := loadSpec(t)
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		name := filepath.Base(file)
		t.Run(name, func(t *testing.T) {
			var s *schema
			if strings.HasPrefix(name, "error_") {
				s = api.Components.Schemas["Error"]
			} else {
				command, ok := fixtures[name]
				if !ok {
					t.Fatalf("testdata/%s is not listed in fixtures, so nothing says which schema it follows", name)
				}
				cmd, _ := lookup(command)
				s = successSchema(t, api, cmd)
			}
			for _, problem := range api.validateJSON(s, readFixture(t, name)) {
				t.Errorf("fixture drift: testdata/%s: %s", name, problem)
			}
		})
	}
}

// TestFixturesDecodeStrictly decodes each fixture into the CLI's type with unknown fields refused.
// Commands that make the same request (reminders edit and done) share a fixture.
func TestFixturesDecodeStrictly(t *testing.T) {
	covered := map[string]bool{}
	request := func(c apiCommand) string { return c.Method + " " + c.Path }
	for name, command := range fixtures {
		cmd, ok := lookup(command)
		if !ok || cmd.Response == nil {
			t.Fatalf("fixture %s names command %q, which has no JSON response", name, command)
		}
		covered[request(cmd)] = true
		dec := json.NewDecoder(bytes.NewReader(readFixture(t, name)))
		dec.DisallowUnknownFields()
		out := reflect.New(reflect.TypeOf(cmd.Response)).Interface()
		if err := dec.Decode(out); err != nil {
			t.Errorf("testdata/%s into %T: %v", name, out, err)
		}
	}
	for _, cmd := range apiCommands {
		if cmd.Response != nil && !covered[request(cmd)] {
			t.Errorf("command %q decodes JSON but has no fixture in testdata/", cmd.Name)
		}
	}
}
