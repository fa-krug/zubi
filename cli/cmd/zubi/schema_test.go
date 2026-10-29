package main

// A small model of openapi.json and a strict validator for the subset of JSON Schema the spec
// uses. The validator refuses keywords it doesn't implement, so a spec that starts using, say,
// oneOf fails loudly here instead of being half-checked.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const specPath = "../../../Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json"

type openAPI struct {
	Paths      map[string]pathItem   `json:"paths"`
	Security   []map[string][]string `json:"security"`
	Components struct {
		Schemas         map[string]*schema `json:"schemas"`
		SecuritySchemes map[string]struct {
			Type   string `json:"type"`
			Scheme string `json:"scheme"`
			In     string `json:"in"`
			Name   string `json:"name"`
		} `json:"securitySchemes"`
	} `json:"components"`
}

type pathItem struct {
	Parameters []parameter `json:"parameters"`
	Get        *operation  `json:"get"`
	Put        *operation  `json:"put"`
	Post       *operation  `json:"post"`
	Patch      *operation  `json:"patch"`
	Delete     *operation  `json:"delete"`
	Head       *operation  `json:"head"`
	Options    *operation  `json:"options"`
}

func (p pathItem) operation(method string) *operation {
	return map[string]*operation{
		"GET": p.Get, "PUT": p.Put, "POST": p.Post, "PATCH": p.Patch,
		"DELETE": p.Delete, "HEAD": p.Head, "OPTIONS": p.Options,
	}[strings.ToUpper(method)]
}

