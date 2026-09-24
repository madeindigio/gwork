package output

import (
	"testing"
	"time"
)

func TestDateTime(t *testing.T) {
	madrid := time.FixedZone("CEST", 2*3600)
	ts := time.Date(2026, 9, 24, 8, 30, 0, 0, time.UTC)
	if got := DateTime(ts, madrid); got != "2026-09-24 10:30" {
		t.Errorf("DateTime = %q", got)
	}
	if got := DateTime(ts, time.UTC); got != "2026-09-24 08:30" {
		t.Errorf("DateTime UTC = %q", got)
	}
	if got := DateTime(ts, nil); got != ts.Local().Format(DateTimeLayout) {
		t.Errorf("DateTime nil loc = %q", got)
	}
	if got := DateTime(time.Time{}, madrid); got != "" {
		t.Errorf("zero time = %q", got)
	}
}

func TestSameDay(t *testing.T) {
	a := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	if !SameDay(a, a.Add(23*time.Hour+59*time.Minute)) {
		t.Error("same day expected")
	}
	if SameDay(a, a.Add(24*time.Hour)) {
		t.Error("different day expected")
	}
}

func TestPerson(t *testing.T) {
	tests := []struct{ name, email, want string }{
		{"Ana", "ana@digio.es", "Ana <ana@digio.es>"},
		{"Ana", "", "Ana"},
		{"", "ana@digio.es", "ana@digio.es"},
		{"", "", ""},
	}
	for _, tt := range tests {
		if got := Person(tt.name, tt.email); got != tt.want {
			t.Errorf("Person(%q, %q) = %q, want %q", tt.name, tt.email, got, tt.want)
		}
	}
}

func TestBytes(t *testing.T) {
	tests := map[int64]string{-1: "", 0: "", 512: "512 B", 1023: "1023 B", 1536: "1.5 KiB", 2048: "2.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"}
	for n, want := range tests {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
