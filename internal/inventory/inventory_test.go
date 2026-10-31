package inventory

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/filiprojek/dockup/internal/dockerx"
)

func root(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("testdata/srv")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ctr(name, project, service, workdir, state string) dockerx.Container {
	l := map[string]string{}
	if project != "" {
		l[dockerx.LabelProject] = project
		l[dockerx.LabelService] = service
	}
	if workdir != "" {
		l[dockerx.LabelWorkingDir] = workdir
	}
	return dockerx.Container{ID: name + "-id", Name: name, State: state, Labels: l, Image: "img/" + name}
}

func build(t *testing.T, cs []dockerx.Container, ignore func(p, s string) bool) *Inventory {
	t.Helper()
	b := Builder{Docker: &dockerx.Fake{List: cs}, Roots: []string{root(t)}, Ignore: ignore}
	inv, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func immichContainers(t *testing.T) []dockerx.Container {
	wd := filepath.Join(root(t), "immich")
	unhealthy := ctr("immich_server", "immich", "immich-server", wd, "running")
	unhealthy.Health = "unhealthy"
	return []dockerx.Container{
		unhealthy,
		ctr("immich_machine_learning", "immich", "immich-machine-learning", wd, "running"),
		ctr("immich_redis", "immich", "redis", wd, "running"),
		ctr("immich_postgres", "immich", "database", wd, "running"),
		ctr("immich_old_worker", "immich", "old-worker", wd, "exited"), // orphan: not in compose
	}
}

func TestBuildGroupsByProject(t *testing.T) {
	inv := build(t, immichContainers(t), nil)

	names := []string{}
	for _, p := range inv.Projects {
		names = append(names, p.Name)
	}
	// sorted; broken listed with error; notes (no compose file) skipped
	want := []string{"broken", "builder", "gitea", "immich"}
	if len(names) != len(want) {
		t.Fatalf("projects %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("projects %v, want %v", names, want)
		}
	}

	im := inv.Project("immich")
	if im == nil || !im.Updatable() || im.Dir != filepath.Join(root(t), "immich") {
		t.Fatalf("immich %+v", im)
	}
	if len(im.Services) != 5 {
		t.Fatalf("immich services: %+v", im.Services)
	}
	srv := im.Service("immich-server")
	if srv.Image != "ghcr.io/immich-app/immich-server:v1.135.0" {
		t.Errorf(".env interpolation: image = %q", srv.Image)
	}
	if len(srv.Containers) != 1 || srv.State() != StateRunning || !srv.Unhealthy() {
		t.Errorf("immich-server %+v", srv)
	}
	if !im.Service("database").Ignored {
		t.Error("compose label dockup.ignore should mark service ignored")
	}
	if o := im.Service("old-worker"); o == nil || !o.Orphan || o.State() != StateStopped {
		t.Errorf("orphan %+v", o)
	}
}

func TestStoppedProjectFromRoots(t *testing.T) {
	inv := build(t, nil, nil)
	g := inv.Project("gitea")
	if g == nil || !g.Updatable() || g.Running() {
		t.Fatalf("gitea %+v", g)
	}
	if s := g.Service("server"); s == nil || s.State() != StateMissing || s.Image != "gitea/gitea:1.24" {
		t.Errorf("server %+v", s)
	}
}

func TestBuildFlagAndBrokenProject(t *testing.T) {
	inv := build(t, nil, nil)
	if s := inv.Project("builder").Service("app"); !s.Build {
		t.Errorf("builder app should have Build=true: %+v", s)
	}
	b := inv.Project("broken")
	if b.LoadErr == "" || b.Updatable() {
		t.Errorf("broken %+v", b)
	}
}

func TestProjectsOutsideRoots(t *testing.T) {
	cs := []dockerx.Container{
		ctr("portainer", "portainer", "portainer", "/opt/elsewhere/portainer", "running"),
		ctr("random_nginx", "", "", "", "running"),
	}
	inv := build(t, cs, nil)

	p := inv.Project("portainer")
	if p == nil || p.Updatable() || p.Service("portainer") == nil {
		t.Errorf("label-only project with missing compose dir: %+v", p)
	}
	s := inv.Project("random_nginx")
	if s == nil || !s.Standalone || s.Updatable() {
		t.Errorf("standalone %+v", s)
	}
}

func TestLabelOnlyProjectLoadsComposeFromLabels(t *testing.T) {
	// a project living outside the roots but whose compose file exists
	c := ctr("gitea-server", "elsewhere", "server", filepath.Join(root(t), "gitea"), "running")
	c.Labels[dockerx.LabelConfigFiles] = filepath.Join(root(t), "gitea", "compose.yaml")
	b := Builder{Docker: &dockerx.Fake{List: []dockerx.Container{c}}} // no roots
	inv, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := inv.Project("elsewhere")
	if p == nil || !p.Updatable() || p.Service("server").Image != "gitea/gitea:1.24" {
		t.Errorf("%+v", p)
	}
}

func TestIgnoreFuncAndContainerLabel(t *testing.T) {
	cs := immichContainers(t)
	cs[2].Labels[dockerx.LabelIgnore] = "true" // redis
	inv := build(t, cs, func(p, s string) bool { return p == "immich" && s == "immich-machine-learning" })
	im := inv.Project("immich")
	if !im.Service("redis").Ignored || !im.Service("immich-machine-learning").Ignored || im.Service("immich-server").Ignored {
		t.Errorf("ignore flags wrong: %+v", im.Services)
	}
}

func TestSummary(t *testing.T) {
	inv := build(t, immichContainers(t), nil)
	s := inv.Summary()
	if s.Containers != 5 || s.Running != 4 || s.Stopped != 1 || s.Unhealthy != 1 {
		t.Errorf("%+v", s)
	}
	if s.Projects != 4 || s.ProjectsRunning != 1 {
		t.Errorf("%+v", s)
	}
}

func TestDeclaredPortsOfStoppedProjects(t *testing.T) {
	inv := build(t, immichContainers(t), nil)
	d := inv.DeclaredStopped()

	// immich is running → excluded; gitea + builder are stopped
	want := map[string]bool{
		"gitea/server tcp 0.0.0.0:3000":   true,
		"gitea/server tcp 127.0.0.1:2222": true,
		"gitea/server udp 0.0.0.0:6000":   true,
		"gitea/server udp 0.0.0.0:6001":   true,
		"builder/app tcp 0.0.0.0:9000":    true,
	}
	if len(d) != len(want) {
		t.Fatalf("declared %+v", d)
	}
	for _, x := range d {
		k := x.Project + "/" + x.Service + " " + string(x.Proto) + " " + netip.AddrPortFrom(x.IP, x.Port).String()
		if !want[k] {
			t.Errorf("unexpected %s", k)
		}
	}
}
