package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The shapes Zubi's API returns, as openapi.json describes them. Fields a newer server adds are
// ignored, so an old CLI keeps working. The server omits a field it has no value for, so every
// field the spec doesn't mark required is a pointer: absent stays distinguishable from "".

type ReminderList struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Account  string  `json:"account"`
	Color    *string `json:"color"`
	Writable bool    `json:"writable"`
}

type Reminder struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Notes       *string `json:"notes"`
	ListID      string  `json:"listID"`
	ListTitle   string  `json:"listTitle"`
	IsCompleted bool    `json:"isCompleted"`
	Priority    int     `json:"priority"`
	Due         *string `json:"due"`
	DueAllDay   bool    `json:"dueAllDay"`
}

type Calendar struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Account  string  `json:"account"`
	Color    *string `json:"color"`
	Writable bool    `json:"writable"`
}

type Event struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Start         string  `json:"start"`
	End           string  `json:"end"`
	IsAllDay      bool    `json:"isAllDay"`
	Location      *string `json:"location"`
	Notes         *string `json:"notes"`
	URL           *string `json:"url"`
	CalendarID    string  `json:"calendarID"`
	CalendarTitle string  `json:"calendarTitle"`
}

type NoteFolder struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Account  string  `json:"account"`
	ParentID *string `json:"parentID"`
	Writable bool    `json:"writable"`
}

type NoteSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Modified string `json:"modified"`
	Locked   bool   `json:"locked"`
}

type Note struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	FolderID  string  `json:"folderID"`
	Created   string  `json:"created"`
	Modified  string  `json:"modified"`
	Locked    bool    `json:"locked"`
	Plaintext *string `json:"plaintext"`
	HTML      *string `json:"html"`
}

// The wrappers the list endpoints answer with, named as in the spec.

type ListsResponse struct {
	Lists []ReminderList `json:"lists"`
}

type RemindersResponse struct {
	Reminders []Reminder `json:"reminders"`
}

type CalendarsResponse struct {
	Calendars []Calendar `json:"calendars"`
}

type EventsResponse struct {
	Events []Event `json:"events"`
}

type NoteFoldersResponse struct {
	Folders []NoteFolder `json:"folders"`
}

type NoteSummariesResponse struct {
	Notes []NoteSummary `json:"notes"`
}

// KeyInfo is the key a request was made with: its name in Zubi's Settings and its access.
type KeyInfo struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

// The access a key can have. A read key can never change anything.
const (
	accessRead      = "read"
	accessReadWrite = "readWrite"
)

// Change is one message on /api/changes: something changed, so re-read what you show.
type Change struct {
	Source string `json:"source"`
	Origin string `json:"origin"`
}

// The bodies the CLI sends, named as in the spec. A field the spec requires is a value; an
// optional one is a pointer with omitempty, so "not given" is left out of the body; a field the
// spec lets you clear with null is a Nullable, which can be absent, null or a value.

type ReminderCreate struct {
	Title    string  `json:"title"`
	List     string  `json:"list"`
	Notes    *string `json:"notes,omitempty"`
	Due      *string `json:"due,omitempty"`
	Priority *int    `json:"priority,omitempty"`
}

type ReminderUpdate struct {
	Title       *string          `json:"title,omitempty"`
	List        *string          `json:"list,omitempty"`
	Notes       Nullable[string] `json:"notes,omitzero"`
	Due         Nullable[string] `json:"due,omitzero"`
	Priority    *int             `json:"priority,omitempty"`
	IsCompleted *bool            `json:"isCompleted,omitempty"`
}

type EventCreate struct {
	Title    string  `json:"title"`
	Calendar string  `json:"calendar"`
	Start    string  `json:"start"`
	End      string  `json:"end"`
	IsAllDay *bool   `json:"isAllDay,omitempty"`
	Location *string `json:"location,omitempty"`
	Notes    *string `json:"notes,omitempty"`
	URL      *string `json:"url,omitempty"`
}

type EventUpdate struct {
	Title    *string          `json:"title,omitempty"`
	Calendar *string          `json:"calendar,omitempty"`
	Start    *string          `json:"start,omitempty"`
	End      *string          `json:"end,omitempty"`
	IsAllDay *bool            `json:"isAllDay,omitempty"`
	Location Nullable[string] `json:"location,omitzero"`
	Notes    Nullable[string] `json:"notes,omitzero"`
	URL      Nullable[string] `json:"url,omitzero"`
}

type NoteCreate struct {
	Folder string  `json:"folder"`
	Title  string  `json:"title"`
	Text   *string `json:"text,omitempty"`
	HTML   *string `json:"html,omitempty"`
}

type NoteUpdate struct {
	Title *string `json:"title,omitempty"`
	Text  *string `json:"text,omitempty"`
	HTML  *string `json:"html,omitempty"`
}

// Nullable is an update field with three states: absent (the zero value, left out of the body
// under `omitzero`, so the server keeps what it has), null (clears the field), or a value.
type Nullable[T any] struct {
	Value T
	Null  bool
	Set   bool
}

func Some[T any](v T) Nullable[T] { return Nullable[T]{Value: v, Set: true} }
func Null[T any]() Nullable[T]    { return Nullable[T]{Null: true, Set: true} }

