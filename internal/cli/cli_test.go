package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/filiprojek/dockup/internal/config"
	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/notify"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updates"
	"github.com/filiprojek/dockup/internal/version"
)

const (
	dOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

type fakeReg map[string]string

func (f fakeReg) Digest(_ context.Context, ref string) (string, error) {
	if d, ok := f[ref]; ok {
		return d, nil
	}
	return "", errors.New("not found")
}

type fakeRunner struct {
	calls  []string
	failOn string            // fail commands containing this
	hook   func(args string) // e.g. swap a container's image on "up"
}

func (f *fakeRunner) Run(_ context.Context, args []string, out io.Writer) error {
	j := strings.Join(args, " ")
	f.calls = append(f.calls, j)
	io.WriteString(out, "compose output\n")
	if f.hook != nil {
		f.hook(j)
	}
	if f.failOn != "" && strings.Contains(j, f.failOn) {
		io.WriteString(out, "Error response from daemon: cannot stop container\n")
		return errors.New("exit status 1")
	}
	return nil
}

type fakeBinder map[string]error

func (f fakeBinder) Bind(p ports.Proto, ip netip.Addr, port uint16) error {
	return f[fmt.Sprintf("%s/%s", p, netip.AddrPortFrom(ip, port))]
}

type fakeSender struct{ got []notify.Message }

func (f *fakeSender) Send(_ context.Context, m notify.Message) error {
	f.got = append(f.got, m)
	return nil
}

type env struct {
	docker *dockerx.Fake
	deps   *Deps
	runner *fakeRunner
	sender *fakeSender
	home   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	root, _ := filepath.Abs("testdata/srv")
	wd := filepath.Join(root, "web")
	lbl := func(svc string) map[string]string {
		return map[string]string{dockerx.LabelProject: "web", dockerx.LabelService: svc, dockerx.LabelWorkingDir: wd}
	}
	docker := &dockerx.Fake{
		List: []dockerx.Container{
			{ID: "c1", Name: "web-app-1", Image: "ghcr.io/acme/web:latest", ImageID: "sha256:web", State: "running", Labels: lbl("app"),
				Ports: []dockerx.Port{{IP: netip.MustParseAddr("0.0.0.0"), Private: 80, Public: 8080, Proto: "tcp"}}},
			{ID: "c2", Name: "web-cache-1", Image: "redis:7", ImageID: "sha256:redis", State: "running", Health: "unhealthy", Labels: lbl("cache")},
		},
		Images: map[string]dockerx.Image{
			"ghcr.io/acme/web:latest": {ID: "sha256:web", RepoDigests: []string{"ghcr.io/acme/web@" + dOld}},
			"redis:7":                 {ID: "sha256:redis", RepoDigests: []string{"redis@" + dOld}},
		},
	}

	procNet := t.TempDir()
	os.WriteFile(filepath.Join(procNet, "tcp"), []byte(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 11 1 0 100 0 0 10 0\n"+
			"   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12 1 0 100 0 0 10 0\n"), 0o644)

	e := &env{runner: &fakeRunner{}, sender: &fakeSender{}, home: home, docker: docker}
	e.deps = &Deps{
		Docker:   docker,
		Registry: fakeReg{"ghcr.io/acme/web:latest": dNew, "redis:7": dOld, "gitea/gitea:1.24": dOld},
		Runner:   e.runner,
		Binder:   fakeBinder{"tcp/0.0.0.0:7000": syscall.EADDRINUSE},
		ProcNet:  procNet,
		ProcRoot: t.TempDir(),
		Ifaces:   func() ([]ports.Iface, error) { return nil, nil },
		Now:      func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) },
		Senders:  func(config.Notify) []notify.Sender { return []notify.Sender{e.sender} },
		Stdin:    strings.NewReader(""),
		roots:    []string{root},
	}
	return e
}

func (e *env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	e.deps.Stdout, e.deps.Stderr = &out, &out
	cmd := NewRoot(e.deps)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func (e *env) cachePath() string { return filepath.Join(e.home, ".cache", "dockup", "updates.json") }

func TestDashboardDefault(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2/2 running", "1 unhealthy", "never checked"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCheckSavesCacheAndDashboardUsesIt(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "check")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "outdated") || !strings.Contains(out, "app") {
		t.Errorf("check output:\n%s", out)
	}
	rep, _ := updates.Load(e.cachePath())
	if rep.Find("web", "app").Status != updates.StatusOutdated {
		t.Errorf("cache: %+v", rep)
	}
	if len(e.sender.got) != 0 {
		t.Error("notify sent without --notify")
	}

	out, _ = e.run(t)
	if !strings.Contains(out, "1 pending in 1 project") || !strings.Contains(out, "web  app") {
		t.Errorf("dashboard after check:\n%s", out)
	}
}

