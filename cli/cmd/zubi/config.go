package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fa-krug/zubi/cli/internal/tunnel"
)

const defaultURL = "http://127.0.0.1:8787"

// Config is what `zubi config set` and `zubi pair` store. Flags and environment variables
// override it per run.
type Config struct {
	URL    string `json:"url,omitempty"`
	APIKey string `json:"apiKey,omitempty"`
	// Relay is set by `zubi pair`: Zubi is reached through a relay rather than at URL.
	Relay *tunnel.Pairing `json:"relay,omitempty"`
}

// endpoint is where Zubi is: an address, or a Mac on a relay.
type endpoint struct {
	URL   string
	Relay *tunnel.Pairing
}

// String names the endpoint for people.
func (e endpoint) String() string {
	if e.Relay == nil {
		return e.URL
	}
	room := e.Relay.Room
	if len(room) > 8 {
		room = room[:8] + "…"
	}
	return fmt.Sprintf("the relay %s (room %s)", e.Relay.Relay, room)
}

// configPath is ~/.config/zubi/config.json on Linux, ~/Library/Application Support/zubi on macOS
// and %AppData%\zubi on Windows; ZUBI_CONFIG points elsewhere.
func configPath() (string, error) {
	if path := os.Getenv("ZUBI_CONFIG"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "zubi", "config.json"), nil
}

func loadConfig() (Config, string, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, path, nil
	}
	if err != nil {
		return Config{}, path, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, path, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return cfg, path, nil
}

// saveConfig writes owner-only, since the file holds the API key.
func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// resolve applies precedence: flag, then environment, then config file, then the default. A URL
// from a flag or the environment wins over a pairing; otherwise a pairing means the relay, and
// its key is the default key.
func resolve(flagURL, flagKey string) (ep endpoint, key string, err error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return endpoint{}, "", err
	}
	if override := first(flagURL, os.Getenv("ZUBI_URL")); override == "" && cfg.Relay != nil {
		ep = endpoint{Relay: cfg.Relay}
		return ep, first(flagKey, os.Getenv("ZUBI_API_KEY"), cfg.APIKey, cfg.Relay.Secret), nil
	}
	ep = endpoint{URL: first(flagURL, os.Getenv("ZUBI_URL"), cfg.URL, defaultURL)}
	return ep, first(flagKey, os.Getenv("ZUBI_API_KEY"), cfg.APIKey), nil
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// masked shows enough of a key to tell two apart, never enough to use it.
func masked(key string) string {
	if key == "" {
		return "(not set)"
	}
	if len(key) <= 8 {
		return "********"
	}
	return key[:4] + strings.Repeat("*", 8) + key[len(key)-4:]
}
