package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"google.golang.org/api/googleapi"
)

func TestParseWriteServices(t *testing.T) {
	cases := []struct {
		in      string
		want    []Service
		wantErr string
	}{
		{"", []Service{}, ""},
		{"none", []Service{}, ""},
		{" NONE ", []Service{}, ""},
		{"all", WritableServices, ""},
		{"chat,gmail", []Service{Gmail, Chat}, ""},
		{"gmail, gmail ,Calendar", []Service{Gmail, Calendar}, ""},
		{"gmail,all", WritableServices, ""},
		{"drive", nil, "does not support write"},
		{"gmail,drive", nil, "does not support write"},
		{"nope", nil, "unknown service"},
	}
	for _, c := range cases {
		got, err := ParseWriteServices(c.in)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: err = %v, want %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil || got == nil || !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestWritableAndScopes(t *testing.T) {
	for _, svc := range AllServices {
		if got, want := svc.Writable(), svc != Drive; got != want {
			t.Errorf("%s.Writable() = %v", svc, got)
		}
	}
	if got := Gmail.WriteScopes(); !slices.Equal(got, []string{ScopeGmailModify}) {
		t.Errorf("gmail write scopes %v", got)
	}
	if Drive.WriteScopes() != nil {
		t.Error("drive must have no write scopes")
	}
	got := ScopesForWrite([]Service{Drive}, []Service{Calendar})
	for _, want := range []string{ScopeOpenID, ScopeDriveReadonly, ScopeCalendarReadonly, ScopeCalendarEvents} {
		if !slices.Contains(got, want) {
			t.Errorf("ScopesForWrite missing %s: %v", want, got)
		}
	}
	if slices.Contains(got, ScopeGmailModify) {
		t.Errorf("unexpected gmail scope: %v", got)
	}
}

func TestMissingWriteScopesAndGranted(t *testing.T) {
	read := ScopesFor(AllServices)
	all := ScopesForWrite(AllServices, WritableServices)
	cases := []struct {
		name        string
		granted     []string
		svc         Service
		wantMissing []string
		wantWrite   []Service
	}{
		{"read only", read, Gmail, []string{ScopeGmailModify}, []Service{}},
		{"all", all, Chat, nil, []Service{Gmail, Calendar, Chat}},
		{"write without read", []string{ScopeCalendarEvents}, Calendar, []string{ScopeCalendarReadonly}, []Service{}},
		{"partial", []string{ScopeGmailReadonly, ScopeGmailModify, ScopeCalendarEvents}, Calendar, []string{ScopeCalendarReadonly}, []Service{Gmail}},
	}
	for _, c := range cases {
		if got := MissingWriteScopes(c.granted, c.svc); !slices.Equal(got, c.wantMissing) {
			t.Errorf("%s: missing %v, want %v", c.name, got, c.wantMissing)
		}
		if got := WriteGrantedServices(c.granted); !slices.Equal(got, c.wantWrite) {
			t.Errorf("%s: write granted %v, want %v", c.name, got, c.wantWrite)
		}
		st := &StoredToken{Scopes: c.granted}
		if got := st.WriteServices(); !slices.Equal(got, c.wantWrite) {
			t.Errorf("%s: StoredToken.WriteServices %v", c.name, got)
		}
	}
}

func TestLoginRequestsWriteScopes(t *testing.T) {
	f := newFakeGoogleAuth(t)
	var requested string
	_, err := Login(context.Background(), LoginOptions{
		Credentials:   testCreds(),
		Services:      []Service{Drive},
		WriteServices: []Service{Calendar},
		Endpoint:      f.endpoint(),
		UserinfoURL:   f.srv.URL + "/userinfo",
		OpenBrowser: func(authURL string) error {
			u, _ := url.Parse(authURL)
			requested = u.Query().Get("scope")
			return simulateBrowser(t, nil)(authURL)
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	scopes := strings.Fields(requested)
	for _, want := range []string{ScopeDriveReadonly, ScopeCalendarReadonly, ScopeCalendarEvents} {
		if !slices.Contains(scopes, want) {
			t.Errorf("scope %s not requested: %v", want, scopes)
		}
	}
	if slices.Contains(scopes, ScopeGmailModify) || slices.Contains(scopes, ScopeGmailReadonly) {
		t.Errorf("gmail scopes must not be requested: %v", scopes)
	}
}

func TestStoreProviderWriteClientOptions(t *testing.T) {
	keyring.MockInit()
	store := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	_ = store.Save(expiredToken("a@digio.es", ScopeGmailReadonly, ScopeGmailModify, ScopeCalendarReadonly, ScopeChatMessagesCreate))
	p, err := NewStoreProvider(context.Background(), ProviderOptions{Account: "a@digio.es", Store: store, Credentials: testCreds()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.WriteClientOptions(ctx, Gmail); err != nil {
		t.Fatalf("gmail write: %v", err)
	}
	for _, svc := range []Service{Calendar, Chat} { // calendar lacks write, chat lacks read
		_, err := p.WriteClientOptions(ctx, svc)
		if !errors.Is(err, ErrInsufficientScope) {
			t.Fatalf("%s: expected ErrInsufficientScope, got %v", svc, err)
		}
		if want := "run: gwork auth login --services " + string(svc) + " --write " + string(svc); HintFor(err) != want {
			t.Errorf("%s: hint %q, want %q", svc, HintFor(err), want)
		}
	}
	if _, err := p.WriteClientOptions(ctx, Drive); err == nil {
		t.Error("drive write must fail")
	}
	if g := p.WriteGrantedServices(); !slices.Equal(g, []Service{Gmail}) {
		t.Errorf("write granted %v", g)
	}
}

func TestClassifyWrite(t *testing.T) {
	apiErr := &googleapi.Error{Code: http.StatusForbidden, Message: "Request had insufficient authentication scopes.",
		Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}}
	wrapped := errors.Join(errors.New("send"), apiErr)

	w := ClassifyWrite(wrapped, Gmail)
	if !errors.Is(w, ErrInsufficientScope) || HintFor(w) != "run: gwork auth login --services gmail --write gmail" {
		t.Errorf("write: %v (hint %q)", w, HintFor(w))
	}
	r := ClassifyService(wrapped, Gmail)
	if HintFor(r) != "run: gwork auth login --services gmail" {
		t.Errorf("read hint changed: %q", HintFor(r))
	}
	if ClassifyWrite(nil, Gmail) != nil {
		t.Error("nil must stay nil")
	}
	already := NewWriteScopeError("a@digio.es", Chat)
	if !errors.Is(ClassifyWrite(already, Gmail), already) {
		t.Error("already classified errors must be returned unchanged")
	}
}
