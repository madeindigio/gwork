package mcpserver

import (
	"strings"
	"testing"
)

func TestTruncateText(t *testing.T) {
	cases := []struct {
		in    string
		max   int
		want  string
		trunc bool
	}{
		{"hello", 10, "hello", false},
		{"hello", 5, "hello", false},
		{"hello", 3, "hel", true},
		{"ñandú", 2, "ña", true},
		{"hello", 0, "hello", false},
		{"", 3, "", false},
	}
	for _, c := range cases {
		got, trunc := TruncateText(c.in, c.max)
		if got != c.want || trunc != c.trunc {
			t.Errorf("TruncateText(%q,%d) = %q,%v want %q,%v", c.in, c.max, got, trunc, c.want, c.trunc)
		}
	}
	if effectiveMaxChars(0) != DefaultMaxChars || effectiveMaxChars(7) != 7 {
		t.Fatal("effectiveMaxChars mismatch")
	}
}

func TestTruncateEach(t *testing.T) {
	a, b, c := "hello", "ñandú", "hi"
	if !truncateEach(3, &a, &b, &c) || a != "hel" || b != "ñan" || c != "hi" {
		t.Errorf("got %q %q %q", a, b, c)
	}
	a, b = "hello", "hi"
	if truncateEach(5, &a, &b) || a != "hello" || b != "hi" {
		t.Errorf("no cut expected: %q %q", a, b)
	}
	long := strings.Repeat("x", DefaultMaxChars+1)
	if !truncateEach(0, &long) || len(long) != DefaultMaxChars {
		t.Errorf("default limit not applied: %d", len(long))
	}
	if truncateEach(3) {
		t.Error("no texts: want false")
	}
}

func TestTruncateShared(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		in     []string
		want   []string
		trunc  bool
	}{
		{"fits", 10, []string{"abc", "de"}, []string{"abc", "de"}, false},
		{"exact", 5, []string{"abc", "de"}, []string{"abc", "de"}, false},
		{"cut second", 4, []string{"abc", "de"}, []string{"abc", "d"}, true},
		{"empty rest", 3, []string{"ééé", "x", "", "y"}, []string{"ééé", "", "", ""}, true},
		{"cut first", 2, []string{"abc", "de"}, []string{"ab", ""}, true},
		{"trailing empty ok", 3, []string{"abc", ""}, []string{"abc", ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			texts := append([]string(nil), tt.in...)
			ptrs := make([]*string, len(texts))
			for i := range texts {
				ptrs[i] = &texts[i]
			}
			got := truncateShared(tt.budget, ptrs...)
			if got != tt.trunc || strings.Join(texts, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %v %q, want %v %q", got, texts, tt.trunc, tt.want)
			}
		})
	}
	long := strings.Repeat("x", DefaultMaxChars+1)
	if !truncateShared(0, &long) || len(long) != DefaultMaxChars {
		t.Errorf("default budget not applied: %d", len(long))
	}
}
