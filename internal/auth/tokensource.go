package auth

import (
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/oauth2"
)

// persistingTokenSource wraps a refreshing TokenSource and writes the token
// back to the store whenever it changes (i.e. after a refresh).
type persistingTokenSource struct {
	base  oauth2.TokenSource
	store TokenStore

	mu     sync.Mutex
	stored StoredToken
}

// NewPersistingTokenSource returns a TokenSource that obtains tokens from
// base and saves refreshed tokens to store under st.Email. Errors from base
// are classified (e.g. invalid_grant becomes ErrReauthRequired). A failure
// to persist is logged but does not fail the request.
func NewPersistingTokenSource(base oauth2.TokenSource, store TokenStore, st *StoredToken) oauth2.TokenSource {
	return &persistingTokenSource{base: base, store: store, stored: *st}
}

// Token implements oauth2.TokenSource.
func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	tok, err := p.base.Token()
	if err != nil {
		return nil, fmt.Errorf("obtain access token: %w", Classify(err))
	}
	old := p.stored.Token
	if old != nil && tok.AccessToken == old.AccessToken && tok.RefreshToken == old.RefreshToken {
		return tok, nil
	}
	save := *tok
	if save.RefreshToken == "" && old != nil {
		save.RefreshToken = old.RefreshToken
	}
	p.stored.Token = &save
	if err := p.store.Save(&p.stored); err != nil {
		slog.Warn("could not persist refreshed token", "account", p.stored.Email, "err", err)
	}
	return tok, nil
}
