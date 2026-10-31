package ports

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ParseSpec parses "8080", "8080,8443", "9000-9010" and mixtures thereof into
// a sorted, de-duplicated port list.
func ParseSpec(s string) ([]uint16, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty port spec")
	}
	var out []uint16
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			lo, hi, err := ParseRange(part)
			if err != nil {
				return nil, err
			}
			for p := int(lo); p <= int(hi); p++ {
				out = append(out, uint16(p))
			}
			continue
		}
		p, err := parsePort(part)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// ParseRange parses "lo-hi".
func ParseRange(s string) (uint16, uint16, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return 0, 0, fmt.Errorf("range %q: expected lo-hi", s)
	}
	lo, err := parsePort(a)
	if err != nil {
		return 0, 0, err
	}
	hi, err := parsePort(b)
	if err != nil {
		return 0, 0, err
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("range %q: start after end", s)
	}
	return lo, hi, nil
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return uint16(n), nil
}
