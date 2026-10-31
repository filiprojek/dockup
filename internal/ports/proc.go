package ports

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Owner is the process holding a socket.
type Owner struct {
	PID         int    `json:"pid"`
	Process     string `json:"process"`
	ContainerID string `json:"container_id,omitempty"`
}

// OwnersByInode scans procRoot (normally /proc) and maps socket inodes to the
// owning process. Processes we can't inspect (other users without root) are
// silently skipped, so their sockets stay unattributed.
func OwnersByInode(procRoot string) map[uint64]Owner {
	out := map[uint64]Owner{}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		var owner *Owner
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			ino, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
			if err != nil {
				continue
			}
			if owner == nil {
				comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
				cg, _ := os.ReadFile(filepath.Join(dir, "cgroup"))
				owner = &Owner{PID: pid, Process: strings.TrimSpace(string(comm)), ContainerID: ContainerIDFromCgroup(string(cg))}
			}
			if _, seen := out[ino]; !seen {
				out[ino] = *owner
			}
		}
	}
	return out
}

var cgroupIDRe = regexp.MustCompile(`(?:/docker[-/])([0-9a-f]{64})(?:\.scope|/|$)`)

// ContainerIDFromCgroup extracts a Docker container ID from /proc/<pid>/cgroup
// content (cgroup v1 or v2, systemd or cgroupfs driver).
func ContainerIDFromCgroup(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if m := cgroupIDRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}
