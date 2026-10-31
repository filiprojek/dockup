package ports

import (
	"reflect"
	"testing"
)

func TestParseSpec(t *testing.T) {
	cases := []struct {
		in   string
		want []uint16
	}{
		{"8080", []uint16{8080}},
		{"8080,8443", []uint16{8080, 8443}},
		{"9000-9003", []uint16{9000, 9001, 9002, 9003}},
		{"80, 9000-9001 ,443", []uint16{80, 443, 9000, 9001}},
		{"8080,8080,8079-8080", []uint16{8079, 8080}}, // deduped + sorted
	}
	for _, tc := range cases {
		got, err := ParseSpec(tc.in)
		if err != nil {
			t.Errorf("ParseSpec(%q) err: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseSpec(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseSpecErrors(t *testing.T) {
	for _, in := range []string{"", "abc", "0", "65536", "9010-9000", "1-", "-5", "80,,81"} {
		if _, err := ParseSpec(in); err == nil {
			t.Errorf("ParseSpec(%q) expected error", in)
		}
	}
}

func TestParseRange(t *testing.T) {
	lo, hi, err := ParseRange("8000-9000")
	if err != nil || lo != 8000 || hi != 9000 {
		t.Errorf("got %d %d %v", lo, hi, err)
	}
	if _, _, err := ParseRange("8000"); err == nil {
		t.Error("single port is not a range")
	}
}
