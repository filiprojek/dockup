package ports

import (
	"net/netip"
	"slices"
	"testing"
)

func groupFixture() ([]Binding, []Declared) {
	rows := []Binding{
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 2283, Iface: "*", Ref: Ref{Container: "immich_server", Project: "immich", Service: "immich-server"}},
		{Proto: TCP, IP: addr("::"), Port: 2283, Iface: "*", Ref: Ref{Container: "immich_server", Project: "immich", Service: "immich-server"}},
		{Proto: TCP, IP: addr("127.0.0.1"), Port: 5432, Iface: "lo", Ref: Ref{Project: "immich", Service: "database"}},
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 8096, Iface: "*", Source: SourceDocker, Ref: Ref{Project: "jellyfin", Service: "jellyfin"}},
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 22, Iface: "*", Process: "sshd", PID: 1},
		{Proto: TCP, IP: addr("::"), Port: 22, Iface: "*", Process: "sshd", PID: 1},
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 80, Iface: "*", Ref: Ref{Container: "random_nginx", Service: "random_nginx"}}, // standalone
		{Proto: UDP, IP: addr("0.0.0.0"), Port: 9999, Iface: "*"},                                                             // unknown owner
	}
	declared := []Declared{
		{Project: "gitea", Service: "server", Proto: TCP, IP: addr("0.0.0.0"), Port: 3000},
		{Project: "gitea", Service: "server", Proto: TCP, IP: addr("127.0.0.1"), Port: 2222},
	}
	return rows, declared
}

func TestGroupBindings(t *testing.T) {
	rows, declared := groupFixture()
	gs := GroupBindings(rows, declared, nil)

	var names []string
	for _, g := range gs {
		names = append(names, g.Project)
	}
	if want := []string{"gitea", "immich", "jellyfin", ""}; !slices.Equal(names, want) {
		t.Fatalf("groups %q, want %q", names, want)
	}

	gitea := gs[0]
	if !gitea.Stopped || len(gitea.Services) != 1 || len(gitea.Services[0].Entries) != 2 {
		t.Fatalf("gitea %+v", gitea)
	}
	if e := gitea.Services[0].Entries[0]; e.Port != 2222 || !e.Reserved {
		t.Errorf("gitea entries sorted by port + reserved: %+v", gitea.Services[0].Entries)
	}

	immich := gs[1]
	if immich.Stopped || len(immich.Services) != 2 || immich.Services[0].Name != "database" {
		t.Fatalf("immich %+v", immich)
	}
	srv := immich.Services[1].Entries
	if len(srv) != 1 || len(srv[0].IPs) != 2 {
		t.Errorf("v4+v6 not merged: %+v", srv)
	}
	if !gs[2].Services[0].Entries[0].NATOnly {
		t.Errorf("jellyfin should be NAT-only: %+v", gs[2])
	}

	other := gs[3]
	var svc []string
	for _, s := range other.Services {
		svc = append(svc, s.Name)
	}
	if want := []string{"?", "random_nginx", "sshd"}; !slices.Equal(svc, want) {
		t.Errorf("other services %q, want %q", svc, want)
	}
	if ssh := other.Services[2].Entries; len(ssh) != 1 || len(ssh[0].IPs) != 2 {
		t.Errorf("sshd not merged: %+v", ssh)
	}
}

func TestGroupBindingsKeepFilter(t *testing.T) {
	rows, declared := groupFixture()
	gs := GroupBindings(rows, declared, func(p string) bool { return p == "immich" || p == "gitea" })
	if len(gs) != 2 || gs[0].Project != "gitea" || gs[1].Project != "immich" {
		t.Errorf("filtered groups %+v", gs)
	}
}

func TestGroupDeclaredDoesNotDuplicateLivePort(t *testing.T) {
	// a running project's declared port is already a socket; only stopped ones are passed,
	// but if both appear, the live entry wins and the group isn't "stopped"
	rows := []Binding{{Proto: TCP, IP: addr("0.0.0.0"), Port: 3000, Ref: Ref{Project: "gitea", Service: "server"}}}
	declared := []Declared{{Project: "gitea", Service: "server", Proto: TCP, IP: addr("0.0.0.0"), Port: 3000}}
	gs := GroupBindings(rows, declared, nil)
	if len(gs) != 1 || gs[0].Stopped || len(gs[0].Services[0].Entries) != 1 || gs[0].Services[0].Entries[0].Reserved {
		t.Errorf("%+v", gs)
	}
}

func TestEntryScope(t *testing.T) {
	cases := []struct {
		e    Entry
		want string
	}{
		{Entry{IPs: addrs("0.0.0.0", "::"), Ifaces: []string{"*"}}, "all"},
		{Entry{IPs: addrs("0.0.0.0"), Ifaces: []string{"*"}}, "all (IPv4)"},
		{Entry{IPs: addrs("::"), Ifaces: []string{"*"}}, "all (IPv6)"},
		{Entry{IPs: addrs("127.0.0.1"), Ifaces: []string{"lo"}}, "127.0.0.1 (lo)"},
		{Entry{IPs: addrs("10.8.0.1", "127.0.0.1"), Ifaces: []string{"wg0", "lo"}}, "10.8.0.1 (wg0), 127.0.0.1 (lo)"},
		{Entry{IPs: addrs("172.16.0.1"), Ifaces: []string{"?"}}, "172.16.0.1"},
	}
	for _, tc := range cases {
		if got := tc.e.Scope(); got != tc.want {
			t.Errorf("Scope(%v) = %q, want %q", tc.e.IPs, got, tc.want)
		}
	}
}

func addrs(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, addr(s))
	}
	return out
}
