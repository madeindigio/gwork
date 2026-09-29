package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

func TestConfirmWrite(t *testing.T) {
	cases := []struct {
		name     string
		flags    writeFlags
		tty      bool
		input    string
		wantErr  string
		wantPrmt bool
	}{
		{"yes flag", writeFlags{Yes: true}, false, "", "", false},
		{"tty y", writeFlags{}, true, "y\n", "", true},
		{"tty YES", writeFlags{}, true, "YES\n", "", true},
		{"tty n", writeFlags{}, true, "n\n", "aborted", true},
		{"tty empty", writeFlags{}, true, "\n", "aborted", true},
		{"tty eof", writeFlags{}, true, "", "aborted", true},
		{"no tty", writeFlags{}, false, "y\n", "--yes", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app, _, stderr := newTestApp(t, nil)
			app.In = strings.NewReader(c.input)
			app.IsTerminal = func() bool { return c.tty }
			err := app.confirmWrite(c.flags, "Send email to x.")
			if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("err = %v, want %q", err, c.wantErr)
			}
			if got := strings.Contains(stderr.String(), "Send email to x. Proceed? [y/N]: "); got != c.wantPrmt {
				t.Fatalf("prompt shown = %v, stderr %q", got, stderr.String())
			}
		})
	}
	app, _, _ := newTestApp(t, nil) // In and IsTerminal unset: non-interactive
	if err := app.confirmWrite(writeFlags{}, "x"); err == nil {
		t.Fatal("default App must not be interactive")
	}
}

func TestPrintDryRun(t *testing.T) {
	req := map[string]any{"to": "a@b.c", "subject": "hi"}

	app, stdout, stderr := newTestApp(t, nil)
	if err := app.printDryRun(req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "dry run: nothing was changed") || !strings.Contains(stdout.String(), `"subject": "hi"`) {
		t.Fatalf("text: stdout %q stderr %q", stdout, stderr)
	}

	app, stdout, stderr = newTestApp(t, nil)
	app.Flags.JSON = true
	if err := app.printDryRun(req); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got["to"] != "a@b.c" {
		t.Fatalf("json stdout %q: %v", stdout, err)
	}
	if strings.Contains(stderr.String(), "dry run") {
		t.Fatalf("json mode must keep stderr clean: %q", stderr)
	}
}

func TestAddWriteFlags(t *testing.T) {
	var f writeFlags
	cmd := &cobra.Command{Use: "x"}
	addWriteFlags(cmd, &f)
	if err := cmd.ParseFlags([]string{"-y", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if !f.Yes || !f.DryRun {
		t.Fatalf("flags %+v", f)
	}
}

func TestAppWriteClientOptions(t *testing.T) {
	p := testutil.NewFakeProvider(t, http.NotFoundHandler()).GrantWrite(auth.Gmail)
	app, _, _ := newTestApp(t, p)
	if _, err := app.WriteClientOptions(context.Background(), auth.Gmail); err != nil {
		t.Fatal(err)
	}
	_, err := app.WriteClientOptions(context.Background(), auth.Calendar)
	if !errors.Is(err, auth.ErrInsufficientScope) || auth.HintFor(err) != "run: gwork auth login --services calendar --write calendar" {
		t.Fatalf("err %v", err)
	}
	if got := classifyWrite(err, auth.Calendar); !errors.Is(got, err) {
		t.Fatal("classified errors must pass through")
	}
}

func TestLoginWriteFlag(t *testing.T) {
	dir := isolateConfig(t)
	writeClient(t, dir)

	_, _, errOut, code := login(t, "a@digio.es", "openid", "--write", "drive")
	if code == 0 || !strings.Contains(errOut, "does not support write") {
		t.Fatalf("--write drive: code %d, %q", code, errOut)
	}

	scopes := strings.Join(auth.ScopesForWrite([]auth.Service{auth.Gmail}, []auth.Service{auth.Gmail}), " ")
	_, out, errOut, code := login(t, "a@digio.es", scopes, "--services", "gmail", "--write", "gmail", "--json")
	if code != 0 {
		t.Fatalf("login: %s", errOut)
	}
	var info accountInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.WriteServices, []auth.Service{auth.Gmail}) {
		t.Fatalf("write_services %v", info.WriteServices)
	}
	if !strings.Contains(errOut, "gmail.modify") {
		t.Fatalf("auth URL must request gmail.modify: %q", errOut)
	}

	_, out, _, _ = login(t, "b@digio.es", "openid "+auth.ScopeGmailReadonly, "--services", "gmail", "--json")
	if !strings.Contains(out, `"write_services": []`) && !strings.Contains(out, `"write_services":[]`) {
		t.Fatalf("read-only login must show empty write_services: %s", out)
	}
}

func TestMCPAllowFlagsValidation(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"mcp", "--allow-send"}, "--allow-send requires gmail"},
		{[]string{"mcp", "--allow-send", "--allow-write", "calendar"}, "--allow-send requires gmail"},
		{[]string{"mcp", "--allow-write", "drive"}, "does not support write"},
		{[]string{"mcp", "--allow-write", "bogus"}, "unknown service"},
	}
	for _, c := range cases {
		_, errOut, code := runCLI(t, nil, c.args...)
		if code == 0 || !strings.Contains(errOut, c.wantErr) {
			t.Errorf("%v: code %d, stderr %q", c.args, code, errOut)
		}
	}
}
