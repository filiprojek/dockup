package ports

import (
	"fmt"
	"net/netip"
	"syscall"
	"testing"
)

// fakeBinder returns preset errors for "proto/ip:port"; everything else binds.
type fakeBinder struct {
	errs  map[string]error
	calls []string
}

func (f *fakeBinder) Bind(p Proto, ip netip.Addr, port uint16) error {
	k := fmt.Sprintf("%s/%s", p, netip.AddrPortFrom(ip, port))
	f.calls = append(f.calls, k)
	return f.errs[k]
}

func newChecker(b *fakeBinder) *Checker {
	return &Checker{
		Table: []Binding{
			{Proto: TCP, IP: addr("0.0.0.0"), Port: 22, Process: "sshd"},
			{Proto: TCP, IP: addr("127.0.0.1"), Port: 5432, Process: "postgres"},
			{Proto: TCP, IP: addr("10.8.0.1"), Port: 8080, Ref: Ref{Container: "vpn-web"}},
			{Proto: TCP, IP: addr("0.0.0.0"), Port: 8096, Source: SourceDocker, Ref: Ref{Container: "jellyfin"}},
			{Proto: UDP, IP: addr("0.0.0.0"), Port: 51820, Process: "wireguard"},
		},
		Declared: []Declared{
			{Proto: TCP, IP: addr("0.0.0.0"), Port: 3000, Project: "gitea", Service: "gitea"},
		},
		Binder: b,
	}
}

