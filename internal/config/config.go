// Package config loads dockup's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the full dockup configuration.
type Config struct {
	// Roots are directories whose immediate subdirectories hold compose projects.
	Roots []string `yaml:"roots" json:"roots"`
	// Ignore lists "project" or "project/service" entries excluded from update checks.
	Ignore      []string `yaml:"ignore" json:"ignore"`
	LogPath     string   `yaml:"log_path" json:"log_path"`
	CachePath   string   `yaml:"cache_path" json:"cache_path"`
	Concurrency int      `yaml:"concurrency" json:"concurrency"`
	Notify      Notify   `yaml:"notify" json:"notify"`
}

type Notify struct {
	Ntfy    *Ntfy    `yaml:"ntfy" json:"ntfy,omitempty"`
	Gotify  *Gotify  `yaml:"gotify" json:"gotify,omitempty"`
	Webhook *Webhook `yaml:"webhook" json:"webhook,omitempty"`
}

type Ntfy struct {
	URL   string `yaml:"url" json:"url"`
	Topic string `yaml:"topic" json:"topic"`
	Token string `yaml:"token" json:"-"`
}

type Gotify struct {
	URL   string `yaml:"url" json:"url"`
	Token string `yaml:"token" json:"-"`
}

type Webhook struct {
	URL string `yaml:"url" json:"url"`
}

// Default returns the built-in configuration, honouring XDG base dirs.
func Default() Config {
	home, _ := os.UserHomeDir()
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return Config{
		Roots:       []string{"/srv/docker"},
		CachePath:   filepath.Join(cache, "dockup", "updates.json"),
		LogPath:     filepath.Join(state, "dockup", "dockup.log"),
		Concurrency: 8,
	}
}

// SearchPaths returns the locations Discover checks, in priority order.
func SearchPaths() []string {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".config")
	}
	return []string{filepath.Join(cfg, "dockup", "config.yml"), "/etc/dockup/config.yml"}
}

// Load reads the config at path on top of the defaults. A missing file is an error.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// Discover loads the first existing file in paths, or defaults if none exist.
func Discover(paths []string) (Config, error) {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return Load(p)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Default(), err
		}
	}
	return Default(), nil
}

// IsIgnored reports whether the service is excluded by the Ignore list.
func (c Config) IsIgnored(project, service string) bool {
	for _, e := range c.Ignore {
		p, s, hasSvc := strings.Cut(e, "/")
		if p == project && (!hasSvc || s == service) {
			return true
		}
	}
	return false
}
