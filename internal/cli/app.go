// Package cli implements the gwork cobra commands. Commands are thin
// adapters: they parse flags, obtain API client options from the App,
// call internal/workspace/* and render results with App.Print.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/buildinfo"
	"github.com/digio/gwork-cli/internal/config"
	"github.com/digio/gwork-cli/internal/output"
)

// DefaultTimeout is the default value of --timeout for API commands.
const DefaultTimeout = 60 * time.Second

// GlobalFlags holds the persistent flags shared by every command.
type GlobalFlags struct {
	Account     string
	Credentials string
	Output      string
	JSON        bool
	Timeout     time.Duration
}

// App is the state shared by all commands: global flags, output streams,
// the lazily created auth.ClientProvider and the printer. Tests construct
// it with NewApp and inject fakes through the exported hook fields.
type App struct {
	// Out receives command results (stdout).
	Out io.Writer
	// Err receives diagnostics, prompts and warnings (stderr).
	Err io.Writer
	// Flags are the parsed global flags.
	Flags GlobalFlags

	// Now returns the current time; tests may override it.
	Now func() time.Time
	// OpenBrowser opens a URL for auth login; nil disables it.
	OpenBrowser func(url string) error
	// NewProvider, when set, replaces the real provider construction
	// (tests inject testutil.FakeProvider through it).
	NewProvider func(ctx context.Context) (auth.ClientProvider, error)
	// OAuthEndpoint, UserinfoURL and RevokeURL override Google endpoints
	// (tests). Zero values use Google's.
	OAuthEndpoint oauth2.Endpoint
	UserinfoURL   string
	RevokeURL     string

	provider auth.ClientProvider
	cancel   context.CancelFunc
}

// NewApp returns an App writing results to out and diagnostics to errw.
func NewApp(out, errw io.Writer) *App {
	return &App{Out: out, Err: errw, Now: time.Now}
}

// CurrentTime returns the App clock's current time.
func (a *App) CurrentTime() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Format returns the selected output format (--json wins over --output).
func (a *App) Format() (output.Format, error) {
	if a.Flags.JSON {
		return output.FormatJSON, nil
	}
	return output.ParseFormat(a.Flags.Output)
}

// JSON reports whether JSON output was requested.
func (a *App) JSON() bool {
	f, _ := a.Format()
	return f == output.FormatJSON
}

// Printer returns a printer for the selected format writing to Out.
func (a *App) Printer() *output.Printer {
	f, err := a.Format()
	if err != nil {
		f = output.FormatText
	}
	return output.New(a.Out, f)
}

// Print renders v as JSON with --output json, otherwise calls textFn with
// Out. Use output.Table / output.KeyValues inside textFn for tabular text.
func (a *App) Print(v any, textFn func(w io.Writer) error) error {
	return a.Printer().Print(v, textFn)
}

// ConfigDir returns the gwork configuration directory.
func (a *App) ConfigDir() (string, error) {
	return config.Dir()
}

// tokenStore returns the token store for dir.
func (a *App) tokenStore(dir string) *auth.FallbackStore {
	return auth.NewTokenStore(dir, a.Err)
}

// credentials resolves the OAuth client following the documented precedence.
func (a *App) credentials(dir string) (*auth.ClientCredentials, error) {
	return auth.ResolveCredentials(auth.DefaultCredentialInputs(a.Flags.Credentials, dir))
}

// Provider returns the ClientProvider for the selected account, creating
// it on first use.
func (a *App) Provider(ctx context.Context) (auth.ClientProvider, error) {
	if a.provider != nil {
		return a.provider, nil
	}
	if a.NewProvider != nil {
		p, err := a.NewProvider(ctx)
		if err != nil {
			return nil, err
		}
		a.provider = p
		return p, nil
	}
	dir, err := a.ConfigDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	account, err := auth.ResolveAccount(a.Flags.Account, os.Getenv(auth.EnvAccount), cfg)
	if err != nil {
		return nil, err
	}
	creds, err := a.credentials(dir)
	if err != nil {
		return nil, err
	}
	p, err := auth.NewStoreProvider(ctx, auth.ProviderOptions{
		Account:     account,
		Store:       a.tokenStore(dir),
		Credentials: creds,
		Endpoint:    a.OAuthEndpoint,
		UserAgent:   buildinfo.UserAgent(),
	})
	if err != nil {
		return nil, err
	}
	a.provider = p
	return p, nil
}

// ClientOptions returns google.golang.org/api client options for svc,
// failing with an actionable error when the account has not granted it.
//
//	opts, err := app.ClientOptions(cmd.Context(), auth.Gmail)
func (a *App) ClientOptions(ctx context.Context, svc auth.Service) ([]option.ClientOption, error) {
	p, err := a.Provider(ctx)
	if err != nil {
		return nil, err
	}
	opts, err := p.ClientOptions(ctx, svc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", svc, err)
	}
	return opts, nil
}
