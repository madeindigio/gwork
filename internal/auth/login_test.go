package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeGoogleAuth is a fake OAuth authorization server exposing /token,
// /userinfo and /revoke.
type fakeGoogleAuth struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	gotVerifier   string
	gotCode       string
	gotRedirect   string
	refreshCount  int
	revoked       []string
	idToken       string
	scope         string
	refreshStatus int
	refreshBody   string
	// refreshRotate, when set, is returned as a new refresh token.
	refreshRotate string
	// refreshDelay delays refresh responses.
	refreshDelay time.Duration
}

func newFakeGoogleAuth(t *testing.T) *fakeGoogleAuth {
	f := &fakeGoogleAuth{t: t, scope: "openid https://www.googleapis.com/auth/userinfo.email " + ScopeGmailReadonly}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"email":"Alice@Digio.es","email_verified":true}`)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogleAuth) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
}

func (f *fakeGoogleAuth) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.gotCode = r.PostForm.Get("code")
		f.gotVerifier = r.PostForm.Get("code_verifier")
		f.gotRedirect = r.PostForm.Get("redirect_uri")
		resp := map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         f.scope,
		}
		if f.idToken != "" {
			resp["id_token"] = f.idToken
		}
		_ = json.NewEncoder(w).Encode(resp)
	case "refresh_token":
		f.refreshCount++
		if f.refreshDelay > 0 {
			select {
			case <-time.After(f.refreshDelay):
			case <-r.Context().Done():
				return
			}
		}
		if f.refreshStatus != 0 {
			w.WriteHeader(f.refreshStatus)
			_, _ = io.WriteString(w, f.refreshBody)
			return
		}
		resp := map[string]any{
			"access_token": "access-refreshed",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if f.refreshRotate != "" {
			resp["refresh_token"] = f.refreshRotate
		}
		_ = json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, "unsupported grant", http.StatusBadRequest)
	}
}

// simulateBrowser returns an OpenBrowser func that checks the auth URL and
// then calls the loopback redirect like a browser would.
func simulateBrowser(t *testing.T, mutate func(q url.Values)) func(string) error {
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		for k, want := range map[string]string{
			"access_type":            "offline",
			"prompt":                 "consent",
			"code_challenge_method":  "S256",
			"include_granted_scopes": "true",
			"response_type":          "code",
		} {
			if got := q.Get(k); got != want {
				t.Errorf("auth URL %s = %q, want %q", k, got, want)
			}
		}
		if q.Get("code_challenge") == "" || q.Get("state") == "" {
			t.Error("auth URL lacks code_challenge or state")
		}
		redirect := q.Get("redirect_uri")
		if !strings.HasPrefix(redirect, "http://127.0.0.1:") || !strings.HasSuffix(redirect, "/callback") {
			t.Errorf("unexpected redirect_uri %q", redirect)
		}
		cb := url.Values{"state": {q.Get("state")}, "code": {"the-code"}}
		if mutate != nil {
			mutate(cb)
		}
		go func() {
			resp, err := http.Get(redirect + "?" + cb.Encode())
			if err != nil {
				t.Errorf("callback request: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
		return nil
	}
}

func testCreds() *ClientCredentials {
	return &ClientCredentials{ClientID: "cid", ClientSecret: "csecret", Source: SourceFlag}
}

func TestLoginSuccessUserinfo(t *testing.T) {
	f := newFakeGoogleAuth(t)
	var prompt strings.Builder
	st, err := Login(context.Background(), LoginOptions{
		Credentials:  testCreds(),
		Services:     []Service{Gmail},
		HostedDomain: "digio.es",
		Endpoint:     f.endpoint(),
		UserinfoURL:  f.srv.URL + "/userinfo",
		OpenBrowser:  simulateBrowser(t, nil),
		Prompt:       &prompt,
		Timeout:      10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Email != "alice@digio.es" {
		t.Fatalf("email %q", st.Email)
	}
	if st.Token.RefreshToken != "refresh-1" || st.Token.AccessToken != "access-1" {
		t.Fatalf("token %+v", st.Token)
	}
	if len(st.Services()) != 1 || st.Services()[0] != Gmail {
		t.Fatalf("services %v (scopes %v)", st.Services(), st.Scopes)
	}
	if f.gotCode != "the-code" || f.gotVerifier == "" {
		t.Fatalf("exchange got code=%q verifier=%q", f.gotCode, f.gotVerifier)
	}
	if !strings.Contains(prompt.String(), "hd=digio.es") {
		t.Fatalf("printed URL lacks hd hint: %s", prompt.String())
	}
}

func TestLoginEmailFromIDToken(t *testing.T) {
	f := newFakeGoogleAuth(t)
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"bob@digio.es","hd":"digio.es"}`))
	f.idToken = "eyJhbGciOiJub25lIn0." + claims + ".sig"
	st, err := Login(context.Background(), LoginOptions{
		Credentials: testCreds(),
		Services:    AllServices,
		Endpoint:    f.endpoint(),
		UserinfoURL: "http://127.0.0.1:1/unreachable",
		OpenBrowser: simulateBrowser(t, nil),
		Timeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Email != "bob@digio.es" {
		t.Fatalf("email %q", st.Email)
	}
}

// getStatus performs a GET and returns the status code.
func getStatus(t *testing.T, u string) int {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Errorf("callback request: %v", err)
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestLoginStateMismatchKeepsWaiting(t *testing.T) {
	f := newFakeGoogleAuth(t)
	statuses := make(chan []int, 1)
	st, err := Login(context.Background(), LoginOptions{
		Credentials: testCreds(),
		Endpoint:    f.endpoint(),
		UserinfoURL: f.srv.URL + "/userinfo",
		OpenBrowser: func(authURL string) error {
			u, err := url.Parse(authURL)
			if err != nil {
				return err
			}
			q := u.Query()
			redirect := q.Get("redirect_uri")
			go func() {
				var got []int
				for _, cb := range []url.Values{
					{"state": {"forged"}, "code": {"evil-code"}},
					{"code": {"evil-code"}},
					{"state": {"forged"}, "error": {"access_denied"}},
				} {
					got = append(got, getStatus(t, redirect+"?"+cb.Encode()))
				}
				statuses <- got
				getStatus(t, redirect+"?"+url.Values{"state": {q.Get("state")}, "code": {"the-code"}}.Encode())
			}()
			return nil
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("login must survive invalid callbacks: %v", err)
	}
	for _, code := range <-statuses {
		if code != http.StatusBadRequest {
			t.Errorf("invalid callback status = %d, want 400", code)
		}
	}
	if f.gotCode != "the-code" || st.Email != "alice@digio.es" {
		t.Fatalf("exchanged code %q, email %q", f.gotCode, st.Email)
	}
}

func TestLoginStateMismatchTimesOut(t *testing.T) {
	f := newFakeGoogleAuth(t)
	_, err := Login(context.Background(), LoginOptions{
		Credentials: testCreds(),
		Endpoint:    f.endpoint(),
		OpenBrowser: simulateBrowser(t, func(q url.Values) { q.Set("state", "forged") }),
		Timeout:     time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("expected timeout mentioning the rejected state, got %v", err)
	}
	if f.gotCode != "" {
		t.Fatal("code must not be exchanged on state mismatch")
	}
}

func TestLoginAccessDenied(t *testing.T) {
	f := newFakeGoogleAuth(t)
	_, err := Login(context.Background(), LoginOptions{
		Credentials: testCreds(),
		Endpoint:    f.endpoint(),
		OpenBrowser: simulateBrowser(t, func(q url.Values) {
			q.Del("code")
			q.Set("error", "org_internal")
		}),
		Timeout: 10 * time.Second,
	})
	if !errors.Is(err, ErrOrgInternal) {
		t.Fatalf("expected ErrOrgInternal, got %v", err)
	}
}

func TestLoginTimeout(t *testing.T) {
	f := newFakeGoogleAuth(t)
	_, err := Login(context.Background(), LoginOptions{
		Credentials: testCreds(),
		Endpoint:    f.endpoint(),
		OpenBrowser: func(string) error { return errors.New("no browser") },
		Timeout:     50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
}

func TestLoginRequiresCredentials(t *testing.T) {
	if _, err := Login(context.Background(), LoginOptions{}); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("got %v", err)
	}
}

func TestRevoke(t *testing.T) {
	f := newFakeGoogleAuth(t)
	if err := Revoke(context.Background(), nil, f.srv.URL+"/revoke", "refresh-1"); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "refresh-1" {
		t.Fatalf("revoked %v", f.revoked)
	}
	if err := Revoke(context.Background(), nil, f.srv.URL+"/missing", "x"); err == nil {
		t.Fatal("expected error on 404")
	}
}
