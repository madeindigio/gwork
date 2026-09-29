package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/config"
)

// fakeOAuth serves /token, /userinfo and /revoke like Google would.
type fakeOAuth struct {
	srv     *httptest.Server
	mu      sync.Mutex
	revoked []string
	email   string
	scope   string
}

func newFakeOAuth(t *testing.T, email, scope string) *fakeOAuth {
	f := &fakeOAuth{email: email, scope: scope}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-" + f.email, "refresh_token": "rt-" + f.email,
			"token_type": "Bearer", "expires_in": 3600, "scope": f.scope,
		})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"email":"`+f.email+`"}`)
	})
	mux.HandleFunc("POST /revoke", func(_ http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// configure points app at the fake endpoints and simulates the browser.
func (f *fakeOAuth) configure(t *testing.T, app *App) {
	app.OAuthEndpoint = oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}
	app.UserinfoURL = f.srv.URL + "/userinfo"
	app.RevokeURL = f.srv.URL + "/revoke"
	app.OpenBrowser = func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state")))
			if err != nil {
				t.Errorf("callback: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
		return nil
	}
}

func writeClient(t *testing.T, dir string) {
	t.Helper()
	data := `{"installed":{"client_id":"cid","client_secret":"cs"}}`
	if err := os.WriteFile(filepath.Join(dir, auth.CredentialsFileName), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func login(t *testing.T, email, scope string, args ...string) (*fakeOAuth, string, string, int) {
	t.Helper()
	f := newFakeOAuth(t, email, scope)
	app, out, errw := newTestApp(t, nil)
	f.configure(t, app)
	code := app.Run(context.Background(), append([]string{"auth", "login"}, args...))
	return f, out.String(), errw.String(), code
}

func TestAuthLoginListUseStatusLogout(t *testing.T) {
	dir := isolateConfig(t)
	writeClient(t, dir)

	gmailScopes := "openid " + auth.ScopeUserinfoEmail + " " + auth.ScopeGmailReadonly
	_, out, errOut, code := login(t, "alice@digio.es", gmailScopes, "--services", "gmail")
	if code != 0 {
		t.Fatalf("login failed: %s", errOut)
	}
	if !strings.Contains(out, "Logged in as alice@digio.es (services: gmail; stored in file)") {
		t.Fatalf("login out %q", out)
	}
	if !strings.Contains(errOut, "/auth?") || !strings.Contains(errOut, "code_challenge=") {
		t.Fatalf("login must print the URL on stderr, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "tokens", "alice@digio.es.json")); err != nil {
		t.Fatalf("token file: %v", err)
	}

	allScopes := strings.Join(auth.ScopesFor(auth.AllServices), " ")
	if _, out, errOut, code := login(t, "bob@digio.es", allScopes, "--json"); code != 0 {
		t.Fatalf("second login failed: %s", errOut)
	} else {
		var info accountInfo
		if err := json.Unmarshal([]byte(out), &info); err != nil {
			t.Fatalf("login json %q: %v", out, err)
		}
		if info.Account != "bob@digio.es" || info.Default || len(info.Services) != 4 {
			t.Fatalf("login info %+v", info)
		}
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAccount != "alice@digio.es" || len(cfg.Accounts) != 2 {
		t.Fatalf("config %+v", cfg)
	}

	out, _, code = runCLI(t, nil, "auth", "list")
	if code != 0 || !strings.Contains(out, "*        alice@digio.es  gmail") || !strings.Contains(out, "bob@digio.es    gmail,calendar,drive,chat") {
		t.Fatalf("list:\n%s", out)
	}

	if _, errOut, code := runCLI(t, nil, "auth", "use", "carol@digio.es"); code != 1 || !strings.Contains(errOut, "unknown account") {
		t.Fatalf("use unknown: %d %q", code, errOut)
	}
	if out, _, code := runCLI(t, nil, "auth", "use", "BOB@digio.es"); code != 0 || !strings.Contains(out, "bob@digio.es") {
		t.Fatalf("use: %d %q", code, out)
	}

	out, _, code = runCLI(t, nil, "auth", "status", "--json")
	if code != 0 {
		t.Fatalf("status code %d", code)
	}
	var st statusInfo
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if st.Account != "bob@digio.es" || !st.Default || st.Storage != "file" || !st.HasRefreshToken || !strings.HasPrefix(st.CredentialsSource, "config") {
		t.Fatalf("status %+v", st)
	}
	out, _, code = runCLI(t, nil, "auth", "status", "--account", "alice@digio.es")
	if code != 0 || !strings.Contains(out, "Services:") || !strings.Contains(out, "gmail") {
		t.Fatalf("status text:\n%s", out)
	}

	// Logout the default account: token revoked, removed, default promoted.
	app, outBuf, errBuf := newTestApp(t, nil)
	f := newFakeOAuth(t, "", "")
	f.configure(t, app)
	if code := app.Run(context.Background(), []string{"auth", "logout"}); code != 0 {
		t.Fatalf("logout: %s", errBuf.String())
	}
	if len(f.revoked) != 1 || f.revoked[0] != "rt-bob@digio.es" {
		t.Fatalf("revoked %v", f.revoked)
	}
	if !strings.Contains(outBuf.String(), "Default account is now alice@digio.es") {
		t.Fatalf("logout out %q", outBuf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "tokens", "bob@digio.es.json")); !os.IsNotExist(err) {
		t.Fatal("bob's token should be deleted")
	}

	if _, errOut, code := runCLI(t, nil, "auth", "logout", "nobody@digio.es", "--no-revoke"); code != 1 || !strings.Contains(errOut, "unknown account") {
		t.Fatalf("logout unknown: %d %q", code, errOut)
	}
	if _, errOut, code := runCLI(t, nil, "auth", "logout", "--all", "--no-revoke"); code != 0 {
		t.Fatalf("logout --all: %s", errOut)
	}
	if _, errOut, code := runCLI(t, nil, "auth", "status"); code != 1 || !strings.Contains(errOut, "gwork auth login") {
		t.Fatalf("status after logout: %d %q", code, errOut)
	}
}

func TestAuthLoginWithoutCredentials(t *testing.T) {
	isolateConfig(t)
	_, _, errOut, code := login(t, "a@digio.es", "openid")
	if code != 1 || !strings.Contains(errOut, "no OAuth client configured") || !strings.Contains(errOut, "hint:") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestProviderFromStore(t *testing.T) {
	dir := isolateConfig(t)
	writeClient(t, dir)
	gmailScopes := "openid " + auth.ScopeUserinfoEmail + " " + auth.ScopeGmailReadonly
	if _, _, errOut, code := login(t, "alice@digio.es", gmailScopes, "--services", "gmail"); code != 0 {
		t.Fatal(errOut)
	}
	app, _, _ := newTestApp(t, nil)
	p, err := app.Provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Account() != "alice@digio.es" {
		t.Fatalf("account %q", p.Account())
	}
	if _, err := app.ClientOptions(context.Background(), auth.Gmail); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ClientOptions(context.Background(), auth.Chat); err == nil {
		t.Fatal("chat was not granted")
	}
}
