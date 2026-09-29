package cli

import (
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/mcpserver"
)

func TestMCPToolTimeout(t *testing.T) {
	cases := []struct {
		name     string
		explicit bool
		flag     time.Duration
		want     time.Duration
	}{
		{"default flag uses server default", false, DefaultTimeout, 0},
		{"explicit value", true, 3 * time.Minute, 3 * time.Minute},
		{"explicit zero disables", true, 0, mcpserver.NoToolTimeout},
	}
	for _, c := range cases {
		if got := mcpToolTimeout(c.explicit, c.flag); got != c.want {
			t.Errorf("%s: mcpToolTimeout(%v, %v) = %v, want %v", c.name, c.explicit, c.flag, got, c.want)
		}
	}
}

func TestMCPTimeoutFlagChanged(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--timeout", "0"}, true},
	} {
		app, _, _ := newTestApp(t, nil)
		mcpCmd, _, err := NewRootCmd(app).Find([]string{"mcp"})
		if err != nil {
			t.Fatal(err)
		}
		if err := mcpCmd.ParseFlags(c.args); err != nil {
			t.Fatal(err)
		}
		if got := mcpCmd.Flags().Changed("timeout"); got != c.want {
			t.Errorf("args %v: Changed(timeout) = %v, want %v", c.args, got, c.want)
		}
	}
}
