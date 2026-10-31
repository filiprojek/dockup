package ports

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

type Status string

const (
	StatusFree     Status = "free"
	StatusInUse    Status = "in-use"
	StatusReserved Status = "reserved" // free now, but declared by a stopped compose project
	StatusUnknown  Status = "unknown"  // couldn't verify (privileged port without root)
)

// Declared is a port mapping from a compose file whose project isn't running.
type Declared struct {
	Project string     `json:"project"`
	Service string     `json:"service"`
	Proto   Proto      `json:"proto"`
	IP      netip.Addr `json:"ip"`
	Port    uint16     `json:"port"`
}

// Binder attempts a real bind to prove a port is free.
type Binder interface {
	Bind(proto Proto, ip netip.Addr, port uint16) error
}

type Result struct {
	Proto    Proto      `json:"proto"`
	IP       netip.Addr `json:"ip"`
	Port     uint16     `json:"port"`
	Status   Status     `json:"status"`
	Holders  []Binding  `json:"holders,omitempty"`
	Reserved []Declared `json:"reserved,omitempty"`
	Note     string     `json:"note,omitempty"`
}

type Checker struct {
	Table    []Binding
	Declared []Declared
	Binder   Binder
}

// overlaps reports whether binds on a and b would collide: a wildcard of
// either family collides with everything (Docker publishes both families and
// Linux [::] is dual-stack).
func overlaps(a, b netip.Addr) bool {
	return a.IsUnspecified() || b.IsUnspecified() || a == b
}

// Check determines whether proto/ip:port can be used for a new container.
func (c *Checker) Check(proto Proto, ip netip.Addr, port uint16) (Result, error) {
	r := Result{Proto: proto, IP: ip, Port: port, Status: StatusFree}
	for _, b := range c.Table {
		if b.Proto == proto && b.Port == port && overlaps(b.IP, ip) {
			r.Holders = append(r.Holders, b)
		}
	}

	bindErr, err := c.bindTest(proto, ip, port)
	if err != nil {
		return r, err
	}

	switch {
	case len(r.Holders) > 0:
		r.Status = StatusInUse
	case errors.Is(bindErr, syscall.EADDRINUSE):
		r.Status = StatusInUse
		r.Note = "bind failed: address in use by an unidentified process (try sudo for details)"
	case errors.Is(bindErr, syscall.EACCES):
		r.Status = StatusUnknown
		r.Note = "privileged port: bind test needs root; no known listener"
	}
	if r.Status != StatusFree {
		return r, nil
	}
	for _, d := range c.Declared {
		if d.Proto == proto && d.Port == port && overlaps(d.IP, ip) {
			r.Reserved = append(r.Reserved, d)
		}
	}
	if len(r.Reserved) > 0 {
		r.Status = StatusReserved
	}
	return r, nil
}

// bindTest returns the meaningful bind error (in-use / permission), or a hard
// error when the address isn't usable on this host at all.
func (c *Checker) bindTest(proto Proto, ip netip.Addr, port uint16) (error, error) {
	if c.Binder == nil {
		return nil, nil
	}
	addrs := []netip.Addr{ip}
	if ip.IsUnspecified() {
		addrs = []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()}
	}
	var result error
	for i, a := range addrs {
		err := c.Binder.Bind(proto, a, port)
		switch {
		case err == nil:
		case errors.Is(err, syscall.EADDRINUSE):
			return err, nil
		case errors.Is(err, syscall.EACCES):
			result = err
		case i > 0 && (errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL)):
			// host has no IPv6; v4 wildcard result stands
		case errors.Is(err, syscall.EADDRNOTAVAIL):
			return nil, fmt.Errorf("%s is not an address on this host", a)
		default:
			return nil, fmt.Errorf("bind %s/%s: %w", proto, netip.AddrPortFrom(a, port), err)
		}
	}
	return result, nil
}

// CheckAll checks every port for every protocol.
func (c *Checker) CheckAll(protos []Proto, ip netip.Addr, ports []uint16) ([]Result, error) {
	var out []Result
	for _, p := range ports {
		for _, pr := range protos {
			r, err := c.Check(pr, ip, p)
			if err != nil {
				return out, err
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// FindFree returns the first port in [lo, hi] free for all given protocols.
func (c *Checker) FindFree(protos []Proto, ip netip.Addr, lo, hi uint16) (uint16, bool, error) {
	for p := int(lo); p <= int(hi); p++ {
		free := true
		for _, pr := range protos {
			r, err := c.Check(pr, ip, uint16(p))
			if err != nil {
				return 0, false, err
			}
			if r.Status != StatusFree {
				free = false
				break
			}
		}
		if free {
			return uint16(p), true, nil
		}
	}
	return 0, false, nil
}
