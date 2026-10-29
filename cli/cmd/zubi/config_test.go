package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func readConfig(t *testing.T, path string) Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // no Unix permission bits
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s has mode %o, want 600", path, perm)
	}
}

func TestConfigSet(t *testing.T) {
	path := isolate(t)

	code, out, errOut := runZubi(t, "", "config", "set", "url", "http://mac.local:8787/")
	if code != exitOK || !strings.Contains(out, path) {
		t.Fatalf("set url: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	assertOwnerOnly(t, path)
	if got := readConfig(t, path).URL; got != "http://mac.local:8787" {
		t.Errorf("url = %q", got)
	}

	const key = "s3cret-api-key-0123"
	code, _, errOut = runZubi(t, "  "+key+"\n", "config", "set", "key", "-")
	if code != exitOK {
		t.Fatalf("set key -: exit %d, stderr %q", code, errOut)
	}
	assertOwnerOnly(t, path)
	cfg := readConfig(t, path)
	if cfg.APIKey != key || cfg.URL != "http://mac.local:8787" {
		t.Errorf("config = %+v", cfg)
	}

	// Without a trailing newline, as `printf %s "$KEY" | zubi config set key -` sends it.
	if code, _, _ = runZubi(t, "other-key-4567", "config", "set", "key", "-"); code != exitOK {
		t.Fatalf("set key - without newline: exit %d", code)
	}
	if got := readConfig(t, path).APIKey; got != "other-key-4567" {
		t.Errorf("key = %q", got)
	}
	if code, _, _ = runZubi(t, "", "config", "set", "key", key); code != exitOK {
		t.Fatalf("set key KEY: exit %d", code)
	}

	code, out, _ = runZubi(t, "", "config")
	if code != exitOK {
		t.Fatalf("config: exit %d", code)
	}
	if strings.Contains(out, key) || !strings.Contains(out, "s3cr********0123") {
		t.Errorf("config should show the key masked: %q", out)
	}
	if !strings.Contains(out, "file  "+path) || !strings.Contains(out, "url   http://mac.local:8787") {
		t.Errorf("config: %q", out)
	}

	if code, _, _ = runZubi(t, "", "config", "unset", "key"); code != exitOK {
		t.Fatalf("unset key: exit %d", code)
	}
	if got := readConfig(t, path).APIKey; got != "" {
		t.Errorf("key after unset = %q", got)
	}
	if _, out, _ = runZubi(t, "", "config", "show"); !strings.Contains(out, "key   (not set)") {
		t.Errorf("config show after unset: %q", out)
	}
}

func TestConfigBadUsage(t *testing.T) {
	isolate(t)
	for _, c := range []struct {
		stdin string
		args  []string
	}{
		{"", []string{"config", "set", "url", "mac.local:8787"}},
		{"", []string{"config", "set", "url", "ftp://mac.local"}},
		{"\n", []string{"config", "set", "key", "-"}},
		{"", []string{"config", "set", "colour", "blue"}},
		{"", []string{"config", "set", "key"}},
		{"", []string{"config", "unset", "everything"}},
	} {
		code, _, errOut := runZubi(t, c.stdin, c.args...)
		if code != exitUsage || errOut == "" {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errOut)
		}
	}
}

func TestConfigPrecedence(t *testing.T) {
	path := isolate(t)

	ep, key, err := resolve("", "")
	if err != nil || ep.URL != defaultURL || ep.Relay != nil || key != "" {
		t.Errorf("nothing set: %+v %q %v", ep, key, err)
	}

	f := newFakeZubi(t)
	if err := saveConfig(path, Config{URL: f.URL, APIKey: "file-key"}); err != nil {
		t.Fatal(err)
	}
	expect := func(step, wantKey string, args ...string) {
		t.Helper()
		f.setKey(wantKey)
		code, _, errOut := runZubi(t, "", append([]string{"lists"}, args...)...)
		if code != exitOK {
			t.Errorf("%s: want the request to carry %s; exit %d, stderr %q", step, wantKey, code, errOut)
		}
	}
	expect("file only", "file-key")

	t.Setenv("ZUBI_API_KEY", "env-key")
	expect("env over file", "env-key")

	expect("flag over env", "flag-key", "--key", "flag-key")

	// URL: the file points at the fake; an env URL that answers nothing must win over it.
	t.Setenv("ZUBI_URL", "http://127.0.0.1:1")
	if code, _, _ := runZubi(t, "", "lists"); code != exitUnreachable {
		t.Errorf("env URL over file: exit %d, want %d", code, exitUnreachable)
	}
	expect("flag URL over env", "env-key", "--url", f.URL)

	ep, key, _ = resolve("http://flag:1", "")
	if ep.URL != "http://flag:1" || key != "env-key" {
		t.Errorf("resolve = %+v %q", ep, key)
	}
	if _, out, _ := runZubi(t, "", "config"); !strings.Contains(out, "url   http://127.0.0.1:1") || !strings.Contains(out, "key   ********\n") {
		t.Errorf("config shows the effective settings: %q", out)
	}
}

func TestMasked(t *testing.T) {
	for in, want := range map[string]string{
		"":                    "(not set)",
		"short":               "********",
		"12345678":            "********",
		"abcd-efgh-ijkl-mnop": "abcd********mnop",
	} {
		if got := masked(in); got != want {
			t.Errorf("masked(%q) = %q, want %q", in, got, want)
		}
	}
}
