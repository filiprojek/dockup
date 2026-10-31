package ports

import (
	"cmp"
	"net/netip"
	"slices"
	"strings"
)

// Entry is one port of a service, with its v4/v6 (or multi-IP) binds merged.
type Entry struct {
	Proto    Proto        `json:"proto"`
	Port     uint16       `json:"port"`
	IPs      []netip.Addr `json:"ips"`
	Ifaces   []string     `json:"ifaces"`
	Reserved bool         `json:"reserved,omitempty"` // declared by a stopped project
	NATOnly  bool         `json:"nat_only,omitempty"` // published by Docker without a socket
}

type ServiceGroup struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// Group is a compose project's ports; Project "" is the group of everything
// not owned by a compose project (host processes, standalone containers).
type Group struct {
	Project  string         `json:"project"`
	Stopped  bool           `json:"stopped,omitempty"`
	Services []ServiceGroup `json:"services"`
}

// Scope describes where an entry listens: "all", "127.0.0.1 (lo)", ...
func (e Entry) Scope() string {
	v4, v6 := false, false
	var specific []string
	for i, ip := range e.IPs {
		switch {
		case ip == netip.IPv4Unspecified():
			v4 = true
		case ip == netip.IPv6Unspecified():
			v6 = true
		default:
			s := ip.String()
			if i < len(e.Ifaces) && e.Ifaces[i] != "?" && e.Ifaces[i] != "" {
				s += " (" + e.Ifaces[i] + ")"
			}
			specific = append(specific, s)
		}
	}
	var parts []string
	switch {
	case v4 && v6:
		parts = append(parts, "all")
	case v4:
		parts = append(parts, "all (IPv4)")
	case v6:
		parts = append(parts, "all (IPv6)")
	}
	return strings.Join(append(parts, specific...), ", ")
}

// GroupBindings groups the port table by compose project and service, merging
// binds of the same port. declared (stopped projects' ports) are added as
// reserved entries. keep, if non-nil, selects which projects to show; the
// non-project group is then dropped.
func GroupBindings(rows []Binding, declared []Declared, keep func(project string) bool) []Group {
	type key struct {
		project, service string
		proto            Proto
		port             uint16
	}
	entries := map[key]*Entry{}
	live := map[string]bool{}
	var order []key

	add := func(k key, ip netip.Addr, iface string, reserved, nat bool) {
		e, ok := entries[k]
		if !ok {
			e = &Entry{Proto: k.proto, Port: k.port, Reserved: reserved, NATOnly: nat}
			entries[k] = e
			order = append(order, k)
		}
		if !slices.Contains(e.IPs, ip) {
			e.IPs = append(e.IPs, ip)
			e.Ifaces = append(e.Ifaces, iface)
		}
		e.NATOnly = e.NATOnly && nat
	}

	for _, b := range rows {
		k := key{project: b.Project, proto: b.Proto, port: b.Port}
		switch {
		case b.Project != "":
			k.service = b.Service
			live[b.Project] = true
		case b.Process != "":
			k.service = b.Process
		case b.Container != "":
			k.service = b.Container
		default:
			k.service = "?"
		}
		add(k, b.IP, b.Iface, false, b.Source == SourceDocker)
	}
	for _, d := range declared {
		k := key{d.Project, d.Service, d.Proto, d.Port}
		if live[d.Project] {
			continue // running project: its real sockets are already listed
		}
		add(k, d.IP, IfaceFor(d.IP, nil), true, false)
	}

	byProject := map[string]*Group{}
	for _, k := range order {
		if keep != nil && (k.project == "" || !keep(k.project)) {
			continue
		}
		g, ok := byProject[k.project]
		if !ok {
			g = &Group{Project: k.project, Stopped: k.project != "" && !live[k.project]}
			byProject[k.project] = g
		}
		i := slices.IndexFunc(g.Services, func(s ServiceGroup) bool { return s.Name == k.service })
		if i < 0 {
			g.Services = append(g.Services, ServiceGroup{Name: k.service})
			i = len(g.Services) - 1
		}
		g.Services[i].Entries = append(g.Services[i].Entries, *entries[k])
	}

	var out []Group
	for _, g := range byProject {
		slices.SortFunc(g.Services, func(a, b ServiceGroup) int { return cmp.Compare(a.Name, b.Name) })
		for _, s := range g.Services {
			slices.SortFunc(s.Entries, func(a, b Entry) int { return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Proto, b.Proto)) })
		}
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Group) int {
		// the non-project group goes last
		if (a.Project == "") != (b.Project == "") {
			if a.Project == "" {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Project, b.Project)
	})
	return out
}
