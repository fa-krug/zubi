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

// Config is what pairing stores: the relay, the Mac's room on it and the key, from the pairing
// code. The key is the secret, so the file is readable by its owner only.
type Config struct {
	Relay *tunnel.Pairing `json:"relay,omitempty"`
}

// endpoint is where Zubi is: the Mac on a relay, through the client service's socket when that
// runs. URL is only for the tests, which talk to a fake Zubi directly (--url or ZUBI_URL).
type endpoint struct {
	URL    string
	Relay  *tunnel.Pairing
	Socket string
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

// socketPath is where the client service listens, next to the config, so only its owner reaches it.
func socketPath() (string, error) {
	config, err := configPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(config), "zubi.sock"), nil
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

// resolve finds Zubi: the pairing, through the client service's socket when it runs, with the
// pairing's key. --url and ZUBI_URL (with --key or ZUBI_API_KEY) reach a Zubi directly instead,
// which only the tests do. paired is false when there is neither.
func resolve(flagURL, flagKey string) (ep endpoint, key string, paired bool, err error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return endpoint{}, "", false, err
	}
	if override := first(flagURL, os.Getenv("ZUBI_URL")); override != "" {
		return endpoint{URL: override}, first(flagKey, os.Getenv("ZUBI_API_KEY")), true, nil
	}
	if cfg.Relay == nil {
		return endpoint{}, "", false, nil
	}
	socket, _ := socketPath()
	return endpoint{Relay: cfg.Relay, Socket: socket}, cfg.Relay.Secret, true, nil
}

func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