type operation struct {
	OperationID string                 `json:"operationId"`
	Parameters  []parameter            `json:"parameters"`
	Security    *[]map[string][]string `json:"security"`
	RequestBody *struct {
		Required bool                 `json:"required"`
		Content  map[string]mediaType `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]mediaType `json:"content"`
	} `json:"responses"`
}

type mediaType struct {
	Schema *schema `json:"schema"`
}

// bodySchema is the JSON schema of the operation's request body, or nil when it takes none.
func (op *operation) bodySchema() *schema {
	if op.RequestBody == nil {
		return nil
	}
	return op.RequestBody.Content["application/json"].Schema
}

type parameter struct {
	Name     string  `json:"name"`
	In       string  `json:"in"`
	Required bool    `json:"required"`
	Schema   *schema `json:"schema"`
}

type schema struct {
	Ref                  string             `json:"$ref"`
	Type                 json.RawMessage    `json:"type"`
	Properties           map[string]*schema `json:"properties"`
	Required             []string           `json:"required"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Items                *schema            `json:"items"`
	Enum                 []json.RawMessage  `json:"enum"`
	Const                json.RawMessage    `json:"const"`
	Pattern              string             `json:"pattern"`
	Minimum              *float64           `json:"minimum"`
	Maximum              *float64           `json:"maximum"`

	unsupported []string
}

// Keywords the validator enforces, and annotations it may ignore.
var schemaKeywords = map[string]bool{
	"$ref": true, "type": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "enum": true, "const": true, "pattern": true, "minimum": true, "maximum": true,
	"description": true, "title": true, "format": true, "example": true, "examples": true,
	"default": true, "deprecated": true,
}

func (s *schema) UnmarshalJSON(data []byte) error {
	type plain schema
	if err := json.Unmarshal(data, (*plain)(s)); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for k := range keys {
		if !schemaKeywords[k] {
			s.unsupported = append(s.unsupported, k)
		}
	}
	sort.Strings(s.unsupported)
	return nil
}

func (s *schema) types() []string {
	if len(s.Type) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(s.Type, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(s.Type, &many)
	return many
}

func (s *schema) closed() bool {
	return string(bytes.TrimSpace(s.AdditionalProperties)) == "false"
}

var (
	specOnce   sync.Once
	specParsed *openAPI
	specErr    error
)

func loadSpec(t *testing.T) *openAPI {
	t.Helper()
	specOnce.Do(func() {
		data, err := os.ReadFile(specPath)
		if err != nil {
			specErr = fmt.Errorf("reading the API contract: %w", err)
			return
		}
		var api openAPI
		if err := json.Unmarshal(data, &api); err != nil {
			specErr = fmt.Errorf("%s is not valid OpenAPI JSON: %w", specPath, err)
			return
		}
		specParsed = &api
	})
	if specErr != nil {
		t.Fatal(specErr)
	}
	return specParsed
}

const refPrefix = "#/components/schemas/"

func (api *openAPI) resolve(ref string) (*schema, string, error) {
	name, ok := strings.CutPrefix(ref, refPrefix)
	if !ok {
		return nil, "", fmt.Errorf("$ref %q is not under %s", ref, refPrefix)
	}
	s, ok := api.Components.Schemas[name]
	if !ok {
		return nil, "", fmt.Errorf("$ref %q names no schema", ref)
	}
	return s, name, nil
}

// operation finds method+path, with the path item's parameters merged into the operation's.
func (api *openAPI) operation(method, path string) (*operation, map[string]parameter, bool) {
	item, ok := api.Paths[path]
	if !ok {
		return nil, nil, false
	}
	op := item.operation(method)
	if op == nil {
		return nil, nil, false
	}
	params := map[string]parameter{}
	for _, p := range append(slices.Clone(item.Parameters), op.Parameters...) {
		params[p.In+":"+p.Name] = p
	}
	return op, params, true
}

// match finds the operation a concrete request path belongs to: "/api/reminders/R%2F1" is
// "/api/reminders/{id}" with id "R/1". A literal path wins over a template.
func (api *openAPI) match(method, escapedPath string) (op *operation, params map[string]parameter, template string, values map[string]string, ok bool) {
	segments := strings.Split(escapedPath, "/")
	templates := keys(api.Paths)
	sort.SliceStable(templates, func(i, j int) bool {
		return strings.Count(templates[i], "{") < strings.Count(templates[j], "{")
	})
	for _, tmpl := range templates {
		parts := strings.Split(tmpl, "/")
		if len(parts) != len(segments) {
			continue
		}
		vals := map[string]string{}
		matched := true
		for i, part := range parts {
			if name, isParam := strings.CutPrefix(part, "{"); isParam && strings.HasSuffix(name, "}") {
				v, err := url.PathUnescape(segments[i])
				if err != nil || v == "" {
					matched = false
					break
				}
				vals[strings.TrimSuffix(name, "}")] = v
			} else if part != segments[i] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if op, params, ok := api.operation(method, tmpl); ok {
			return op, params, tmpl, vals, true
		}
	}
	return nil, nil, "", nil, false
}

// queryKeyParam is the query parameter op takes the key in, when one of its security schemes
// is an API key in the query; "" when it takes none.
func (api *openAPI) queryKeyParam(op *operation) string {
	security := api.Security
	if op.Security != nil {
		security = *op.Security
	}
	for _, requirement := range security {
		for name := range requirement {
			if s, ok := api.Components.SecuritySchemes[name]; ok && s.Type == "apiKey" && s.In == "query" {
				return s.Name
			}
		}
	}
	return ""
}

func (api *openAPI) needsAuth(op *operation) bool {
	if op.Security != nil {
		return len(*op.Security) > 0
	}
	return len(api.Security) > 0
}

// decodeValue parses JSON keeping numbers as json.Number, so integer and number stay apart.
func decodeValue(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

func sameJSON(raw json.RawMessage, v any) bool {
	want, err := decodeValue(raw)
	if err != nil {
		return false
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(v)
	return bytes.Equal(a, b)
}

func typeMatches(name string, v any) bool {
	switch name {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(n.String(), 10, 64)
		return err == nil
	}
	return false
}

// validate returns every way v breaks s; at is the JSON path, for messages.
func (api *openAPI) validate(s *schema, v any, at string) []string {
	if s == nil {
		return []string{at + ": no schema"}
	}
	if len(s.unsupported) > 0 {
		return []string{fmt.Sprintf("%s: the schema uses %v, which the test validator doesn't implement; extend schema_test.go", at, s.unsupported)}
	}
	var errs []string
	if s.Ref != "" {
		target, _, err := api.resolve(s.Ref)
		if err != nil {
			return []string{at + ": " + err.Error()}
		}
		errs = append(errs, api.validate(target, v, at)...)
	}
	if types := s.types(); len(types) > 0 && !slices.ContainsFunc(types, func(t string) bool { return typeMatches(t, v) }) {
		return append(errs, fmt.Sprintf("%s: %s is not of type %v", at, describe(v), types))
	}
	if len(s.Const) > 0 && !sameJSON(s.Const, v) {
		errs = append(errs, fmt.Sprintf("%s: %s is not the constant %s", at, describe(v), s.Const))
	}
	if len(s.Enum) > 0 && !slices.ContainsFunc(s.Enum, func(e json.RawMessage) bool { return sameJSON(e, v) }) {
		errs = append(errs, fmt.Sprintf("%s: %s is not one of the enum values", at, describe(v)))
	}
	if str, ok := v.(string); ok && s.Pattern != "" {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: pattern %q does not compile: %v", at, s.Pattern, err))
		} else if !re.MatchString(str) {
			errs = append(errs, fmt.Sprintf("%s: %q does not match %s", at, str, s.Pattern))
		}
	}
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		if s.Minimum != nil && f < *s.Minimum {
			errs = append(errs, fmt.Sprintf("%s: %s is below the minimum %v", at, n, *s.Minimum))
		}
		if s.Maximum != nil && f > *s.Maximum {
			errs = append(errs, fmt.Sprintf("%s: %s is above the maximum %v", at, n, *s.Maximum))
		}
	}
	switch v := v.(type) {
	case map[string]any:
		for _, name := range s.Required {
			if _, ok := v[name]; !ok {
				errs = append(errs, fmt.Sprintf("%s: required property %q is missing", at, name))
			}
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := s.Properties[k]; ok {
				errs = append(errs, api.validate(ps, v[k], at+"."+k)...)
			} else if s.closed() {
				errs = append(errs, fmt.Sprintf("%s: property %q is not allowed (additionalProperties is false)", at, k))
			}
		}
	case []any:
		if s.Items != nil {
			for i, item := range v {
				errs = append(errs, api.validate(s.Items, item, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return errs
}

func describe(v any) string {
	if v == nil {
		return "null"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// validateJSON validates raw JSON against schema s.
func (api *openAPI) validateJSON(s *schema, data []byte) []string {
	v, err := decodeValue(data)
	if err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	return api.validate(s, v, "$")
}

// TestSchemaValidatorRejects keeps the validator honest: each payload breaks the spec in one way.
func TestSchemaValidatorRejects(t *testing.T) {
	api := loadSpec(t)
	ref := func(name string) *schema { return &schema{Ref: refPrefix + name} }
	cases := []struct {
		name, schema, payload, want string
	}{
		{"missing required", "ReminderList", `{"id":"L1","account":"iCloud"}`, `required property "title"`},
		{"extra property", "ReminderList", `{"id":"L1","title":"T","account":"A","colour":"#FFFFFF"}`, `"colour" is not allowed`},
		{"null for an omitted field", "Reminder", `{"id":"R","title":"T","listID":"L","listTitle":"L","isCompleted":false,"priority":0,"dueAllDay":false,"due":null}`, "not of type"},
		{"string for integer", "Reminder", `{"id":"R","title":"T","listID":"L","listTitle":"L","isCompleted":false,"priority":"high","dueAllDay":false}`, "not of type [integer]"},
		{"fraction for integer", "Reminder", `{"id":"R","title":"T","listID":"L","listTitle":"L","isCompleted":false,"priority":1.5,"dueAllDay":false}`, "not of type [integer]"},
		{"above maximum", "Reminder", `{"id":"R","title":"T","listID":"L","listTitle":"L","isCompleted":false,"priority":12,"dueAllDay":false}`, "above the maximum"},
		{"pattern", "Calendar", `{"id":"C","title":"T","account":"A","color":"#1badf8"}`, "does not match"},
		{"array expected", "ListsResponse", `{"lists":{}}`, "not of type [array]"},
		{"items checked through $ref", "NoteFoldersResponse", `{"folders":[{"id":"F","name":"N","writable":true}]}`, `$.folders[0]: required property "account"`},
		{"boolean expected", "Calendar", `{"id":"C","title":"T","account":"A","writable":"yes"}`, "not of type [boolean]"},
		// Request bodies.
		{"create without a required field", "ReminderCreate", `{"title":"Milk"}`, `required property "list"`},
		{"unknown field in a body", "ReminderUpdate", `{"completed":true}`, `"completed" is not allowed`},
		{"null where only a value may go", "ReminderUpdate", `{"title":null}`, "$.title: null is not of type [string]"},
		{"wrong type in a type array", "EventUpdate", `{"location":5}`, "$.location: 5 is not of type [string null]"},
		{"priority out of range in a body", "ReminderCreate", `{"title":"T","list":"L","priority":10}`, "above the maximum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := api.validateJSON(ref(c.schema), []byte(c.payload))
			if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
				t.Errorf("want an error containing %q, got %q", c.want, errs)
			}
		})
	}

	t.Run("const", func(t *testing.T) {
		op, _, _ := api.operation("GET", "/health")
		s := op.Responses["200"].Content["text/plain"].Schema
		if errs := api.validate(s, "ok", "$"); len(errs) > 0 {
			t.Errorf("ok should pass: %v", errs)
		}
		if errs := api.validate(s, "nope", "$"); len(errs) == 0 {
			t.Error("const should reject nope")
		}
	})
	t.Run("enum", func(t *testing.T) {
		_, params, _ := api.operation("GET", "/api/reminders")
		s := params["query:due"].Schema
		if errs := api.validate(s, "today", "$"); len(errs) > 0 {
			t.Errorf("today should pass: %v", errs)
		}
		if errs := api.validate(s, "tomorrow", "$"); len(errs) == 0 {
			t.Error("enum should reject tomorrow")
		}
	})
	t.Run("unsupported keyword", func(t *testing.T) {
		var s schema
		if err := json.Unmarshal([]byte(`{"oneOf":[{"type":"string"}]}`), &s); err != nil {
			t.Fatal(err)
		}
		if errs := api.validate(&s, "x", "$"); len(errs) == 0 || !strings.Contains(errs[0], "oneOf") {
			t.Errorf("want a refusal naming oneOf, got %v", errs)
		}
	})
	t.Run("valid payloads pass", func(t *testing.T) {
		for name, payload := range map[string]string{
			"Reminder":       `{"id":"R","title":"T","listID":"L","listTitle":"L","isCompleted":false,"priority":9,"dueAllDay":true,"due":"2026-11-02"}`,
			"ReminderUpdate": `{"due":null,"notes":"n","isCompleted":true}`,
			"EventUpdate":    `{"location":null,"notes":null,"url":null}`,
			"NoteUpdate":     `{}`,
		} {
			if errs := api.validateJSON(ref(name), []byte(payload)); len(errs) > 0 {
				t.Errorf("%s: unexpected errors: %v", name, errs)
			}
		}
	})
	t.Run("request body schema found", func(t *testing.T) {
		op, _, ok := api.operation("PATCH", "/api/reminders/{id}")
		if !ok || op.bodySchema() == nil {
			t.Fatal("PATCH /api/reminders/{id} should have a JSON request body")
		}
		if errs := api.validateJSON(op.bodySchema(), []byte(`{"due":null}`)); len(errs) > 0 {
			t.Errorf("unexpected errors: %v", errs)
		}
		if errs := api.validateJSON(op.bodySchema(), []byte(`{"due":false}`)); len(errs) == 0 {
			t.Error("false is no due date")
		}
	})
	t.Run("path templates match", func(t *testing.T) {
		_, _, tmpl, vals, ok := api.match("PATCH", "/api/reminders/a%2Fb%20c")
		if !ok || tmpl != "/api/reminders/{id}" || vals["id"] != "a/b c" {
			t.Errorf("got %v %q %v", ok, tmpl, vals)
		}
		if _, _, tmpl, _, ok := api.match("GET", "/api/notes/folders"); !ok || tmpl != "/api/notes/folders" {
			t.Errorf("literal path: got %v %q", ok, tmpl)
		}
		if _, _, _, _, ok := api.match("DELETE", "/api/reminders/R1"); ok {
			t.Error("the spec defines no DELETE")
		}
	})
}

// TestSpecUsesOnlyValidatedKeywords: every schema in the spec is one the validator fully understands.
func TestSpecUsesOnlyValidatedKeywords(t *testing.T) {
	api := loadSpec(t)
	var walk func(s *schema, at string)
	walk = func(s *schema, at string) {
		if s == nil {
			return
		}
		if len(s.unsupported) > 0 {
			t.Errorf("%s uses %v, which schema_test.go's validator doesn't implement", at, s.unsupported)
		}
		for name, p := range s.Properties {
			walk(p, at+".properties."+name)
		}
		walk(s.Items, at+".items")
	}
	for name, s := range api.Components.Schemas {
		walk(s, "components.schemas."+name)
	}
	for path, item := range api.Paths {
		for _, method := range []string{"GET", "PUT", "POST", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			op := item.operation(method)
			if op == nil {
				continue
			}
			for _, p := range append(slices.Clone(item.Parameters), op.Parameters...) {
				walk(p.Schema, fmt.Sprintf("%s %s parameter %s", method, path, p.Name))
			}
			if op.RequestBody != nil {
				for media, c := range op.RequestBody.Content {
					walk(c.Schema, fmt.Sprintf("%s %s request body %s", method, path, media))
				}
			}
			for status, r := range op.Responses {
				for media, c := range r.Content {
					walk(c.Schema, fmt.Sprintf("%s %s %s %s", method, path, status, media))
				}
			}
		}
	}
}
