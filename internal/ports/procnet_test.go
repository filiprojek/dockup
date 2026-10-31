package ports

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseProcNetTCP4(t *testing.T) {
	f := openFixture(t, "tcp")
	got, err := ParseProcNet(f, TCP)
	if err != nil {
		t.Fatal(err)
	}
	want := []Socket{
		{Proto: TCP, IP: netip.MustParseAddr("127.0.0.1"), Port: 57343, Inode: 2536643, UID: 1000},
		{Proto: TCP, IP: netip.MustParseAddr("0.0.0.0"), Port: 22, Inode: 1111},
		{Proto: TCP, IP: netip.MustParseAddr("10.8.0.1"), Port: 8080, Inode: 2222},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseProcNetTCP6(t *testing.T) {
	got, err := ParseProcNet(openFixture(t, "tcp6"), TCP)
	if err != nil {
		t.Fatal(err)
	}
	want := []Socket{
		{Proto: TCP, IP: netip.MustParseAddr("::"), Port: 22, Inode: 4444},
		{Proto: TCP, IP: netip.MustParseAddr("::1"), Port: 631, Inode: 5555},
		// v4-mapped addresses are unmapped so they compare equal to plain v4
		{Proto: TCP, IP: netip.MustParseAddr("127.0.0.1"), Port: 9090, Inode: 6666},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseProcNetUDPOnlyUnconnected(t *testing.T) {
	got, err := ParseProcNet(openFixture(t, "udp"), UDP)
	if err != nil {
		t.Fatal(err)
	}
	want := []Socket{
		{Proto: UDP, IP: netip.MustParseAddr("0.0.0.0"), Port: 49200, Inode: 8355},
		{Proto: UDP, IP: netip.MustParseAddr("192.168.122.1"), Port: 53, Inode: 6306},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseProcNetMalformed(t *testing.T) {
	in := "header\n   0: ZZZZ:0016 00000000:0000 0A 0 0 0 0 0 1\n"
	if _, err := ParseProcNet(strings.NewReader(in), TCP); err == nil {
		t.Fatal("expected error for malformed address")
	}
}

func TestReadListeningAllTables(t *testing.T) {
	got, err := ReadListening("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// 3 tcp + 3 tcp6 + 2 udp + 1 udp6
	if len(got) != 9 {
		t.Fatalf("got %d sockets: %+v", len(got), got)
	}
}

func TestReadListeningMissingV6IsOK(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile("testdata/tcp")
	os.WriteFile(filepath.Join(dir, "tcp"), data, 0o644)
	got, err := ReadListening(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
}

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
