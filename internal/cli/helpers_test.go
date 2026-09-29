package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/auth"
)

// testNow is the fixed clock of test apps.
var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// newTestApp returns an App with captured stdout/stderr, a fixed clock and,
// when provider is non-nil, that provider injected (e.g. a
// testutil.FakeProvider).
func newTestApp(t *testing.T, provider auth.ClientProvider) (app *App, stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	app = NewApp(stdout, stderr)
	app.Now = func() time.Time { return testNow }
	if provider != nil {
		app.NewProvider = func(context.Context) (auth.ClientProvider, error) { return provider, nil }
	}
	return app, stdout, stderr
}

// runCLI runs args against a fresh test app and returns stdout, stderr and
// the exit code.
func runCLI(t *testing.T, provider auth.ClientProvider, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	app, out, errw := newTestApp(t, provider)
	code = app.Run(context.Background(), args)
	return out.String(), errw.String(), code
}

// isolateConfig points GWORK_CONFIG_DIR at a temp dir and forces the file
// token store so tests never touch the real keyring or config.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GWORK_CONFIG_DIR", dir)
	t.Setenv(auth.EnvKeyring, "file")
	t.Setenv(auth.EnvAccount, "")
	t.Setenv(auth.EnvCredentials, "")
	t.Setenv(auth.EnvHostedDomain, "")
	return dir
}
