package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

func TestClassifyOAuth(t *testing.T) {
	re := &oauth2.RetrieveError{ErrorCode: "invalid_grant", ErrorDescription: "Token has been expired or revoked."}
	// Token refresh errors typically arrive wrapped in a *url.Error.
	wrapped := &url.Error{Op: "Get", URL: "https://gmail.googleapis.com", Err: re}
	err := Classify(wrapped)
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("expected ErrReauthRequired, got %v", err)
	}
	var got *oauth2.RetrieveError
	if !errors.As(err, &got) {
		t.Fatal("underlying RetrieveError should remain reachable")
	}
	if !strings.Contains(err.Error(), "gwork auth login") {
		t.Fatalf("missing hint: %q", err.Error())
	}

	for _, code := range []string{"org_internal", "access_denied"} {
		err := Classify(&oauth2.RetrieveError{ErrorCode: code})
		if !errors.Is(err, ErrOrgInternal) {
			t.Errorf("%s: got %v", code, err)
		}
	}
	if err := Classify(&oauth2.RetrieveError{ErrorCode: "invalid_client"}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("invalid_client: got %v", err)
	}
}

func TestClassifyAPI(t *testing.T) {
	cases := []struct {
		name string
		err  *googleapi.Error
		kind error
		hint string
	}{
		{
			name: "insufficient permissions",
			err: &googleapi.Error{Code: 403, Message: "Request had insufficient authentication scopes.",
				Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}},
			kind: ErrInsufficientScope,
			hint: "gwork auth login --services chat",
		},
		{
			name: "scope insufficient via details",
			err: &googleapi.Error{Code: 403, Message: "denied",
				Details: []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "ACCESS_TOKEN_SCOPE_INSUFFICIENT"}}},
			kind: ErrInsufficientScope,
		},
		{
			name: "service disabled",
			err: &googleapi.Error{Code: 403, Message: "Google Chat API has not been used in project 123 before or it is disabled.",
				Body: `{"error":{"details":[{"reason":"SERVICE_DISABLED"}]}}`},
			kind: ErrAPIDisabled,
			hint: "setup-google-cloud.md",
		},
		{
			name: "access not configured",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}},
			kind: ErrAPIDisabled,
		},
		{name: "not found", err: &googleapi.Error{Code: 404, Message: "Requested entity was not found."}, kind: ErrNotFound},
		{name: "rate limit", err: &googleapi.Error{Code: 429, Message: "Too many requests"}, kind: ErrRateLimited},
		{name: "unauthorized", err: &googleapi.Error{Code: 401, Message: "Invalid Credentials"}, kind: ErrReauthRequired},
		{name: "other 403", err: &googleapi.Error{Code: 403, Message: "The caller does not have permission"}, kind: ErrPermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ClassifyService(fmt.Errorf("list spaces: %w", c.err), Chat)
			if !errors.Is(err, c.kind) {
				t.Fatalf("got %v, want kind %v", err, c.kind)
			}
			var ge *googleapi.Error
			if !errors.As(err, &ge) {
				t.Fatal("googleapi.Error should remain reachable")
			}
			if c.hint != "" && !strings.Contains(HintFor(err), c.hint) {
				t.Fatalf("hint %q does not contain %q", HintFor(err), c.hint)
			}
		})
	}
}

func TestClassifyPassThrough(t *testing.T) {
	if Classify(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	plain := errors.New("boom")
	if err := Classify(plain); !errors.Is(err, plain) || err.Error() != "boom" {
		t.Fatal("unknown errors must be returned unchanged")
	}
	if err := Classify(&googleapi.Error{Code: http.StatusInternalServerError}); errors.Is(err, ErrNotFound) {
		t.Fatal("500 should not be classified as not found")
	}
	classified := NewScopeError("a@digio.es", Drive)
	if err := Classify(classified); err.Error() != classified.Error() || !errors.Is(err, ErrInsufficientScope) {
		t.Fatal("already classified errors must be returned unchanged")
	}
	if !strings.Contains(classified.Error(), "--services drive") {
		t.Fatalf("scope error: %q", classified.Error())
	}
}
