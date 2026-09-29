package buildinfo

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldV, oldC, oldD })

	Version, Commit, Date = "v1.2.3", "abc123", "2026-01-01T00:00:00Z"
	got := String()
	if !strings.HasPrefix(got, "gwork v1.2.3 (abc123, 2026-01-01T00:00:00Z)") {
		t.Fatalf("String() = %q", got)
	}
	if UserAgent() != "gwork/v1.2.3" {
		t.Fatalf("UserAgent() = %q", UserAgent())
	}
}
