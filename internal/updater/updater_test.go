package updater

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/filiprojek/dockup/internal/inventory"
)

func testInv() *inventory.Inventory {
	return &inventory.Inventory{Projects: []inventory.Project{
		{Name: "immich", Dir: "/srv/docker/immich", ComposeFiles: []string{"/srv/docker/immich/docker-compose.yml", "/srv/docker/immich/docker-compose.override.yml"},
			Services: []inventory.Service{{Name: "immich-server"}, {Name: "redis"}, {Name: "database"}}},
		{Name: "gitea", Dir: "/srv/docker/gitea", ComposeFiles: []string{"/srv/docker/gitea/compose.yaml"},
			Services: []inventory.Service{{Name: "server"}}},
		{Name: "portainer", Services: []inventory.Service{{Name: "portainer"}}}, // no compose file
	}}
}

func TestParseTargets(t *testing.T) {
	got := ParseTargets([]string{"immich/redis", "gitea", "immich/immich-server", "immich/redis"})
	want := []Target{
		{Project: "immich", Services: []string{"redis", "immich-server"}},
		{Project: "gitea"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	// whole project wins over individual services
	got = ParseTargets([]string{"immich/redis", "immich"})
	if !reflect.DeepEqual(got, []Target{{Project: "immich"}}) {
		t.Errorf("got %+v", got)
	}
}

func TestBuildPlansProject(t *testing.T) {
	plans, err := BuildPlans(testInv(), []Target{{Project: "gitea"}})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"docker", "compose", "--project-directory", "/srv/docker/gitea", "-f", "/srv/docker/gitea/compose.yaml", "-p", "gitea"}
	want := [][]string{
		append(slicesClone(base), "pull"),
		append(slicesClone(base), "down"),
		append(slicesClone(base), "up", "-d"),
	}
	if len(plans) != 1 || plans[0].Project != "gitea" {
		t.Fatalf("plans %+v", plans)
	}
	for i, s := range plans[0].Steps {
		if !reflect.DeepEqual(s.Args, want[i]) {
			t.Errorf("step %d\n got %v\nwant %v", i, s.Args, want[i])
		}
	}
}

func TestBuildPlansServicesNoDown(t *testing.T) {
	plans, err := BuildPlans(testInv(), []Target{{Project: "immich", Services: []string{"redis", "immich-server"}}})
	if err != nil {
		t.Fatal(err)
	}
	steps := plans[0].Steps
	if len(steps) != 2 {
		t.Fatalf("steps %+v", steps)
	}
	if got := strings.Join(steps[0].Args[len(steps[0].Args)-3:], " "); got != "pull redis immich-server" {
		t.Errorf("pull step: %s", got)
	}
	if got := strings.Join(steps[1].Args[len(steps[1].Args)-4:], " "); got != "up -d redis immich-server" {
		t.Errorf("up step: %s", got)
	}
	// both compose files passed
	if strings.Count(strings.Join(steps[0].Args, " "), " -f ") != 2 {
		t.Errorf("compose files missing: %v", steps[0].Args)
	}
}

func TestBuildPlansErrors(t *testing.T) {
	cases := map[string][]Target{
		"unknown project": {{Project: "nope"}},
		"not updatable":   {{Project: "portainer"}},
		"unknown service": {{Project: "immich", Services: []string{"ghost"}}},
	}
	for name, tg := range cases {
		if _, err := BuildPlans(testInv(), tg); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

type fakeRunner struct {
	calls  []string
	failOn string // substring of joined args
}

func (f *fakeRunner) Run(_ context.Context, args []string, out io.Writer) error {
	j := strings.Join(args, " ")
	f.calls = append(f.calls, j)
	io.WriteString(out, "ran "+args[len(args)-1]+"\n")
	if f.failOn != "" && strings.Contains(j, f.failOn) {
		return errors.New("exit status 1")
	}
	return nil
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	return func() time.Time { t = t.Add(time.Second); return t }
}

func TestApplyRunsAllAndLogs(t *testing.T) {
	plans, _ := BuildPlans(testInv(), []Target{{Project: "immich"}, {Project: "gitea"}})
	r := &fakeRunner{}
	var out, log bytes.Buffer
	u := Updater{Runner: r, Out: &out, Log: &log, Now: fixedClock()}

	res := u.Apply(context.Background(), plans)

	if len(r.calls) != 6 {
		t.Fatalf("calls %v", r.calls)
	}
	if len(res) != 2 || res[0].Err != nil || res[1].Err != nil {
		t.Fatalf("results %+v", res)
	}
	if !strings.Contains(out.String(), "ran pull") {
		t.Errorf("runner output not streamed: %q", out.String())
	}
	l := log.String()
	if !strings.Contains(l, "2026-10-31T12:00:01Z immich start") || !strings.Contains(l, "immich ok") || !strings.Contains(l, "gitea ok") {
		t.Errorf("log:\n%s", l)
	}
}

func TestApplyStopsProjectOnFailureButContinuesOthers(t *testing.T) {
	plans, _ := BuildPlans(testInv(), []Target{{Project: "immich"}, {Project: "gitea"}})
	r := &fakeRunner{failOn: "-p immich down"}
	var log bytes.Buffer
	res := Updater{Runner: r, Out: io.Discard, Log: &log, Now: fixedClock()}.Apply(context.Background(), plans)

	// immich: pull, down(fail) → no up; gitea: 3 steps
	if len(r.calls) != 5 {
		t.Fatalf("calls %v", r.calls)
	}
	if res[0].Err == nil || res[1].Err != nil {
		t.Errorf("results %+v", res)
	}
	if !strings.Contains(log.String(), "immich FAILED") {
		t.Errorf("log:\n%s", log.String())
	}
}

func TestApplyDryRun(t *testing.T) {
	plans, _ := BuildPlans(testInv(), []Target{{Project: "gitea"}})
	r := &fakeRunner{}
	var out, log bytes.Buffer
	Updater{Runner: r, Out: &out, Log: &log, DryRun: true, Now: fixedClock()}.Apply(context.Background(), plans)

	if len(r.calls) != 0 {
		t.Errorf("dry-run executed %v", r.calls)
	}
	if !strings.Contains(out.String(), "docker compose --project-directory /srv/docker/gitea -f /srv/docker/gitea/compose.yaml -p gitea up -d") {
		t.Errorf("dry-run output:\n%s", out.String())
	}
	if log.Len() != 0 {
		t.Errorf("dry-run should not log: %q", log.String())
	}
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }
