package ports

import (
	"net"
	"net/netip"
)

// Iface is a network interface and its addresses.
type Iface struct {
	Name  string
	Addrs []netip.Addr
}

// HostIfaces lists the host's interfaces.
func HostIfaces() ([]Iface, error) {
	nifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Iface
	for _, n := range nifs {
		addrs, err := n.Addrs()
		if err != nil {
			continue
		}
		ifc := Iface{Name: n.Name}
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				ifc.Addrs = append(ifc.Addrs, p.Addr().Unmap())
			}
		}
		out = append(out, ifc)
	}
	return out, nil
}

// IfaceFor names the interface an address belongs to: "*" for wildcard
// binds, "?" when no interface carries it.
func IfaceFor(ip netip.Addr, ifaces []Iface) string {
	if ip.IsUnspecified() {
		return "*"
	}
	if ip.IsMulticast() {
		return "multicast"
	}
	for _, ifc := range ifaces {
		for _, a := range ifc.Addrs {
			if a == ip {
				return ifc.Name
			}
		}
	}
	if ip.IsLoopback() {
		for _, ifc := range ifaces {
			for _, a := range ifc.Addrs {
				if a.IsLoopback() {
					return ifc.Name
				}
			}
		}
		return "lo"
	}
	return "?"
}
