package ports

import (
	"net/netip"
	"testing"
)

func TestIfaceFor(t *testing.T) {
	ifaces := []Iface{
		{Name: "lo", Addrs: []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}},
		{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.10")}},
		{Name: "wg0", Addrs: []netip.Addr{netip.MustParseAddr("10.8.0.1")}},
	}
	cases := map[string]string{
		"0.0.0.0":      "*",
		"::":           "*",
		"127.0.0.1":    "lo",
		"127.0.0.53":   "lo", // whole 127/8 is loopback
		"::1":          "lo",
		"10.8.0.1":     "wg0",
		"192.168.1.10": "eth0",
		"172.16.9.9":   "?",
		"224.0.0.251":  "multicast",
		"ff02::fb":     "multicast",
	}
	for ip, want := range cases {
		if got := IfaceFor(netip.MustParseAddr(ip), ifaces); got != want {
			t.Errorf("IfaceFor(%s) = %q, want %q", ip, got, want)
		}
	}
}
