package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// feedKeyParam is the query parameter the calendar feed reads the key from (the spec's queryKey
// security scheme), because calendar apps can't send an Authorization header.
const feedKeyParam = "key"

// runFeed prints the URL a calendar app subscribes to, or with --download the iCalendar itself.
func runFeed(ctx context.Context, client *Client, cmd apiCommand, req request, o options, g globals, stdout, stderr io.Writer) int {
	if o.download {
		resp, err := client.send(ctx, cmd, req)
		if err != nil {
			return fail(stderr, err)
		}
		defer resp.Body.Close()
		if _, err := io.Copy(stdout, resp.Body); err != nil {
			return fail(stderr, err)
		}
		return exitOK
	}

	// The URL names the calendar by ID, so it keeps working when the calendar is renamed; looking
	// it up also catches a typo before it ends up in a calendar app.
	calendars, _ := lookup("calendars")
	res, err := client.Do(ctx, calendars, request{})
	if err != nil {
		return fail(stderr, err)
	}
	cal, err := findCalendar(res.(*CalendarsResponse).Calendars, req.Path["id"])
	if err != nil {
		return fail(stderr, err)
	}
	feedURL, err := subscriptionURL(client, cmd, cal.ID, req.Query)
	if err != nil {
		return fail(stderr, err)
	}

	// Whether the key can write decides how loud the warning is; an older Zubi without /api/key
	// (or any other failure here) just gets the general one.
	warning := "zubi: this URL contains your API key. Give each calendar app its own read-only key " +
		"(Zubi › Settings › Keys) and pass it with --key or ZUBI_API_KEY."
	keyCmd, _ := lookup("key")
	if info, err := client.Do(ctx, keyCmd, request{}); err == nil {
		switch k := info.(*KeyInfo); k.Access {
		case accessRead:
			warning = fmt.Sprintf("zubi: this URL contains the read-only key %q; anyone who has the URL can read this calendar.", k.Name)
		case accessReadWrite:
			warning = fmt.Sprintf("zubi: this URL contains the key %q, which can also write. Create a read-only key for "+
				"calendar apps in Zubi › Settings › Keys and pass it with --key or ZUBI_API_KEY.", k.Name)
		}
	}

	if g.json {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // keep & in the URL as it is
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			URL      string   `json:"url"`
			Calendar Calendar `json:"calendar"`
		}{feedURL, cal}); err != nil {
			return fail(stderr, err)
		}
		stdout.Write(buf.Bytes())
	} else {
		fmt.Fprintln(stdout, feedURL)
	}
	fmt.Fprintln(stderr, warning)
	return exitOK
}

// subscriptionURL is the feed's URL for calendarID with the key in its query, built through the
// same checks as every other request.
func subscriptionURL(client *Client, cmd apiCommand, calendarID string, query url.Values) (string, error) {
	target, err := client.target(cmd, request{Path: map[string]string{"id": calendarID}, Query: query})
	if err != nil {
		return "", err
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(feedKeyParam, client.APIKey)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// findCalendar picks the calendar named by ID or title: an ID first, then an exact title, then
// a title that differs only in case.
func findCalendar(calendars []Calendar, name string) (Calendar, error) {
	for _, c := range calendars {
		if c.ID == name {
			return c, nil
		}
	}
	for _, exact := range []bool{true, false} {
		var found []Calendar
		for _, c := range calendars {
			if (exact && c.Title == name) || (!exact && strings.EqualFold(c.Title, name)) {
				found = append(found, c)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			var ids []string
			for _, c := range found {
				ids = append(ids, c.ID+" ("+c.Account+")")
			}
			return Calendar{}, usagef("%d calendars are called %q; give one by ID: %s", len(found), name, strings.Join(ids, ", "))
		}
	}
	return Calendar{}, &APIError{Status: 404, Message: fmt.Sprintf("No shared calendar is called %q; see `zubi calendars`", name)}
}
