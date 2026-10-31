package ports

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeProc builds a minimal /proc tree: pid → (comm, cgroup, socket inodes).
func fakeProc(t *testing.T, procs map[int]struct {
	comm, cgroup string
	inodes       []string
}) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		must(t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(p.comm+"\n"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "cgroup"), []byte(p.cgroup), 0o644))
		for i, ino := range p.inodes {
			must(t, os.Symlink(ino, filepath.Join(dir, "fd", strconv.Itoa(i+3))))
		}
	}
	// non-pid entries must be skipped
	must(t, os.MkdirAll(filepath.Join(root, "sys"), 0o755))
	return root
}

func TestOwnersByInode(t *testing.T) {
	root := fakeProc(t, map[int]struct {
		comm, cgroup string
		inodes       []string
	}{
		812:  {comm: "sshd", cgroup: "0::/system.slice/sshd.service\n", inodes: []string{"socket:[1111]", "socket:[4444]", "/dev/null"}},
		4021: {comm: "docker-proxy", cgroup: "0::/system.slice/docker.service\n", inodes: []string{"socket:[2222]"}},
		5100: {comm: "adguardhome", cgroup: "0::/system.slice/docker-3f2a9c1b7d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeef.scope\n", inodes: []string{"socket:[6306]"}},
	})

	owners := OwnersByInode(root)

	if o := owners[1111]; o.PID != 812 || o.Process != "sshd" || o.ContainerID != "" {
		t.Errorf("1111 -> %+v", o)
	}
	if o := owners[4444]; o.PID != 812 {
		t.Errorf("4444 -> %+v", o)
	}
	if o := owners[2222]; o.Process != "docker-proxy" {
		t.Errorf("2222 -> %+v", o)
	}
	if o := owners[6306]; o.ContainerID != "3f2a9c1b7d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeef" {
		t.Errorf("6306 -> %+v", o)
	}
	if _, ok := owners[9999]; ok {
		t.Error("unexpected owner for unknown inode")
	}
}

func TestContainerIDFromCgroup(t *testing.T) {
	id := "3f2a9c1b7d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeef"
	cases := map[string]string{
		"0::/system.slice/docker-" + id + ".scope\n": id, // cgroup v2, systemd driver
		"12:pids:/docker/" + id + "\n1:name=x:/\n":   id, // cgroup v1, cgroupfs driver
		"0::/system.slice/sshd.service\n":            "", // not a container
		"0::/user.slice/user-1000.slice\n":           "", // not a container
		"0::/docker/" + id + "/init\n":               id, // nested path
	}
	for in, want := range cases {
		if got := ContainerIDFromCgroup(in); got != want {
			t.Errorf("ContainerIDFromCgroup(%q) = %q, want %q", in, got, want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
