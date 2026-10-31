package ports

import (
	"net/netip"
	"testing"
)

var testIfaces = []Iface{
	{Name: "lo", Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}},
	{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.10")}},
	{Name: "wg0", Addrs: []netip.Addr{netip.MustParseAddr("10.8.0.1")}},
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestBuildTable(t *testing.T) {
	const hostNetID = "aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999"
	sockets := []Socket{
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 22, Inode: 1},
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 2283, Inode: 2}, // docker-proxy for immich
		{Proto: TCP, IP: addr("::"), Port: 2283, Inode: 3},      // docker-proxy v6
		{Proto: UDP, IP: addr("10.8.0.1"), Port: 53, Inode: 4},  // host-network container
		{Proto: TCP, IP: addr("127.0.0.1"), Port: 5432, Inode: 5},
		{Proto: TCP, IP: addr("192.168.1.10"), Port: 9100, Inode: 6}, // owner unknown (no root)
	}
	owners := map[uint64]Owner{
		1: {PID: 10, Process: "sshd"},
		2: {PID: 20, Process: "docker-proxy"},
		3: {PID: 21, Process: "docker-proxy"},
		4: {PID: 30, Process: "AdGuardHome", ContainerID: hostNetID},
		5: {PID: 40, Process: "postgres"},
	}
	published := []Published{
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 2283, Ref: Ref{ContainerID: "c1", Container: "immich_server", Project: "immich", Service: "immich-server"}},
		{Proto: TCP, IP: addr("::"), Port: 2283, Ref: Ref{ContainerID: "c1", Container: "immich_server", Project: "immich", Service: "immich-server"}},
		// userland-proxy disabled: only an iptables DNAT rule, no socket
		{Proto: TCP, IP: addr("0.0.0.0"), Port: 8096, Ref: Ref{ContainerID: "c2", Container: "jellyfin", Project: "jellyfin", Service: "jellyfin"}},
	}
	byID := map[string]Ref{hostNetID: {ContainerID: hostNetID, Container: "adguard", Project: "adguard", Service: "adguard"}}

	got := BuildTable(sockets, owners, published, byID, testIfaces)

	if len(got) != 7 {
		t.Fatalf("got %d rows: %+v", len(got), got)
	}
	// sorted by port, then proto, then ip
	wantPorts := []uint16{22, 53, 2283, 2283, 5432, 8096, 9100}
	for i, b := range got {
		if b.Port != wantPorts[i] {
			t.Fatalf("row %d port %d, want %d (rows %+v)", i, b.Port, wantPorts[i], got)
		}
	}

	ssh := got[0]
	if ssh.Process != "sshd" || ssh.Iface != "*" || ssh.Source != SourceKernel || ssh.Container != "" {
		t.Errorf("ssh row %+v", ssh)
	}
	dns := got[1]
	if dns.Container != "adguard" || dns.Iface != "wg0" || dns.Project != "adguard" {
		t.Errorf("host-network container row %+v", dns)
	}
	if got[2].Container != "immich_server" || got[2].Project != "immich" || got[2].Process != "docker-proxy" {
		t.Errorf("published row %+v", got[2])
	}
	if got[4].Iface != "lo" || got[4].Process != "postgres" {
		t.Errorf("localhost row %+v", got[4])
	}
	jf := got[5]
	if jf.Source != SourceDocker || jf.Container != "jellyfin" || jf.Process != "" {
		t.Errorf("nat-only row %+v", jf)
	}
	unk := got[6]
	if unk.Process != "" || unk.PID != 0 || unk.Iface != "eth0" {
		t.Errorf("unknown-owner row %+v", unk)
	}
}

func TestBuildTableCollapsesDuplicateSockets(t *testing.T) {
	// SO_REUSEPORT: one process, many identical sockets
	sockets := []Socket{
		{Proto: UDP, IP: addr("0.0.0.0"), Port: 5353, Inode: 1},
		{Proto: UDP, IP: addr("0.0.0.0"), Port: 5353, Inode: 2},
		{Proto: UDP, IP: addr("0.0.0.0"), Port: 5353, Inode: 3},
		{Proto: UDP, IP: addr("0.0.0.0"), Port: 5353, Inode: 4}, // other process
	}
	owners := map[uint64]Owner{1: {PID: 7, Process: "spotify"}, 2: {PID: 7, Process: "spotify"}, 3: {PID: 7, Process: "spotify"}, 4: {PID: 9, Process: "avahi"}}
	got := BuildTable(sockets, owners, nil, nil, testIfaces)
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %+v", got)
	}
}
