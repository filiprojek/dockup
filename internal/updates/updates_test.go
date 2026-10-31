package updates

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
)

const (
	dOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	dNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

type fakeDigester struct {
	mu    sync.Mutex
	m     map[string]string
	errs  map[string]error
	calls map[string]int
}

func (f *fakeDigester) Digest(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[ref]++
	if err := f.errs[ref]; err != nil {
		return "", err
	}
	return f.m[ref], nil
}

func svc(name, image string, containers ...dockerx.Container) inventory.Service {
	return inventory.Service{Name: name, Image: image, Containers: containers}
}

func running(imageID string) dockerx.Container {
	return dockerx.Container{State: "running", ImageID: imageID}
}

func fixture() (*inventory.Inventory, *dockerx.Fake, *fakeDigester) {
	inv := &inventory.Inventory{Projects: []inventory.Project{
		{Name: "app", ComposeFiles: []string{"x"}, Services: []inventory.Service{
			svc("current", "nginx:1.27", running("sha256:img-nginx")),
			svc("outdated", "ghcr.io/acme/api:latest", running("sha256:img-api")),
			svc("pending", "redis:7", running("sha256:img-redis-OLD")), // pulled, not recreated
			svc("shared", "nginx:1.27", running("sha256:img-nginx")),   // same ref as "current"
			{Name: "built", Image: "app-built", Build: true},
			svc("pinned", "postgres:16@"+dOld),
			{Name: "ignored", Image: "nginx:1.27", Ignored: true},
			svc("noimage", "never/pulled:1"),
			svc("broken", "private.example/x:1", running("sha256:img-x")),
			svc("localonly", "my-local:dev", running("sha256:img-local")),
		}},
	}}
	docker := &dockerx.Fake{Images: map[string]dockerx.Image{
		"nginx:1.27":              {ID: "sha256:img-nginx", RepoDigests: []string{"nginx@" + dOld}},
		"ghcr.io/acme/api:latest": {ID: "sha256:img-api", RepoDigests: []string{"ghcr.io/acme/api@" + dOld}},
		"redis:7":                 {ID: "sha256:img-redis-NEW", RepoDigests: []string{"redis@" + dNew}},
		"private.example/x:1":     {ID: "sha256:img-x", RepoDigests: []string{"private.example/x@" + dOld}},
		"my-local:dev":            {ID: "sha256:img-local"}, // no RepoDigests: built/tagged locally
	}}
	reg := &fakeDigester{
		m: map[string]string{
			"nginx:1.27":              dOld,
			"ghcr.io/acme/api:latest": dNew,
			"redis:7":                 dNew,
		},
		errs: map[string]error{"private.example/x:1": errors.New("401 unauthorized")},
	}
	return inv, docker, reg
}

func TestCheck(t *testing.T) {
	inv, docker, reg := fixture()
	c := Checker{Docker: docker, Registry: reg, Concurrency: 4}
	rep := c.Check(context.Background(), inv)

	want := map[string]Status{
		"current":   StatusUpToDate,
		"outdated":  StatusOutdated,
		"pending":   StatusPendingRecreate,
		"shared":    StatusUpToDate,
		"built":     StatusSkipped,
		"pinned":    StatusSkipped,
		"ignored":   StatusSkipped,
		"noimage":   StatusSkipped,
		"broken":    StatusError,
		"localonly": StatusSkipped,
	}
	if len(rep.Results) != len(want) {
		t.Fatalf("got %d results", len(rep.Results))
	}
	for _, r := range rep.Results {
		if r.Project != "app" {
			t.Errorf("%s: project %q", r.Service, r.Project)
		}
		if r.Status != want[r.Service] {
			t.Errorf("%s: %s (%s), want %s", r.Service, r.Status, r.Reason, want[r.Service])
		}
		if (r.Status == StatusSkipped || r.Status == StatusError) && r.Reason == "" {
			t.Errorf("%s: missing reason", r.Service)
		}
	}
	// results keep inventory order
	if rep.Results[0].Service != "current" || rep.Results[9].Service != "localonly" {
		t.Errorf("order: %s ... %s", rep.Results[0].Service, rep.Results[9].Service)
	}
	out := rep.Find("app", "outdated")
	if out.LocalDigest != dOld || out.RemoteDigest != dNew {
		t.Errorf("digests %+v", out)
	}
	if reg.calls["nginx:1.27"] != 1 {
		t.Errorf("same ref queried %d times, want 1", reg.calls["nginx:1.27"])
	}
	if reg.calls["postgres:16@"+dOld] != 0 {
		t.Error("pinned image must not hit registry")
	}
	if rep.CheckedAt.IsZero() {
		t.Error("CheckedAt not set")
	}
}

func TestReportQueries(t *testing.T) {
	inv, docker, reg := fixture()
	rep := Checker{Docker: docker, Registry: reg}.Check(context.Background(), inv)

	// pending-recreate counts as needing an update
	if n := len(rep.NeedsUpdate()); n != 2 {
		t.Errorf("NeedsUpdate = %d, want 2", n)
	}
	if p := rep.ProjectsNeedingUpdate(); len(p) != 1 || p[0] != "app" {
		t.Errorf("projects %v", p)
	}
	c := rep.Counts()
	if c[StatusOutdated] != 1 || c[StatusSkipped] != 5 || c[StatusError] != 1 {
		t.Errorf("counts %v", c)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "updates.json")
	rep := Report{CheckedAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC), Results: []Result{
		{Project: "app", Service: "web", Image: "nginx:1.27", Status: StatusOutdated, LocalDigest: dOld, RemoteDigest: dNew},
	}}
	if err := Save(p, rep); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CheckedAt.Equal(rep.CheckedAt) || len(got.Results) != 1 || got.Results[0] != rep.Results[0] {
		t.Errorf("round trip: %+v", got)
	}
}

func TestLoadMissingCache(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || !got.CheckedAt.IsZero() || got.Results != nil {
		t.Errorf("missing cache should be empty report: %+v %v", got, err)
	}
}
