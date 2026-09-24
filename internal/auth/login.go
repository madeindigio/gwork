package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/digio/gwork-cli/internal/config"
)

// Google endpoints used outside of the oauth2 package.
const (
	DefaultUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	DefaultRevokeURL   = "https://oauth2.googleapis.com/revoke"
)

// DefaultLoginTimeout bounds how long Login waits for the browser callback.
const DefaultLoginTimeout = 5 * time.Minute

// EnvHostedDomain overrides the "hd" hint sent to Google during login.
const EnvHostedDomain = "GWORK_HOSTED_DOMAIN"

// LoginOptions configures Login. Only Credentials is required.
type LoginOptions struct {
	// Credentials is the OAuth Desktop client.
	Credentials *ClientCredentials
	// Services selects the scopes to request (base scopes are always added).
	Services []Service
	// HostedDomain, when set, is sent as the "hd" hint.
	HostedDomain string
	// LoginHint, when set, pre-selects the account in Google's chooser.
	LoginHint string
	// Timeout bounds the wait for the callback; zero means DefaultLoginTimeout.
	Timeout time.Duration
	// OpenBrowser opens the authorization URL. When nil or failing, the
	// user must open the URL printed on Prompt manually.
	OpenBrowser func(url string) error
	// Prompt receives user-facing instructions (usually stderr).
	Prompt io.Writer
	// Endpoint overrides Google's OAuth endpoints (tests).
	Endpoint oauth2.Endpoint
	// UserinfoURL overrides DefaultUserinfoURL (tests).
	UserinfoURL string
	// HTTPClient is used for the token exchange and userinfo calls.
	HTTPClient *http.Client
	// ListenAddr is the loopback listen address, default "127.0.0.1:0".
	ListenAddr string
}

// OAuthConfig builds the oauth2 configuration for creds.
func OAuthConfig(creds *ClientCredentials, endpoint oauth2.Endpoint, redirectURL string, scopes []string) *oauth2.Config {
	if endpoint.TokenURL == "" {
		endpoint = google.Endpoint
	}
	return &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
	}
}

type callbackResult struct {
	code string
	err  error
}

