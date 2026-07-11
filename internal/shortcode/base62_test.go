package shortcode

import (
	"math"
	"testing"
)

func TestEncode_KnownValues(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "A"},
		{35, "Z"},
		{36, "a"},
		{61, "z"},
		{62, "10"},
		{62 * 62, "100"},
	}
	for _, tt := range tests {
		if got := Encode(tt.in); got != tt.want {
			t.Errorf("Encode(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	values := []int64{
		0, 1, 61, 62, 12345, 1_000_000,
		1 << 40, 1 << 62, math.MaxInt64,
	}
	for _, v := range values {
		code := Encode(v)
		got, err := Decode(code)
		if err != nil {
			t.Fatalf("Decode(%q): %v", code, err)
		}
		if got != v {
			t.Errorf("round-trip %d -> %q -> %d", v, code, got)
		}
	}
}

func TestEncode_MaxIDFitsIn11Chars(t *testing.T) {
	if code := Encode(math.MaxInt64); len(code) > 11 {
		t.Errorf("код для MaxInt64 = %q (%d символов), ожидали <= 11", code, len(code))
	}
}

func TestDecode_InvalidInput(t *testing.T) {
	for _, s := range []string{"", "abc-def", "hello world", "код"} {
		if _, err := Decode(s); err == nil {
			t.Errorf("Decode(%q): ожидали ошибку", s)
		}
	}
}
