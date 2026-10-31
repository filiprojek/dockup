package ports

import (
	"net/netip"
	"syscall"
)

// SysBinder performs real bind(2) calls without SO_REUSEADDR, then closes the
// socket immediately. Errors are raw errnos so callers can errors.Is them.
type SysBinder struct{}

func (SysBinder) Bind(proto Proto, ip netip.Addr, port uint16) error {
	family, sa := syscall.AF_INET, syscall.Sockaddr(nil)
	if ip.Is4() {
		sa = &syscall.SockaddrInet4{Port: int(port), Addr: ip.As4()}
	} else {
		family = syscall.AF_INET6
		sa = &syscall.SockaddrInet6{Port: int(port), Addr: ip.As16()}
	}
	typ := syscall.SOCK_STREAM
	if proto == UDP {
		typ = syscall.SOCK_DGRAM
	}
	fd, err := syscall.Socket(family, typ|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if family == syscall.AF_INET6 && ip.IsUnspecified() {
		// dual-stack, like Docker's [::] publish: collides with v4 too
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 0)
	}
	return syscall.Bind(fd, sa)
}
