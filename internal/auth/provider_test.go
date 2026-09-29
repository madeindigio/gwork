package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"github.com/madeindigio/gwork/internal/config"
)

func expiredToken(email string, scopes ...string) *StoredToken {
	return &StoredToken{
		Token:  &oauth2.Token{AccessToken: "old", RefreshToken: "refresh-1", TokenType: "Bearer", Expiry: time.Now().Add(-time.Hour)},
		Scopes: scopes,
		Email:  email,
	}
}

func TestStoreProviderRefreshPersists(t *testing.T) {
	keyring.MockInit()
	f := newFakeGoogleAuth(t)
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	if err := store.Save(expiredToken("a@digio.es", ScopeGmailReadonly)); err != nil {
		t.Fatal(err)
	}
	p, err := NewStoreProvider(context.Background(), ProviderOptions{
		Account: "a@digio.es", Store: store, Credentials: testCreds(), Endpoint: f.endpoint(), UserAgent: "gwork/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Account() != "a@digio.es" {
		t.Fatalf("account %q", p.Account())
	}
	if _, err := p.ClientOptions(context.Background(), Gmail); err != nil {
		t.Fatal(err)
	}
	tok, err := p.TokenSource().Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-refreshed" {
		t.Fatalf("access token %q", tok.AccessToken)
	}
	saved, err := store.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.AccessToken != "access-refreshed" || saved.Token.RefreshToken != "refresh-1" {
		t.Fatalf("saved token %+v", saved.Token)
	}
	// A second call reuses the valid token without refreshing again.
	if _, err := p.TokenSource().Token(); err != nil {
		t.Fatal(err)
	}
	if f.refreshCount != 1 {
		t.Fatalf("refresh count %d", f.refreshCount)
	}
}

func TestStoreProviderScopeCheck(t *testing.T) {
	keyring.MockInit()
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	_ = store.Save(expiredToken("a@digio.es", ScopeGmailReadonly))
	p, err := NewStoreProvider(context.Background(), ProviderOptions{Account: "a@digio.es", Store: store, Credentials: testCreds()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ClientOptions(context.Background(), Chat)
	if !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("expected ErrInsufficientScope, got %v", err)
	}
	if HintFor(err) != "run: gwork auth login --services chat" {
		t.Fatalf("hint %q", HintFor(err))
	}
	if g := p.GrantedServices(); len(g) != 1 || g[0] != Gmail {
		t.Fatalf("granted %v", g)
	}
}

func TestStoreProviderInvalidGrant(t *testing.T) {
	keyring.MockInit()
	f := newFakeGoogleAuth(t)
	f.refreshStatus = http.StatusBadRequest
	f.refreshBody = `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	_ = store.Save(expiredToken("a@digio.es", ScopeGmailReadonly))
	p, err := NewStoreProvider(context.Background(), ProviderOptions{Account: "a@digio.es", Store: store, Credentials: testCreds(), Endpoint: f.endpoint()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.TokenSource().Token(); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("expected ErrReauthRequired, got %v", err)
	}
}

func TestStoreProviderNotLoggedIn(t *testing.T) {
	keyring.MockInit()
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	_, err := NewStoreProvider(context.Background(), ProviderOptions{Account: "nobody@digio.es", Store: store, Credentials: testCreds()})
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveAccount(t *testing.T) {
	cfg := &config.Config{DefaultAccount: "def@digio.es"}
	cases := []struct{ flag, env, want string }{
		{"Flag@digio.es", "env@digio.es", "flag@digio.es"},
		{"", "env@digio.es", "env@digio.es"},
		{"", "", "def@digio.es"},
	}
	for _, c := range cases {
		got, err := ResolveAccount(c.flag, c.env, cfg)
		if err != nil || got != c.want {
			t.Errorf("ResolveAccount(%q,%q) = %q, %v", c.flag, c.env, got, err)
		}
	}
	if _, err := ResolveAccount("", "", &config.Config{}); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("got %v", err)
	}
}

// newRefreshingProvider stores an expired token for a@digio.es and returns
// a provider over it.
func newRefreshingProvider(t *testing.T, f *fakeGoogleAuth, hc *http.Client) (*StoreProvider, *FallbackStore) {
	t.Helper()
	keyring.MockInit()
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	st := expiredToken("a@digio.es", ScopeGmailReadonly)
	st.Created = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	p, err := NewStoreProvider(context.Background(), ProviderOptions{
		Account: "a@digio.es", Store: store, Credentials: testCreds(), Endpoint: f.endpoint(), HTTPClient: hc,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, store
}

func TestRefreshAdoptsNewerLogin(t *testing.T) {
	f := newFakeGoogleAuth(t)
	p, store := newRefreshingProvider(t, f, nil)

	// Meanwhile, "gwork auth login --services gmail,chat" stores a new grant.
	newer := &StoredToken{
		Token:  &oauth2.Token{AccessToken: "access-new", RefreshToken: "refresh-2", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)},
		Scopes: ScopesFor([]Service{Gmail, Chat}),
		Email:  "a@digio.es",
	}
	if err := store.Save(newer); err != nil {
		t.Fatal(err)
	}

	tok, err := p.TokenSource().Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-new" {
		t.Fatalf("access token %q, want the newer grant's", tok.AccessToken)
	}
	saved, err := store.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.RefreshToken != "refresh-2" || saved.Token.AccessToken != "access-new" || len(saved.Scopes) != len(newer.Scopes) {
		t.Fatalf("newer login was overwritten: %+v scopes %v", saved.Token, saved.Scopes)
	}
	if _, err := p.ClientOptions(context.Background(), Chat); err != nil {
		t.Fatalf("adopted grant should include chat: %v", err)
	}
}

func TestRefreshUpdatesOnlyAccessFields(t *testing.T) {
	f := newFakeGoogleAuth(t)
	f.refreshRotate = "refresh-rotated"
	p, store := newRefreshingProvider(t, f, nil)

	if _, err := p.TokenSource().Token(); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.AccessToken != "access-refreshed" || saved.Token.RefreshToken != "refresh-rotated" {
		t.Fatalf("saved token %+v", saved.Token)
	}
	if !saved.Created.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || len(saved.Scopes) != 1 {
		t.Fatalf("record metadata changed: created %v scopes %v", saved.Created, saved.Scopes)
	}
	if !saved.Token.Expiry.After(time.Now()) {
		t.Fatalf("expiry not updated: %v", saved.Token.Expiry)
	}
}

func TestRefreshAfterLogoutDoesNotResurrect(t *testing.T) {
	f := newFakeGoogleAuth(t)
	p, store := newRefreshingProvider(t, f, nil)
	if err := store.Delete("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.TokenSource().Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("token was written back after logout: %v", err)
	}
}

func TestRefreshHTTPClientTimeout(t *testing.T) {
	if c := refreshClient(nil); c.Timeout != DefaultRefreshTimeout {
		t.Fatalf("default refresh timeout %v", c.Timeout)
	}
	f := newFakeGoogleAuth(t)
	f.refreshDelay = 5 * time.Second
	p, _ := newRefreshingProvider(t, f, &http.Client{Timeout: 50 * time.Millisecond})
	start := time.Now()
	if _, err := p.TokenSource().Token(); err == nil {
		t.Fatal("expected a timeout error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("refresh was not bounded by the client timeout: %v", d)
	}
}