func TestCheckCachedDoesNotQueryRegistry(t *testing.T) {
	e := newEnv(t)
	e.run(t, "check")
	e.deps.Registry = fakeReg{} // would error on every lookup
	out, err := e.run(t, "check", "--cached")
	if err != nil || !strings.Contains(out, "outdated") {
		t.Errorf("cached check: %v\n%s", err, out)
	}
}

func TestCheckNotify(t *testing.T) {
	e := newEnv(t)
	if _, err := e.run(t, "check", "--notify"); err != nil {
		t.Fatal(err)
	}
	if len(e.sender.got) != 1 || !strings.Contains(e.sender.got[0].Body, "web: app") {
		t.Errorf("notifications %+v", e.sender.got)
	}
}

func TestJSONOutput(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Summary struct{ Containers, Unhealthy int }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Summary.Containers != 2 || v.Summary.Unhealthy != 1 {
		t.Errorf("json %v: %s", err, out)
	}

	out, _ = e.run(t, "status", "--json")
	var st struct {
		Projects []struct{ Name string }
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Projects) != 2 {
		t.Errorf("status json %v: %s", err, out)
	}
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gitea", "stopped", "web", "running", "cache", "unhealthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestUpdateDryRun(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "update", "gitea", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.runner.calls) != 0 {
		t.Errorf("dry-run ran %v", e.runner.calls)
	}
	if !strings.Contains(out, "-p gitea pull") || !strings.Contains(out, "-p gitea up -d") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUpdateConfirmDeclined(t *testing.T) {
	e := newEnv(t)
	e.deps.Stdin = strings.NewReader("n\n")
	out, err := e.run(t, "update", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.runner.calls) != 0 || !strings.Contains(out, "Aborted") {
		t.Errorf("calls %v\n%s", e.runner.calls, out)
	}
}

func TestUpdateConfirmedRunsAndLogs(t *testing.T) {
	e := newEnv(t)
	e.deps.Stdin = strings.NewReader("y\n")
	if _, err := e.run(t, "update", "web/app"); err != nil {
		t.Fatal(err)
	}
	if len(e.runner.calls) != 2 || !strings.HasSuffix(e.runner.calls[1], "up -d app") {
		t.Errorf("calls %v", e.runner.calls)
	}
	log, err := os.ReadFile(filepath.Join(e.home, ".local", "state", "dockup", "dockup.log"))
	if err != nil || !strings.Contains(string(log), "web ok") {
		t.Errorf("log %v: %s", err, log)
	}
}

func TestUpdateAllUsesPendingProjects(t *testing.T) {
	e := newEnv(t)
	e.run(t, "check")
	if _, err := e.run(t, "update", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	// only "web" has updates; gitea is up to date / not pulled
	if len(e.runner.calls) != 3 || !strings.Contains(e.runner.calls[0], "-p web pull") {
		t.Errorf("calls %v", e.runner.calls)
	}
}

func TestUpdateNothingToDo(t *testing.T) {
	e := newEnv(t)
	e.deps.Registry = fakeReg{"ghcr.io/acme/web:latest": dOld, "redis:7": dOld}
	out, err := e.run(t, "update", "--all", "--yes")
	if err != nil || !strings.Contains(out, "Nothing to update") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestUpdateRequiresTargets(t *testing.T) {
	e := newEnv(t)
	if _, err := e.run(t, "update"); err == nil {
		t.Error("expected error without targets")
	}
	if _, err := e.run(t, "update", "nope", "--yes"); err == nil {
		t.Error("expected error for unknown project")
	}
}

func TestPorts(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "ports")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "8080") || !strings.Contains(out, "web/app") {
		t.Errorf("ports:\n%s", out)
	}
	out, _ = e.run(t, "ports", "--project", "web", "--json")
	var rows []ports.Binding
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0].Port != 8080 {
		t.Errorf("filtered json %v: %s", err, out)
	}
}

func TestPortCheck(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "port-check", "9000")
	if err != nil || !strings.Contains(out, "FREE") {
		t.Errorf("%v\n%s", err, out)
	}

	out, err = e.run(t, "port-check", "8080,3000,7000")
	var ec ExitError
	if !errors.As(err, &ec) || ec.Code != 1 {
		t.Errorf("busy ports should exit 1, got %v", err)
	}
	for _, want := range []string{"tcp/8080", "IN USE", "web/app", "RESERVED", "gitea/server", "tcp/7000"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	// unknown (privileged, can't verify) alone is not a failure
	e.deps.Binder = fakeBinder{"tcp/0.0.0.0:443": syscall.EACCES, "tcp/[::]:443": syscall.EACCES}
	if out, err := e.run(t, "port-check", "443", "--proto", "tcp"); err != nil || !strings.Contains(out, "UNKNOWN") {
		t.Errorf("unknown should exit 0: %v\n%s", err, out)
	}

	out, _ = e.run(t, "port-check", "22", "--proto", "both", "--json")
	var rs []ports.Result
	if err := json.Unmarshal([]byte(out), &rs); err != nil || len(rs) != 2 || rs[0].Status != ports.StatusInUse || rs[1].Status != ports.StatusFree {
		t.Errorf("json %v: %s", err, out)
	}
}

