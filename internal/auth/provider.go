package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	"github.com/madeindigio/gwork/internal/config"
)

// EnvAccount selects the account when --account is not given.
const EnvAccount = "GWORK_ACCOUNT"

// DefaultRefreshTimeout bounds a token refresh request when
// ProviderOptions.HTTPClient is nil. Refreshes run outside the per-command
// context (and under a lock shared by concurrent MCP calls), so they need
// their own bound.
const DefaultRefreshTimeout = 30 * time.Second

// ClientProvider hands out google.golang.org/api client options for the
// current account. It is the only thing CLI commands and MCP tools need to
// build API clients:
//
//	opts, err := provider.ClientOptions(ctx, auth.Gmail)
//	svc, err := gmail.NewService(ctx, opts...)
//
// Tests use testutil.FakeProvider, which points clients at an httptest
// server.
type ClientProvider interface {
	// ClientOptions returns options for google.golang.org/api constructors,
	// after checking that the account granted the scopes of svc. A missing
	// scope yields an error wrapping ErrInsufficientScope.
	ClientOptions(ctx context.Context, svc Service) ([]option.ClientOption, error)
	// Account returns the email of the current account.
	Account() string
	// GrantedServices returns the services the account has granted.
	GrantedServices() []Service
}

// ProviderOptions configures NewStoreProvider.
type ProviderOptions struct {
	// Account is the account email (see ResolveAccount).
	Account string
	// Store holds the account's token.
	Store TokenStore
	// Credentials is the OAuth client, needed to refresh tokens.
	Credentials *ClientCredentials
	// Endpoint overrides Google's OAuth endpoints (tests).
	Endpoint oauth2.Endpoint
	// HTTPClient is used for token refreshes; nil uses a client with
	// DefaultRefreshTimeout.
	HTTPClient *http.Client
	// UserAgent is sent to Google APIs when not empty.
	UserAgent string
}

// StoreProvider is the real ClientProvider, backed by a TokenStore.
type StoreProvider struct {
	account   string
	ts        *persistingTokenSource
	userAgent string
}

var _ ClientProvider = (*StoreProvider)(nil)

// NewStoreProvider loads the account's token and prepares a refreshing,
// persisting token source. It fails with ErrNotLoggedIn when there is no
// stored token.
func NewStoreProvider(ctx context.Context, opts ProviderOptions) (*StoreProvider, error) {
	if opts.Account == "" {
		return nil, &Error{Kind: ErrNotLoggedIn, Message: "no account selected", Hint: "run: gwork auth login"}
	}
	st, err := opts.Store.Load(opts.Account)
	if errors.Is(err, ErrTokenNotFound) {
		return nil, &Error{
			Kind:    ErrNotLoggedIn,
			Message: fmt.Sprintf("no stored credentials for %s", opts.Account),
			Hint:    "run: gwork auth login",
		}
	}
	if err != nil {
		return nil, fmt.Errorf("load token for %s: %w", opts.Account, err)
	}
	if opts.Credentials == nil {
		return nil, &Error{Kind: ErrNoCredentials, Message: "no OAuth client configured to refresh tokens", Hint: SetupDocHint}
	}
	cfg := OAuthConfig(opts.Credentials, opts.Endpoint, "", st.Scopes)
	// The refresh context must outlive the caller's (per-command) context.
	refreshCtx := context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, refreshClient(opts.HTTPClient))
	newSource := func(t *oauth2.Token) oauth2.TokenSource { return cfg.TokenSource(refreshCtx, t) }
	return &StoreProvider{
		account:   st.Email,
		ts:        newPersistingTokenSource(newSource, opts.Store, st),
		userAgent: opts.UserAgent,
	}, nil
}

// refreshClient returns hc, or a client bounded by DefaultRefreshTimeout.
func refreshClient(hc *http.Client) *http.Client {
	if hc != nil {
		return hc
	}
	return &http.Client{Timeout: DefaultRefreshTimeout}
}

// Account implements ClientProvider.
func (p *StoreProvider) Account() string { return p.account }

// GrantedServices implements ClientProvider. It reflects the grant in use,
// which changes when a newer login is picked up after a refresh.
func (p *StoreProvider) GrantedServices() []Service {
	cur := p.ts.Current()
	return cur.Services()
}

// Scopes returns the granted scopes.
func (p *StoreProvider) Scopes() []string { return slices.Clone(p.ts.Current().Scopes) }

// TokenSource returns the persisting token source.
func (p *StoreProvider) TokenSource() oauth2.TokenSource { return p.ts }

// ClientOptions implements ClientProvider.
func (p *StoreProvider) ClientOptions(_ context.Context, svc Service) ([]option.ClientOption, error) {
	if !svc.Valid() {
		return nil, fmt.Errorf("unknown service %q", svc)
	}
	if len(MissingScopes(p.ts.Current().Scopes, svc)) > 0 {
		return nil, NewScopeError(p.account, svc)
	}
	opts := []option.ClientOption{option.WithTokenSource(p.ts)}
	if p.userAgent != "" {
		opts = append(opts, option.WithUserAgent(p.userAgent))
	}
	return opts, nil
}

// ResolveAccount picks the account: --account flag, then GWORK_ACCOUNT, then
// the default account in config.json.
func ResolveAccount(flag, env string, cfg *config.Config) (string, error) {
	switch {
	case flag != "":
		return config.NormalizeEmail(flag), nil
	case env != "":
		return config.NormalizeEmail(env), nil
	case cfg != nil && cfg.DefaultAccount != "":
		return cfg.DefaultAccount, nil
	}
	return "", &Error{Kind: ErrNotLoggedIn, Message: "no account logged in", Hint: "run: gwork auth login"}
}
