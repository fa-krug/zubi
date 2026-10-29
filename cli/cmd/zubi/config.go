package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const defaultURL = "http://127.0.0.1:8787"

// Config is what `zubi config set` stores. Flags and environment variables override it per run.
type Config struct {
	URL    string `json:"url,omitempty"`
	APIKey string `json:"apiKey,omitempty"`
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

// resolve applies precedence: flag, then environment, then config file, then the default.
func resolve(flagURL, flagKey string) (url, key string, err error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	url = first(flagURL, os.Getenv("ZUBI_URL"), cfg.URL, defaultURL)
	key = first(flagKey, os.Getenv("ZUBI_API_KEY"), cfg.APIKey)
	return url, key, nil
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
