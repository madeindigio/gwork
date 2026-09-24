package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/oauth2"
)

// persistingTokenSource wraps a refreshing TokenSource and writes the token
// back to the store whenever it changes (i.e. after a refresh).
//
// A long-running process (gwork mcp) may outlive its startup snapshot: the
// user can run "gwork auth login" meanwhile, storing a new refresh token
// and scopes. After every refresh the store is therefore re-read: when it
// holds a different grant, that grant is adopted instead of being
// overwritten; otherwise only the access token fields (and a rotated
// refresh token) are updated in the stored record.
type persistingTokenSource struct {
	newSource func(*oauth2.Token) oauth2.TokenSource
	store     TokenStore

	mu     sync.Mutex
	base   oauth2.TokenSource
	stored StoredToken

	// current mirrors stored for readers that must not wait for a refresh
	// in progress (scope checks).
	current atomic.Pointer[StoredToken]
}

// NewPersistingTokenSource returns a TokenSource that obtains tokens from
// newSource(st.Token) and saves refreshed tokens to store under st.Email.
// newSource builds a refreshing source for a token (usually
// oauth2.Config.TokenSource); it is called again when a newer grant is
// found in the store. Errors from the source are classified (e.g.
// invalid_grant becomes ErrReauthRequired). A failure to persist is logged
// but does not fail the request.
func NewPersistingTokenSource(newSource func(*oauth2.Token) oauth2.TokenSource, store TokenStore, st *StoredToken) oauth2.TokenSource {
	return newPersistingTokenSource(newSource, store, st)
}

func newPersistingTokenSource(newSource func(*oauth2.Token) oauth2.TokenSource, store TokenStore, st *StoredToken) *persistingTokenSource {
	p := &persistingTokenSource{newSource: newSource, store: store, stored: cloneStored(st)}
	p.base = newSource(p.stored.Token)
	p.publish()
	return p
}

// Token implements oauth2.TokenSource.
func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// A second round runs only after adopting a newer grant from the
	// store, whose token may itself need a refresh.
	for round := 0; ; round++ {
		tok, err := p.base.Token()
		if err != nil {
			return nil, fmt.Errorf("obtain access token: %w", Classify(err))
		}
		old := p.stored.Token
		if tok.AccessToken == old.AccessToken && tok.RefreshToken == old.RefreshToken {
			return tok, nil
		}
		if !p.persist(tok) || round > 0 {
			return tok, nil
		}
	}
}

// persist records a refreshed tok. It returns true when the store holds a
// newer grant, which is then adopted (p.base is rebuilt) and the caller
// must fetch a token from it instead of using tok.
func (p *persistingTokenSource) persist(tok *oauth2.Token) (adopted bool) {
	snapshot := p.stored
	cur, err := p.store.Load(snapshot.Email)
	switch {
	case errors.Is(err, ErrTokenNotFound):
		// Logged out meanwhile: keep serving this process but do not
		// resurrect the stored credentials.
		p.updateAccess(tok)
		slog.Debug("stored token gone; refreshed token kept in memory only", "account", snapshot.Email)
		return false
	case err != nil:
		p.updateAccess(tok)
		slog.Warn("could not re-read stored token; refreshed token not persisted", "account", snapshot.Email, "err", err)
		return false
	case cur.Token.RefreshToken != snapshot.Token.RefreshToken || !slices.Equal(cur.Scopes, snapshot.Scopes):
		// A newer login replaced the grant: adopt it, never overwrite it.
		slog.Info("stored credentials changed; using the new grant", "account", snapshot.Email)
		p.stored = cloneStored(cur)
		p.base = p.newSource(p.stored.Token)
		p.publish()
		return true
	}
	// Same grant: keep the stored record (it may carry newer metadata) and
	// only update the fields a refresh changes.
	p.stored = cloneStored(cur)
	p.updateAccess(tok)
	save := p.stored
	if err := p.store.Save(&save); err != nil {
		slog.Warn("could not persist refreshed token", "account", p.stored.Email, "err", err)
	}
	return false
}

// updateAccess copies the refreshed fields of tok into p.stored.Token,
// including the refresh token when Google rotated it.
func (p *persistingTokenSource) updateAccess(tok *oauth2.Token) {
	t := *p.stored.Token
	t.AccessToken = tok.AccessToken
	t.TokenType = tok.TokenType
	t.Expiry = tok.Expiry
	t.ExpiresIn = tok.ExpiresIn
	if tok.RefreshToken != "" {
		t.RefreshToken = tok.RefreshToken
	}
	p.stored.Token = &t
	p.publish()
}

// publish exposes a copy of p.stored to Current.
func (p *persistingTokenSource) publish() {
	c := cloneStored(&p.stored)
	p.current.Store(&c)
}

// Current returns the grant currently in use (it changes when a newer
// login is adopted). It never blocks on a refresh.
func (p *persistingTokenSource) Current() StoredToken {
	return *p.current.Load()
}

// cloneStored deep-copies the mutable parts of st.
func cloneStored(st *StoredToken) StoredToken {
	c := *st
	if st.Token != nil {
		t := *st.Token
		c.Token = &t
	}
	c.Scopes = slices.Clone(st.Scopes)
	return c
}
