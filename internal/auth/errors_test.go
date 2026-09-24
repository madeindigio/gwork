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
		name   string
		err    *googleapi.Error
		kind   error
		hint   string
		prefix string // expected start of the message, before the wrapping context
	}{
		{
			name: "insufficient permissions",
			err: &googleapi.Error{Code: 403, Message: "Request had insufficient authentication scopes.",
				Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}},
			kind:   ErrInsufficientScope,
			hint:   "gwork auth login --services chat",
			prefix: "missing OAuth scope",
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
			kind:   ErrAPIDisabled,
			hint:   "setup-google-cloud.md",
			prefix: "the API is not enabled for the OAuth client's project",
		},
		{
			name: "access not configured",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}},
			kind: ErrAPIDisabled,
		},
		{
			name:   "chat app not configured",
			err:    &googleapi.Error{Code: 404, Message: "Google Chat app not found. To create a Chat app, you must turn on the Chat API and configure the app in the Google Cloud console."},
			kind:   ErrAPIDisabled,
			hint:   "setup-google-cloud.md, section 5",
			prefix: "the Google Chat API is not configured for the OAuth client's project",
		},
		{name: "not found", err: &googleapi.Error{Code: 404, Message: "Requested entity was not found."}, kind: ErrNotFound, prefix: "not found"},
		{name: "rate limit", err: &googleapi.Error{Code: 429, Message: "Too many requests"}, kind: ErrRateLimited, prefix: "Google API rate limit exceeded"},
		{name: "unauthorized", err: &googleapi.Error{Code: 401, Message: "Invalid Credentials"}, kind: ErrReauthRequired, prefix: "Google rejected the credentials"},
		{name: "other 403", err: &googleapi.Error{Code: 403, Message: "The caller does not have permission"}, kind: ErrPermissionDenied, prefix: "permission denied"},
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
			// The wrapping context and Google's message survive
			// classification.
			var ae *Error
			if !errors.As(err, &ae) {
				t.Fatalf("not an *Error: %T", err)
			}
			if !strings.Contains(ae.Message, "list spaces: ") {
				t.Errorf("message %q lost the wrapping context", ae.Message)
			}
			if c.err.Message != "" && !strings.HasSuffix(ae.Message, c.err.Message) {
				t.Errorf("message %q lost Google's message %q", ae.Message, c.err.Message)
			}
			if c.prefix != "" && !strings.HasPrefix(ae.Message, c.prefix+": list spaces: ") {
				t.Errorf("message %q, want prefix %q", ae.Message, c.prefix+": list spaces: ")
			}
		})
	}
}

// classifiedMessage returns the Message of a classified error.
func classifiedMessage(t *testing.T, err error) string {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("not an *Error: %T %v", err, err)
	}
	return ae.Message
}

func TestClassifyKeepsContext(t *testing.T) {
	ge := &googleapi.Error{Code: 404, Message: "Requested entity was not found."}
	err := ClassifyService(fmt.Errorf("get message 18a: %w", ge), Gmail)
	want := "not found: get message 18a: Requested entity was not found."
	if !strings.HasPrefix(err.Error(), want+"\nhint: ") {
		t.Errorf("got %q, want prefix %q", err.Error(), want)
	}

	// Without wrapping there is no context to keep.
	if got := classifiedMessage(t, ClassifyService(ge, Gmail)); got != "not found: Requested entity was not found." {
		t.Errorf("unwrapped: %q", got)
	}

	// Context added after the cause is not a prefix and is dropped.
	if got := classifiedMessage(t, ClassifyService(fmt.Errorf("%w (while listing)", ge), Gmail)); got != "not found: Requested entity was not found." {
		t.Errorf("suffix context: %q", got)
	}

	// The method and URL of a *url.Error are dropped, the caller's
	// context is kept.
	re := &oauth2.RetrieveError{ErrorCode: "invalid_grant", ErrorDescription: "Token has been expired or revoked."}
	wrapped := fmt.Errorf("list spaces: %w", &url.Error{Op: "Get", URL: "https://chat.googleapis.com/v1/spaces", Err: re})
	msg := classifiedMessage(t, Classify(wrapped))
	if !strings.Contains(msg, "(list spaces: invalid_grant: Token has been expired or revoked.)") || strings.Contains(msg, "https://") {
		t.Errorf("oauth message %q", msg)
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