func TestCheckFree(t *testing.T) {
	c := newChecker(&fakeBinder{})
	r, err := c.Check(TCP, addr("0.0.0.0"), 9000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFree || len(r.Holders) != 0 {
		t.Errorf("%+v", r)
	}
}

func TestCheckWildcardHolderBlocksSpecificIP(t *testing.T) {
	c := newChecker(&fakeBinder{})
	r, _ := c.Check(TCP, addr("10.8.0.1"), 22)
	if r.Status != StatusInUse || len(r.Holders) != 1 || r.Holders[0].Process != "sshd" {
		t.Errorf("%+v", r)
	}
}

func TestCheckSpecificHolderBlocksWildcardRequest(t *testing.T) {
	c := newChecker(&fakeBinder{})
	r, _ := c.Check(TCP, addr("0.0.0.0"), 5432)
	if r.Status != StatusInUse {
		t.Errorf("%+v", r)
	}
}

func TestCheckDifferentSpecificIPsDontConflict(t *testing.T) {
	c := newChecker(&fakeBinder{})
	r, _ := c.Check(TCP, addr("192.168.1.10"), 5432)
	if r.Status != StatusFree {
		t.Errorf("127.0.0.1:5432 should not block 192.168.1.10:5432: %+v", r)
	}
}

func TestCheckProtoMatters(t *testing.T) {
	c := newChecker(&fakeBinder{})
	if r, _ := c.Check(UDP, addr("0.0.0.0"), 22); r.Status != StatusFree {
		t.Errorf("udp/22 should be free: %+v", r)
	}
	if r, _ := c.Check(TCP, addr("0.0.0.0"), 51820); r.Status != StatusFree {
		t.Errorf("tcp/51820 should be free: %+v", r)
	}
}

func TestCheckDockerNATOnlyIsInUse(t *testing.T) {
	// bind succeeds (no socket), but docker would still fail to publish
	c := newChecker(&fakeBinder{})
	r, _ := c.Check(TCP, addr("0.0.0.0"), 8096)
	if r.Status != StatusInUse || r.Holders[0].Container != "jellyfin" {
		t.Errorf("%+v", r)
	}
}

func TestCheckBindFailureWithoutKnownHolder(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/0.0.0.0:7000": syscall.EADDRINUSE}}
	c := newChecker(b)
	r, _ := c.Check(TCP, addr("0.0.0.0"), 7000)
	if r.Status != StatusInUse || r.Note == "" {
		t.Errorf("expected in-use with note: %+v", r)
	}
}

func TestCheckWildcardAlsoTestsV6(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/[::]:7001": syscall.EADDRINUSE}}
	c := newChecker(b)
	r, _ := c.Check(TCP, addr("0.0.0.0"), 7001)
	if r.Status != StatusInUse {
		t.Errorf("v6 wildcard conflict missed: %+v (calls %v)", r, b.calls)
	}
}

func TestCheckV6UnavailableIgnored(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/[::]:7002": syscall.EAFNOSUPPORT}}
	c := newChecker(b)
	if r, _ := c.Check(TCP, addr("0.0.0.0"), 7002); r.Status != StatusFree {
		t.Errorf("%+v", r)
	}
}

func TestCheckPrivilegedPortUnknown(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/0.0.0.0:443": syscall.EACCES, "tcp/[::]:443": syscall.EACCES}}
	c := newChecker(b)
	r, _ := c.Check(TCP, addr("0.0.0.0"), 443)
	if r.Status != StatusUnknown || r.Note == "" {
		t.Errorf("%+v", r)
	}
}

func TestCheckForeignIPErrors(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/172.16.0.9:9000": syscall.EADDRNOTAVAIL}}
	c := newChecker(b)
	if _, err := c.Check(TCP, addr("172.16.0.9"), 9000); err == nil {
		t.Error("expected error for IP not on this host")
	}
}

func TestCheckDeclaredByStoppedProject(t *testing.T) {
	c := newChecker(&fakeBinder{})
	r, _ := c.Check(TCP, addr("0.0.0.0"), 3000)
	if r.Status != StatusReserved || len(r.Reserved) != 1 || r.Reserved[0].Project != "gitea" {
		t.Errorf("%+v", r)
	}
}

func TestCheckAllBothProtocols(t *testing.T) {
	c := newChecker(&fakeBinder{})
	rs, err := c.CheckAll([]Proto{TCP, UDP}, addr("0.0.0.0"), []uint16{22, 51820})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 4 {
		t.Fatalf("%d results", len(rs))
	}
	want := map[string]Status{"tcp/22": StatusInUse, "udp/22": StatusFree, "tcp/51820": StatusFree, "udp/51820": StatusInUse}
	for _, r := range rs {
		k := fmt.Sprintf("%s/%d", r.Proto, r.Port)
		if r.Status != want[k] {
			t.Errorf("%s = %s, want %s", k, r.Status, want[k])
		}
	}
}

func TestFindFree(t *testing.T) {
	b := &fakeBinder{errs: map[string]error{"tcp/0.0.0.0:8097": syscall.EADDRINUSE}}
	c := newChecker(b)
	c.Table = append(c.Table, Binding{Proto: UDP, IP: addr("0.0.0.0"), Port: 8098})

	// 8096 docker, 8097 bind-busy, 8098 udp-only busy → tcp-only search returns 8098
	p, ok, err := c.FindFree([]Proto{TCP}, addr("0.0.0.0"), 8096, 8100)
	if err != nil || !ok || p != 8098 {
		t.Errorf("tcp FindFree = %d %v %v", p, ok, err)
	}
	// both protocols → 8099
	p, ok, _ = c.FindFree([]Proto{TCP, UDP}, addr("0.0.0.0"), 8096, 8100)
	if !ok || p != 8099 {
		t.Errorf("tcp+udp FindFree = %d %v", p, ok)
	}
	// reserved ports are skipped too
	p, ok, _ = c.FindFree([]Proto{TCP}, addr("0.0.0.0"), 3000, 3001)
	if !ok || p != 3001 {
		t.Errorf("reserved not skipped: %d", p)
	}
	// nothing free
	if _, ok, _ := c.FindFree([]Proto{TCP}, addr("0.0.0.0"), 8096, 8097); ok {
		t.Error("expected no free port")
	}
}
