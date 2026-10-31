package ports

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

func TestSysBinderDetectsTCPListener(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := uint16(l.Addr().(*net.TCPAddr).Port)

	err = SysBinder{}.Bind(TCP, addr("127.0.0.1"), port)
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("got %v, want EADDRINUSE", err)
	}
	// wildcard collides with the specific listener
	if err := (SysBinder{}).Bind(TCP, addr("0.0.0.0"), port); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("wildcard got %v, want EADDRINUSE", err)
	}
}

func TestSysBinderDetectsUDP(t *testing.T) {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	port := uint16(c.LocalAddr().(*net.UDPAddr).Port)
	if err := (SysBinder{}).Bind(UDP, addr("127.0.0.1"), port); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("got %v, want EADDRINUSE", err)
	}
}

func TestSysBinderFreePortAndRelease(t *testing.T) {
	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	l.Close()

	if err := (SysBinder{}).Bind(TCP, addr("127.0.0.1"), port); err != nil {
		t.Fatalf("free port: %v", err)
	}
	// the test bind must not leave the port occupied
	l2, err := net.Listen("tcp4", netip.AddrPortFrom(addr("127.0.0.1"), port).String())
	if err != nil {
		t.Fatalf("port not released: %v", err)
	}
	l2.Close()
}

func TestSysBinderForeignAddress(t *testing.T) {
	err := SysBinder{}.Bind(TCP, addr("203.0.113.7"), 9)
	if !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Fatalf("got %v, want EADDRNOTAVAIL", err)
	}
}