func TestPortCheckFree(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "port-check", "--free", "8080-8090")
	if err != nil || strings.TrimSpace(out) != "8081" {
		t.Errorf("%v %q", err, out)
	}
	_, err = e.run(t, "port-check", "--free", "8080-8080")
	if err == nil {
		t.Error("expected error when range exhausted")
	}
}

func TestRootOverrideAndConfigFlag(t *testing.T) {
	e := newEnv(t)
	cfg := filepath.Join(t.TempDir(), "c.yml")
	os.WriteFile(cfg, []byte("ignore: [web/app]\n"), 0o644)
	e.run(t, "--config", cfg, "check")
	rep, _ := updates.Load(e.cachePath())
	if r := rep.Find("web", "app"); r.Status != updates.StatusSkipped {
		t.Errorf("config ignore not applied: %+v", r)
	}
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	e.deps.Build = version.Info{Version: "v0.1.0", Commit: "a1b2c3d", Date: "2026-10-31T12:00:00Z", Go: "go1.27.3", Platform: "linux/amd64"}
	want := "dockup v0.1.0 (commit a1b2c3d, built 2026-10-31T12:00:00Z, go1.27.3, linux/amd64)\n"

	for _, args := range [][]string{{"--version"}, {"version"}} {
		out, err := e.run(t, args...)
		if err != nil || out != want {
			t.Errorf("%v: %v %q", args, err, out)
		}
	}

	out, err := e.run(t, "version", "--json")
	var v version.Info
	if err != nil || json.Unmarshal([]byte(out), &v) != nil || v != e.deps.Build {
		t.Errorf("json %v: %s", err, out)
	}
}

func TestPortsManagedAndGrouped(t *testing.T) {
	e := newEnv(t)

	out, err := e.run(t, "ports", "--managed")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "8080") || strings.Contains(out, " 22 ") {
		t.Errorf("--managed should keep web:8080 and drop sshd:22:\n%s", out)
	}

	out, err = e.run(t, "ports", "--group")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gitea", "stopped", "3000", "reserved", "web", "app", "8080", "other", "22"} {
		if !strings.Contains(out, want) {
			t.Errorf("--group missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "gitea") > strings.Index(out, "web") || strings.Index(out, "other") < strings.Index(out, "web") {
		t.Errorf("group order wrong:\n%s", out)
	}

	out, _ = e.run(t, "ports", "--group", "--managed", "--json")
	var gs []ports.Group
	if err := json.Unmarshal([]byte(out), &gs); err != nil || len(gs) != 2 || gs[0].Project != "gitea" || gs[1].Project != "web" {
		t.Errorf("json %v: %s", err, out)
	}
}

// newImageOnUp makes "up" recreate web/app on a new image.
func (e *env) newImageOnUp() {
	e.runner.hook = func(args string) {
		if strings.Contains(args, " up -d") {
			e.docker.List[0].ImageID = "sha256:web2"
		}
	}
}

func TestUpdatePrintsRecap(t *testing.T) {
	e := newEnv(t)
	e.newImageOnUp()
	out, err := e.run(t, "update", "web", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UPDATE RECAP", "pull ✓ down ✓ up ✓", "2/2 running", "updated: app", "1 ok · 0 partial · 0 failed · 1 service updated"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUpdateRecapAfterFailedDown(t *testing.T) {
	e := newEnv(t)
	e.runner.failOn = "-p web down"
	out, err := e.run(t, "update", "web", "--yes")
	if err == nil || !strings.Contains(err.Error(), "1 of 1 projects failed") {
		t.Fatalf("err %v", err)
	}
	if n := len(e.runner.calls); n != 3 {
		t.Errorf("up -d not run after failed down: %v", e.runner.calls)
	}
	for _, want := range []string{"partial", "down ✗ up ✓", "✗ down: Error response from daemon: cannot stop container"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestUpdateJSON(t *testing.T) {
	e := newEnv(t)
	e.newImageOnUp()
	var stdout, stderr bytes.Buffer
	e.deps.Stdout, e.deps.Stderr = &stdout, &stderr
	cmd := NewRoot(e.deps)
	cmd.SetArgs([]string{"update", "web", "--yes", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Projects []struct {
			Project, Status string
			Updated         []string
		}
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Projects) != 1 || got.Projects[0].Status != "ok" || !reflect.DeepEqual(got.Projects[0].Updated, []string{"app"}) {
		t.Errorf("recap %+v", got)
	}
	if !strings.Contains(stderr.String(), "compose output") {
		t.Errorf("compose output should go to stderr, got %q", stderr.String())
	}
}
