// Package testutil provides fakes shared by tests: a fake Google API server
// and a fake auth.ClientProvider pointing API clients at it.
//
// Typical use in a workspace package test:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
//		testutil.WriteJSON(t, w, map[string]any{"labels": []any{}})
//	})
//	svc, err := gmail.New(ctx, testutil.FakeGoogle(t, mux)...)
//
// It must only be imported from _test.go files.
package testutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"google.golang.org/api/option"

	"github.com/digio/gwork-cli/internal/auth"
)

// FakeGoogle starts an httptest server serving h and returns client options
// that make any google.golang.org/api client talk to it without
// authentication. The server is closed when the test ends.
//
// The endpoint replaces each generated client's base path, so the paths the
// fake server sees are:
//
//	Gmail:    /gmail/v1/users/me/messages, /gmail/v1/users/me/labels, ...
//	Chat:     /v1/spaces, /v1/spaces/{space}/messages, ...
//	Drive:    /files, /files/{id}, /files/{id}/export, ...
//	Calendar: /users/me/calendarList, /calendars/{id}/events, ...
//
// Drive and Calendar have no version prefix because /drive/v3/ and
// /calendar/v3/ are part of their base paths, which the endpoint replaces.
func FakeGoogle(t testing.TB, h http.Handler) []option.ClientOption {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return ServerOptions(srv)
}

// ServerOptions returns the client options for an already running server.
func ServerOptions(srv *httptest.Server) []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(srv.URL + "/"),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	}
}

// WriteJSON writes v as a JSON response with status 200.
func WriteJSON(t testing.TB, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("write json: %v", err)
	}
}

// WriteGoogleError writes a Google-style JSON error response, e.g.
// WriteGoogleError(w, 404, "notFound", "Requested entity was not found.").
func WriteGoogleError(w http.ResponseWriter, code int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"errors":  []any{map[string]any{"reason": reason, "message": message}},
		},
	})
}

// FakeProvider is an auth.ClientProvider for tests.
type FakeProvider struct {
	// Email is returned by Account.
	Email string
	// Services are the granted services; ClientOptions fails for others
	// with the same error as the real provider.
	Services []auth.Service
	// Options are returned by ClientOptions.
	Options []option.ClientOption
	// Err, when set, is returned by ClientOptions.
	Err error
}

var _ auth.ClientProvider = (*FakeProvider)(nil)

// NewFakeProvider returns a FakeProvider for "tester@digio.es" whose clients
// talk to a fake server serving h. With no services, all are granted.
func NewFakeProvider(t testing.TB, h http.Handler, services ...auth.Service) *FakeProvider {
	t.Helper()
	if len(services) == 0 {
		services = slices.Clone(auth.AllServices)
	}
	return &FakeProvider{Email: "tester@digio.es", Services: services, Options: FakeGoogle(t, h)}
}

// ClientOptions implements auth.ClientProvider.
func (f *FakeProvider) ClientOptions(_ context.Context, svc auth.Service) ([]option.ClientOption, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if !slices.Contains(f.Services, svc) {
		return nil, auth.NewScopeError(f.Email, svc)
	}
	return slices.Clone(f.Options), nil
}

// Account implements auth.ClientProvider.
func (f *FakeProvider) Account() string { return f.Email }

// GrantedServices implements auth.ClientProvider.
func (f *FakeProvider) GrantedServices() []auth.Service { return slices.Clone(f.Services) }
