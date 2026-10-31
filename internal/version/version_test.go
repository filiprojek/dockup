package version

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func bi(mainVersion string, settings map[string]string) *debug.BuildInfo {
	b := &debug.BuildInfo{GoVersion: "go1.27.3", Main: debug.Module{Version: mainVersion}}
	for k, v := range settings {
		b.Settings = append(b.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return b
}

func TestResolveFromLdflags(t *testing.T) {
	// ldflags win over build info
	got := Resolve("0.1.0", "a1b2c3d", "2026-10-31T12:00:00Z", bi("v9.9.9", map[string]string{"vcs.revision": "ffffffffffff"}))
	want := Info{Version: "v0.1.0", Commit: "a1b2c3d", Date: "2026-10-31T12:00:00Z", Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestResolveFallsBackToBuildInfo(t *testing.T) {
	// `go install github.com/filiprojek/dockup/cmd/dockup@v0.2.0`
	got := Resolve("", "", "", bi("v0.2.0", nil))
	if got.Version != "v0.2.0" || got.Commit != "" {
		t.Errorf("%+v", got)
	}

	// plain `go build` in a git checkout
	got = Resolve("dev", "", "", bi("(devel)", map[string]string{
		"vcs.revision": "0123456789abcdef", "vcs.time": "2026-10-31T10:00:00Z", "vcs.modified": "true",
	}))
	if got.Version != "dev" || got.Commit != "0123456-dirty" || got.Date != "2026-10-31T10:00:00Z" {
		t.Errorf("%+v", got)
	}
}

func TestResolveNoBuildInfo(t *testing.T) {
	got := Resolve("", "", "", nil)
	if got.Version != "dev" {
		t.Errorf("%+v", got)
	}
}

func TestSnapshotVersionKeepsSuffix(t *testing.T) {
	if got := Resolve("0.1.1-SNAPSHOT-a1b2c3d", "", "", nil).Version; got != "v0.1.1-SNAPSHOT-a1b2c3d" {
		t.Errorf("got %q", got)
	}
}

func TestString(t *testing.T) {
	i := Info{Version: "v0.1.0", Commit: "a1b2c3d", Date: "2026-10-31T12:00:00Z", Go: "go1.27.3", Platform: "linux/amd64"}
	if got, want := i.String(), "dockup v0.1.0 (commit a1b2c3d, built 2026-10-31T12:00:00Z, go1.27.3, linux/amd64)"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// unknown parts are omitted
	i = Info{Version: "dev", Go: "go1.27.3", Platform: "linux/amd64"}
	if got, want := i.String(), "dockup dev (go1.27.3, linux/amd64)"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
