package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"github.com/digio/gwork-cli/internal/config"
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
