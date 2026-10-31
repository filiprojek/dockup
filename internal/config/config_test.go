package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	c := Default()

	if len(c.Roots) != 1 || c.Roots[0] != "/srv/docker" {
		t.Errorf("Roots = %v, want [/srv/docker]", c.Roots)
	}
	if want := filepath.Join(home, ".cache", "dockup", "updates.json"); c.CachePath != want {
		t.Errorf("CachePath = %q, want %q", c.CachePath, want)
	}
	if want := filepath.Join(home, ".local", "state", "dockup", "dockup.log"); c.LogPath != want {
		t.Errorf("LogPath = %q, want %q", c.LogPath, want)
	}
	if c.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", c.Concurrency)
	}
}

func TestDefaultsHonourXDG(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")

	c := Default()
	if c.CachePath != "/xdg/cache/dockup/updates.json" {
		t.Errorf("CachePath = %q", c.CachePath)
	}
	if c.LogPath != "/xdg/state/dockup/dockup.log" {
		t.Errorf("LogPath = %q", c.LogPath)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, p, `
roots: [/srv/docker, /opt/stacks]
ignore: [traefik, immich/immich-machine-learning]
log_path: /var/log/dockup.log
concurrency: 3
notify:
  ntfy:
    url: https://ntfy.sh
    topic: srv-updates
    token: tk_abc
  gotify:
    url: https://gotify.example.com
    token: gt
  webhook:
    url: https://hooks.example.com/x
`)

	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Roots) != 2 || c.Roots[1] != "/opt/stacks" {
		t.Errorf("Roots = %v", c.Roots)
	}
	if len(c.Ignore) != 2 || c.Ignore[1] != "immich/immich-machine-learning" {
		t.Errorf("Ignore = %v", c.Ignore)
	}
	if c.LogPath != "/var/log/dockup.log" {
		t.Errorf("LogPath = %q", c.LogPath)
	}
	if c.Concurrency != 3 {
		t.Errorf("Concurrency = %d", c.Concurrency)
	}
	// unspecified keys keep defaults
	if c.CachePath == "" {
		t.Error("CachePath lost its default")
	}
	if c.Notify.Ntfy == nil || c.Notify.Ntfy.Topic != "srv-updates" || c.Notify.Ntfy.Token != "tk_abc" {
		t.Errorf("Ntfy = %+v", c.Notify.Ntfy)
	}
	if c.Notify.Gotify == nil || c.Notify.Gotify.Token != "gt" {
		t.Errorf("Gotify = %+v", c.Notify.Gotify)
	}
	if c.Notify.Webhook == nil || c.Notify.Webhook.URL != "https://hooks.example.com/x" {
		t.Errorf("Webhook = %+v", c.Notify.Webhook)
	}
}

func TestLoadExplicitMissingFileErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Fatal("expected error for explicit missing config")
	}
}

func TestLoadInvalidYAMLErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, p, "roots: [unterminated")
	if _, err := Load(p); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadUnknownKeyErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	writeFile(t, p, "rootz: [/x]\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown key (typo protection)")
	}
}

func TestDiscoverSearchesUserThenSystem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	sys := t.TempDir()

	// nothing anywhere: defaults, no error
	c, err := Discover([]string{filepath.Join(home, ".config", "dockup", "config.yml"), filepath.Join(sys, "config.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 8 {
		t.Errorf("expected defaults, got %+v", c)
	}

	writeFile(t, filepath.Join(sys, "config.yml"), "concurrency: 2\n")
	c, err = Discover([]string{filepath.Join(home, ".config", "dockup", "config.yml"), filepath.Join(sys, "config.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 2 {
		t.Errorf("system config not used: %d", c.Concurrency)
	}

	writeFile(t, filepath.Join(home, ".config", "dockup", "config.yml"), "concurrency: 5\n")
	c, err = Discover([]string{filepath.Join(home, ".config", "dockup", "config.yml"), filepath.Join(sys, "config.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 5 {
		t.Errorf("user config should win: %d", c.Concurrency)
	}
}

func TestIsIgnored(t *testing.T) {
	c := Config{Ignore: []string{"traefik", "immich/immich-machine-learning"}}
	cases := []struct {
		project, service string
		want             bool
	}{
		{"traefik", "traefik", true},
		{"traefik", "anything", true},
		{"immich", "immich-machine-learning", true},
		{"immich", "immich-server", false},
		{"other", "traefik", false},
	}
	for _, tc := range cases {
		if got := c.IsIgnored(tc.project, tc.service); got != tc.want {
			t.Errorf("IsIgnored(%q,%q) = %v, want %v", tc.project, tc.service, got, tc.want)
		}
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