func (n Nullable[T]) IsZero() bool { return !n.Set }

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Null {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// UnmarshalJSON reads null as Null and anything else as a value. (A field absent from the JSON
// never reaches it, so it stays the zero value: absent.) The MCP server decodes tool arguments
// with it.
func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	if string(bytes.TrimSpace(data)) == "null" {
		*n = Null[T]()
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*n = Some(v)
	return nil
}

func ptr[T any](v T) *T { return &v }

// APIError carries the HTTP status, so main can turn it into a distinct exit code.
type APIError struct {
	Status  int
	Message string
	// Write is set when the request would have changed something, which changes what a 403 means.
	Write bool
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = fmt.Sprintf("Zubi answered %d %s", e.Status, http.StatusText(e.Status))
	} else {
		msg = fmt.Sprintf("%s (%d)", msg, e.Status)
	}
	switch {
	case e.Status == http.StatusUnauthorized:
		return "the API key was refused (401): check ZUBI_API_KEY or `zubi config set key`"
	case e.Status == http.StatusForbidden && e.Write:
		return msg + ". A change needs a Read & Write key (`zubi key` shows yours) and the list, calendar or " +
			"folder set to Read & Write in Zubi's Settings on that Mac (`zubi lists`, `zubi calendars` and " +
			"`zubi folders` mark what is read-only)."
	case e.Status == http.StatusForbidden:
		return msg + ". Grant it in Zubi's Settings on that Mac."
	case e.Status == http.StatusConflict:
		return msg + ". Zubi never changes a locked note; unlock it in Notes first."
	}
	return msg
}

// UnreachableError means nothing answered at the URL: Zubi stopped, wrong port, or no tunnel.
type UnreachableError struct {
	URL string
	Err error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("no Zubi answered at %s: %v", e.URL, e.Err)
}

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// NewStreamClient is for a response that stays open (watch): timeout bounds connecting and
// waiting for the response headers, never the body.
func NewStreamClient(baseURL, apiKey string, timeout time.Duration) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableCompression:    true, // events must arrive as they are sent, not when a gzip block fills
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Transport: transport},
	}
}

// request is what one run of a command sends: values for the {placeholders} in its path, its
// query, and its JSON body.
type request struct {
	Path  map[string]string
	Query url.Values
	Body  any
}

var placeholder = regexp.MustCompile(`\{([^}/]+)\}`)

// target checks req against cmd's entry in apiCommands and builds the URL. Sending anything the
// entry doesn't describe is a bug in the CLI, so it is refused before it reaches the server.
func (c *Client) target(cmd apiCommand, req request) (string, error) {
	for name := range req.Query {
		if !slices.Contains(cmd.Query, name) {
			return "", fmt.Errorf("internal error: %s sends query parameter %q, which its entry in apiCommands doesn't list", cmd.Name, name)
		}
	}
	if (cmd.Body == nil) != (req.Body == nil) ||
		(req.Body != nil && reflect.TypeOf(req.Body) != reflect.TypeOf(cmd.Body)) {
		return "", fmt.Errorf("internal error: %s sends a %T body, but its entry in apiCommands says %T", cmd.Name, req.Body, cmd.Body)
	}
	var missing error
	path := placeholder.ReplaceAllStringFunc(cmd.Path, func(m string) string {
		name := m[1 : len(m)-1]
		value, ok := req.Path[name]
		if !ok || value == "" {
			missing = fmt.Errorf("internal error: %s has no value for {%s}", cmd.Name, name)
		}
		return url.PathEscape(value)
	})
	if missing != nil {
		return "", missing
	}
	for name := range req.Path {
		if !strings.Contains(cmd.Path, "{"+name+"}") {
			return "", fmt.Errorf("internal error: %s fills {%s}, which %s doesn't have", cmd.Name, name, cmd.Path)
		}
	}
	target := c.BaseURL + path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}
	return target, nil
}

// send makes cmd's request and returns the response when it is a success; the caller closes its
// body. Any 2xx is success, not only the status the spec documents: a create that the server
// answered 200 still happened, and calling it a failure would invite a duplicate.
func (c *Client) send(ctx context.Context, cmd apiCommand, req request) (*http.Response, error) {
	target, err := c.target(cmd, req)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if req.Body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // a note's HTML goes as written, not as \u003c...
		if err := enc.Encode(req.Body); err != nil {
			return nil, err
		}
		body = bytes.NewReader(bytes.TrimSpace(buf.Bytes()))
	}
	httpReq, err := http.NewRequestWithContext(ctx, cmd.Method, target, body)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" && !cmd.Public {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", cmd.media())
	httpReq.Header.Set("User-Agent", "zubi-cli/"+version)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		var netErr net.Error
		var opErr *net.OpError
		if ctx.Err() == nil && (errors.As(err, &opErr) || (errors.As(err, &netErr) && netErr.Timeout())) {
			return nil, &UnreachableError{URL: c.BaseURL, Err: err}
		}
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(data), Write: cmd.writes()}
	}
	return resp, nil
}

// Raw sends cmd's request and returns the body untouched; --json prints exactly what the server sent.
func (c *Client) Raw(ctx context.Context, cmd apiCommand, req request) ([]byte, error) {
	resp, err := c.send(ctx, cmd, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Do sends cmd's request and decodes the body into a new value of cmd.Response's type.
func (c *Client) Do(ctx context.Context, cmd apiCommand, req request) (any, error) {
	if cmd.Response == nil {
		return nil, fmt.Errorf("internal error: %s has no JSON response to decode", cmd.Name)
	}
	body, err := c.Raw(ctx, cmd, req)
	if err != nil {
		return nil, err
	}
	out := reflect.New(reflect.TypeOf(cmd.Response)).Interface()
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("unexpected response from %s %s: %w", cmd.Method, cmd.Path, err)
	}
	return out, nil
}

// Hummingbird answers `{"error":{"message":"..."}}`; anything else is shown as plain text.
func errorMessage(body []byte) string {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error.Message != "" {
		return wrapped.Error.Message
	}
	return strings.TrimSpace(string(body))
}
