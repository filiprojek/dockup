package ports

import (
	"cmp"
	"net/netip"
	"slices"
)

// Ref identifies a container.
type Ref struct {
	ContainerID string `json:"container_id,omitempty"`
	Container   string `json:"container,omitempty"`
	Project     string `json:"project,omitempty"`
	Service     string `json:"service,omitempty"`
}

// Published is a port Docker publishes for a container (from the Docker API).
type Published struct {
	Proto Proto
	IP    netip.Addr
	Port  uint16
	Ref
}

type Source string

const (
	SourceKernel Source = "kernel" // a real socket exists
	SourceDocker Source = "docker" // published by Docker with no socket (userland-proxy off)
)

// Binding is one row of the port table.
type Binding struct {
	Proto   Proto      `json:"proto"`
	IP      netip.Addr `json:"ip"`
	Port    uint16     `json:"port"`
	Iface   string     `json:"iface"`
	Source  Source     `json:"source"`
	PID     int        `json:"pid,omitempty"`
	Process string     `json:"process,omitempty"`
	Ref
}

// BuildTable merges kernel sockets, their owners and Docker's published ports
// into one sorted table. byID resolves container IDs found via cgroups
// (host-network containers) to names.
func BuildTable(sockets []Socket, owners map[uint64]Owner, published []Published, byID map[string]Ref, ifaces []Iface) []Binding {
	used := make([]bool, len(published))
	var out []Binding
	for _, s := range sockets {
		b := Binding{Proto: s.Proto, IP: s.IP, Port: s.Port, Iface: IfaceFor(s.IP, ifaces), Source: SourceKernel}
		if o, ok := owners[s.Inode]; ok {
			b.PID, b.Process = o.PID, o.Process
			if r, ok := byID[o.ContainerID]; ok && o.ContainerID != "" {
				b.Ref = r
			}
		}
		for i, p := range published {
			if p.Proto == s.Proto && p.Port == s.Port && p.IP == s.IP {
				used[i] = true
				if b.Container == "" {
					b.Ref = p.Ref
				}
			}
		}
		out = append(out, b)
	}
	for i, p := range published {
		if !used[i] {
			out = append(out, Binding{Proto: p.Proto, IP: p.IP, Port: p.Port, Iface: IfaceFor(p.IP, ifaces), Source: SourceDocker, Ref: p.Ref})
		}
	}
	slices.SortStableFunc(out, func(a, b Binding) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Proto, b.Proto), a.IP.Compare(b.IP),
			cmp.Compare(a.PID, b.PID), cmp.Compare(a.Container, b.Container))
	})
	// SO_REUSEPORT and multi-threaded servers open identical sockets
	return slices.Compact(out)
}