// Login runs the OAuth loopback flow with PKCE (S256): it listens on
// 127.0.0.1, opens the browser on the consent page, waits for the redirect,
// validates state, exchanges the code and discovers the account email.
// The caller is responsible for persisting the returned token.
func Login(ctx context.Context, opts LoginOptions) (*StoredToken, error) {
	if opts.Credentials == nil {
		return nil, &Error{Kind: ErrNoCredentials, Message: "no OAuth client configured", Hint: SetupDocHint}
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultLoginTimeout
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = "127.0.0.1:0"
	}
	if opts.UserinfoURL == "" {
		opts.UserinfoURL = DefaultUserinfoURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	prompt := opts.Prompt
	if prompt == nil {
		prompt = io.Discard
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	requested := ScopesFor(opts.Services)
	cfg := OAuthConfig(opts.Credentials, opts.Endpoint, redirectURL, requested)

	state, err := randomState()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	params := []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce, // prompt=consent: always return a refresh token
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
	}
	if opts.HostedDomain != "" {
		params = append(params, oauth2.SetAuthURLParam("hd", opts.HostedDomain))
	}
	if opts.LoginHint != "" {
		params = append(params, oauth2.SetAuthURLParam("login_hint", opts.LoginHint))
	}
	authURL := cfg.AuthCodeURL(state, params...)

	results := make(chan callbackResult, 1)
	var once sync.Once
	deliver := func(r callbackResult) { once.Do(func() { results <- r }) }

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			writeCallbackPage(w, http.StatusBadRequest, "Login failed: invalid state parameter.")
			deliver(callbackResult{err: errors.New("OAuth callback state mismatch (possible CSRF); try again")})
		case q.Get("error") != "":
			code, desc := q.Get("error"), q.Get("error_description")
			writeCallbackPage(w, http.StatusForbidden, "Login failed: "+code+". You can close this window.")
			deliver(callbackResult{err: classifyOAuth(fmt.Errorf("authorization failed: %s", code), "", code, desc)})
		case q.Get("code") == "":
			writeCallbackPage(w, http.StatusBadRequest, "Login failed: missing authorization code.")
			deliver(callbackResult{err: errors.New("OAuth callback without authorization code")})
		default:
			writeCallbackPage(w, http.StatusOK, "gwork is now authorized. You can close this window and return to the terminal.")
			deliver(callbackResult{code: q.Get("code")})
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if opts.OpenBrowser == nil {
		fmt.Fprintf(prompt, "Open this URL in your browser to sign in:\n\n  %s\n\n", authURL)
	} else {
		fmt.Fprintf(prompt, "Opening the browser to sign in. If it does not open, visit:\n\n  %s\n\n", authURL)
		if err := opts.OpenBrowser(authURL); err != nil {
			fmt.Fprintf(prompt, "Could not open the browser (%v); open the URL above manually.\n", err)
		}
	}

	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	var res callbackResult
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("login cancelled: %w", ctx.Err())
	case <-timer.C:
		return nil, fmt.Errorf("login timed out after %s waiting for the browser callback", opts.Timeout)
	case res = <-results:
	}
	if res.err != nil {
		return nil, res.err
	}

	httpCtx := context.WithValue(ctx, oauth2.HTTPClient, opts.HTTPClient)
	tok, err := cfg.Exchange(httpCtx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", Classify(err))
	}

	granted := requested
	if s, ok := tok.Extra("scope").(string); ok && strings.TrimSpace(s) != "" {
		granted = strings.Fields(s)
	}

	email, err := discoverEmail(ctx, opts.HTTPClient, opts.UserinfoURL, tok)
	if err != nil {
		return nil, err
	}
	return &StoredToken{
		Token:   stripExtra(tok),
		Scopes:  granted,
		Email:   email,
		Created: time.Now().UTC(),
	}, nil
}

// stripExtra drops the raw token response (which includes the id_token)
// so only the fields oauth2.Token serializes are kept.
func stripExtra(t *oauth2.Token) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.Expiry,
		ExpiresIn:    t.ExpiresIn,
	}
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeCallbackPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><html><head><title>gwork</title></head><body style=\"font-family:sans-serif\"><p>%s</p></body></html>", html.EscapeString(msg))
}

// discoverEmail reads the email from the id_token when present, otherwise
// from the OpenID Connect userinfo endpoint.
func discoverEmail(ctx context.Context, hc *http.Client, userinfoURL string, tok *oauth2.Token) (string, error) {
	if idt, ok := tok.Extra("id_token").(string); ok && idt != "" {
		if email := emailFromIDToken(idt); email != "" {
			return email, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("build userinfo request: %w", err)
	}
	tok.SetAuthHeader(req)
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch userinfo: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var info struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("decode userinfo: %w", err)
	}
	if info.Email == "" {
		return "", errors.New("userinfo response has no email; was the userinfo.email scope granted?")
	}
	return config.NormalizeEmail(info.Email), nil
}

// emailFromIDToken extracts the email claim from a JWT without verifying the
// signature. This is acceptable because the token was received directly
// from Google's token endpoint over TLS, never from a third party.
func emailFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return config.NormalizeEmail(claims.Email)
}

// Revoke revokes token (access or refresh) at revokeURL
// (DefaultRevokeURL when empty). It is best effort: callers usually ignore
// the error and delete the local copy anyway.
func Revoke(ctx context.Context, hc *http.Client, revokeURL, token string) error {
	if revokeURL == "" {
		revokeURL = DefaultRevokeURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	form := url.Values{"token": {token}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form))
	if err != nil {
		return fmt.Errorf("build revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke token: HTTP %d", resp.StatusCode)
	}
	return nil
}
