package render

import (
	"bytes"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/filiprojek/dockup/internal/dockerx"
	"github.com/filiprojek/dockup/internal/inventory"
	"github.com/filiprojek/dockup/internal/ports"
	"github.com/filiprojek/dockup/internal/updates"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(p, got, 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", p, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

var now = time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC)

func c(name, state, health string) dockerx.Container {
	return dockerx.Container{Name: name, State: state, Health: health}
}

func sampleInv() *inventory.Inventory {
	return &inventory.Inventory{Projects: []inventory.Project{
		{Name: "gitea", Dir: "/srv/docker/gitea", ComposeFiles: []string{"x"}, Services: []inventory.Service{
			{Name: "server", Image: "gitea/gitea:1.24"},
		}},
		{Name: "immich", Dir: "/srv/docker/immich", ComposeFiles: []string{"x"}, Services: []inventory.Service{
			{Name: "database", Image: "ghcr.io/immich-app/postgres:14", Ignored: true, Containers: []dockerx.Container{c("immich_postgres", "running", "healthy")}},
			{Name: "immich-server", Image: "ghcr.io/immich-app/immich-server:release", Containers: []dockerx.Container{c("immich_server", "running", "unhealthy")}},
			{Name: "redis", Image: "valkey/valkey:8", Containers: []dockerx.Container{c("immich_redis", "running", "")}},
		}},
		{Name: "random_nginx", Standalone: true, Services: []inventory.Service{
			{Name: "random_nginx", Image: "nginx", Containers: []dockerx.Container{c("random_nginx", "exited", "")}},
		}},
	}}
}

func sampleReport() updates.Report {
	return updates.Report{CheckedAt: now.Add(-2 * time.Hour), Results: []updates.Result{
		{Project: "gitea", Service: "server", Image: "gitea/gitea:1.24", Status: updates.StatusOutdated},
		{Project: "immich", Service: "database", Status: updates.StatusSkipped, Reason: "ignored"},
		{Project: "immich", Service: "immich-server", Status: updates.StatusOutdated},
		{Project: "immich", Service: "redis", Status: updates.StatusPendingRecreate},
		{Project: "random_nginx", Service: "random_nginx", Status: updates.StatusError, Reason: "toomanyrequests"},
	}}
}

func TestDashboard(t *testing.T) {
	var b bytes.Buffer
	Plain().Dashboard(&b, sampleInv().Summary(), sampleReport(), now)
	golden(t, "dashboard", b.Bytes())
}

func TestDashboardNeverChecked(t *testing.T) {
	var b bytes.Buffer
	Plain().Dashboard(&b, sampleInv().Summary(), updates.Report{}, now)
	golden(t, "dashboard_never", b.Bytes())
}

func TestStatus(t *testing.T) {
	var b bytes.Buffer
	Plain().Status(&b, sampleInv(), sampleReport())
	golden(t, "status", b.Bytes())
}

func TestUpdates(t *testing.T) {
	var b bytes.Buffer
	Plain().Updates(&b, sampleReport(), false, now)
	golden(t, "updates", b.Bytes())
	b.Reset()
	Plain().Updates(&b, updates.Report{CheckedAt: now, Results: []updates.Result{{Project: "a", Service: "b", Status: updates.StatusUpToDate}}}, false, now)
	golden(t, "updates_none", b.Bytes())
}

func TestPorts(t *testing.T) {
	a := netip.MustParseAddr
	rows := []ports.Binding{
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 22, Iface: "*", Source: ports.SourceKernel, PID: 812, Process: "sshd"},
		{Proto: ports.UDP, IP: a("10.8.0.1"), Port: 53, Iface: "wg0", Source: ports.SourceKernel, PID: 900, Process: "AdGuardHome", Ref: ports.Ref{Container: "adguard", Project: "adguard", Service: "adguard"}},
		{Proto: ports.TCP, IP: a("::"), Port: 2283, Iface: "*", Source: ports.SourceKernel, Process: "docker-proxy", Ref: ports.Ref{Container: "immich_server", Project: "immich", Service: "immich-server"}},
		{Proto: ports.TCP, IP: a("127.0.0.1"), Port: 5432, Iface: "lo", Source: ports.SourceKernel},
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 8096, Iface: "*", Source: ports.SourceDocker, Ref: ports.Ref{Container: "jellyfin", Project: "jellyfin", Service: "jellyfin"}},
	}
	var b bytes.Buffer
	Plain().Ports(&b, rows)
	golden(t, "ports", b.Bytes())
}

func TestPortCheck(t *testing.T) {
	a := netip.MustParseAddr
	rs := []ports.Result{
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 22, Status: ports.StatusInUse, Holders: []ports.Binding{{IP: a("0.0.0.0"), Process: "sshd", PID: 812}}},
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 3000, Status: ports.StatusReserved, Reserved: []ports.Declared{{Project: "gitea", Service: "server"}}},
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 443, Status: ports.StatusUnknown, Note: "privileged port: bind test needs root; no known listener"},
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 9000, Status: ports.StatusFree},
		{Proto: ports.TCP, IP: a("0.0.0.0"), Port: 7000, Status: ports.StatusInUse, Note: "bind failed: address in use by an unidentified process (try sudo for details)"},
	}
	var b bytes.Buffer
	Plain().PortCheck(&b, rs)
	golden(t, "portcheck", b.Bytes())
}

func TestAgo(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "just now",
		5 * time.Minute:  "5m ago",
		2 * time.Hour:    "2h ago",
		50 * time.Hour:   "2d ago",
	}
	for d, want := range cases {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPortGroups(t *testing.T) {
	a := netip.MustParseAddr
	gs := []ports.Group{
		{Project: "gitea", Stopped: true, Services: []ports.ServiceGroup{
			{Name: "server", Entries: []ports.Entry{
				{Proto: ports.TCP, Port: 2222, IPs: []netip.Addr{a("127.0.0.1")}, Ifaces: []string{"lo"}, Reserved: true},
				{Proto: ports.TCP, Port: 3000, IPs: []netip.Addr{a("0.0.0.0")}, Ifaces: []string{"*"}, Reserved: true},
			}},
		}},
		{Project: "immich", Services: []ports.ServiceGroup{
			{Name: "database", Entries: []ports.Entry{{Proto: ports.TCP, Port: 5432, IPs: []netip.Addr{a("127.0.0.1")}, Ifaces: []string{"lo"}}}},
			{Name: "immich-server", Entries: []ports.Entry{{Proto: ports.TCP, Port: 2283, IPs: []netip.Addr{a("0.0.0.0"), a("::")}, Ifaces: []string{"*", "*"}}}},
		}},
		{Project: "jellyfin", Services: []ports.ServiceGroup{
			{Name: "jellyfin", Entries: []ports.Entry{{Proto: ports.TCP, Port: 8096, IPs: []netip.Addr{a("0.0.0.0")}, Ifaces: []string{"*"}, NATOnly: true}}},
		}},
		{Project: "", Services: []ports.ServiceGroup{
			{Name: "sshd", Entries: []ports.Entry{{Proto: ports.TCP, Port: 22, IPs: []netip.Addr{a("0.0.0.0"), a("::")}, Ifaces: []string{"*", "*"}}}},
		}},
	}
	var b bytes.Buffer
	Plain().PortGroups(&b, gs)
	golden(t, "portgroups", b.Bytes())
}
