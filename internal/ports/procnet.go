// Package ports inspects listening sockets on the host and checks whether
// ports are free to use.
package ports

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Proto string

const (
	TCP Proto = "tcp"
	UDP Proto = "udp"
)

// Socket is a listening (TCP) or bound, unconnected (UDP) socket.
type Socket struct {
	Proto Proto      `json:"proto"`
	IP    netip.Addr `json:"ip"`
	Port  uint16     `json:"port"`
	Inode uint64     `json:"-"`
	UID   int        `json:"uid"`
}

const (
	stateTCPListen = "0A"
	stateUDPClose  = "07" // bound, not connected
)

// ParseProcNet parses /proc/net/{tcp,tcp6,udp,udp6} content. Address family
// is inferred from the address width.
func ParseProcNet(r io.Reader, proto Proto) ([]Socket, error) {
	want := stateTCPListen
	if proto == UDP {
		want = stateUDPClose
	}
	var out []Socket
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		if f[3] != want {
			continue
		}
		if proto == UDP && !strings.HasSuffix(f[2], ":0000") {
			continue
		}
		ip, port, err := parseHexAddr(f[1])
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", f[1], err)
		}
		uid, _ := strconv.Atoi(f[7])
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		out = append(out, Socket{Proto: proto, IP: ip, Port: port, Inode: inode, UID: uid})
	}
	return out, sc.Err()
}

// parseHexAddr decodes "0100007F:1F90" or the 32-hex-digit IPv6 form. The
// kernel prints each 32-bit word in host (little-endian) byte order.
func parseHexAddr(s string) (netip.Addr, uint16, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.Addr{}, 0, errors.New("missing port")
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, 0, fmt.Errorf("bad address %q", h)
	}
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(raw[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	ip, _ := netip.AddrFromSlice(raw)
	return ip.Unmap(), uint16(port), nil
}

// ReadListening reads all four socket tables under dir (normally /proc/net).
// Missing tables (e.g. IPv6 disabled) are skipped.
func ReadListening(dir string) ([]Socket, error) {
	var out []Socket
	for _, t := range []struct {
		file  string
		proto Proto
	}{{"tcp", TCP}, {"tcp6", TCP}, {"udp", UDP}, {"udp6", UDP}} {
		f, err := os.Open(filepath.Join(dir, t.file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		s, err := ParseProcNet(f, t.proto)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.file, err)
		}
		out = append(out, s...)
	}
	return out, nil
}
