package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
)

// SetupDocHint points at the Google Cloud setup guide.
const SetupDocHint = "see docs/setup-google-cloud.md"

// Sentinel error kinds. Use errors.Is to test for them; the concrete error
// is an *Error carrying a message and an actionable hint.
var (
	// ErrNotLoggedIn means there is no stored token for the account.
	ErrNotLoggedIn = errors.New("not logged in")
	// ErrNoCredentials means no OAuth client could be resolved.
	ErrNoCredentials = errors.New("no OAuth client credentials")
	// ErrReauthRequired means the refresh token is no longer valid
	// (revoked, expired by session control policy, password change...).
	ErrReauthRequired = errors.New("re-authentication required")
	// ErrOrgInternal means the account is not allowed by the Internal OAuth
	// app (outside the organization) or the user denied consent.
	ErrOrgInternal = errors.New("account not allowed")
	// ErrInsufficientScope means the token lacks the scopes of a service.
	ErrInsufficientScope = errors.New("insufficient OAuth scopes")
	// ErrAPIDisabled means the API is not enabled in the Cloud project or
	// has been blocked by an administrator.
	ErrAPIDisabled = errors.New("API not enabled or blocked")
	// ErrNotFound means the requested resource does not exist or is not
	// visible to the account.
	ErrNotFound = errors.New("not found")
	// ErrRateLimited means the request was throttled by Google.
	ErrRateLimited = errors.New("rate limited")
	// ErrPermissionDenied is any other 403 from a Google API.
	ErrPermissionDenied = errors.New("permission denied")
)

// Error is a classified error with an actionable hint.
type Error struct {
	// Kind is one of the sentinel errors above.
	Kind error
	// Message is a human readable description.
	Message string
	// Hint tells the user what to do next, e.g. "run: gwork auth login".
	Hint string
	// Err is the underlying error, if any.
	Err error
}

// Error implements error. The hint is appended on a new line.
func (e *Error) Error() string {
	msg := e.Message
	if msg == "" && e.Kind != nil {
		msg = e.Kind.Error()
	}
	if e.Hint != "" {
		msg += "\nhint: " + e.Hint
	}
	return msg
}

// Unwrap exposes both the kind and the underlying error to errors.Is/As.
func (e *Error) Unwrap() []error {
	var errs []error
	if e.Kind != nil {
		errs = append(errs, e.Kind)
	}
	if e.Err != nil {
		errs = append(errs, e.Err)
	}
	return errs
}

// HintFor returns the hint of a classified error, or "".
func HintFor(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}

// LoginHint returns the command that grants the given services.
func LoginHint(svcs ...Service) string {
	if len(svcs) == 0 {
		return "run: gwork auth login"
	}
	return "run: gwork auth login --services " + JoinServices(svcs)
}

// NewScopeError reports that the account lacks the scopes of svc.
func NewScopeError(account string, svc Service) error {
	who := "the current account"
	if account != "" {
		who = account
	}
	return &Error{
		Kind:    ErrInsufficientScope,
		Message: fmt.Sprintf("%s has not granted read access to %s", who, svc),
		Hint:    LoginHint(svc),
	}
}

// Classify maps OAuth and Google API errors to *Error values with hints.
// It is equivalent to ClassifyService(err, "").
func Classify(err error) error {
	return ClassifyService(err, "")
}

// ClassifyService maps OAuth and Google API errors to *Error values with
// hints, using svc (may be empty) to tailor scope hints. Errors that are
// already classified, and errors it does not recognize, are returned
// unchanged. nil stays nil.
func ClassifyService(err error, svc Service) error {
	if err == nil {
		return nil
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}

	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return classifyOAuth(err, errContext(err, re), re.ErrorCode, re.ErrorDescription)
	}

	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return classifyAPI(err, errContext(err, ge), ge, svc)
	}
	return err
}

// errContext returns the context that wrapping added in front of the
// Google error cause, e.g. "get message 18a" for
// fmt.Errorf("get message 18a: %w", apiErr), so a classified message can
// keep saying what failed. A *url.Error in the chain (token refresh
// failures) is treated as part of the cause, since its method and URL are
// noise for users. It returns "" when the chain adds no leading context.
func errContext(err, cause error) string {
	var ue *url.Error
	if errors.As(err, &ue) && errors.Is(ue, cause) {
		cause = ue
	}
	full, tail := err.Error(), cause.Error()
	if !strings.HasSuffix(full, tail) {
		return ""
	}
	return strings.TrimRight(strings.TrimSuffix(full, tail), ": \t\n")
}

// withContext joins a summary, the wrapping context (may be empty) and the
// Google detail as "summary: context: detail".
func withContext(summary, ctx, detail string) string {
	if ctx == "" {
		return summary + ": " + detail
	}
	return summary + ": " + ctx + ": " + detail
}

// classifyOAuth handles OAuth error codes from the token endpoint or the
// authorization callback.
func classifyOAuth(err error, ctx, code, desc string) error {
	detail := code
	if desc != "" {
		detail += ": " + desc
	}
	if ctx != "" {
		detail = ctx + ": " + detail
	}
	switch code {
	case "invalid_grant":
		return &Error{
			Kind:    ErrReauthRequired,
			Message: "the stored authorization is no longer valid (" + detail + ")",
			Hint:    "run: gwork auth login",
			Err:     err,
		}
	case "org_internal", "access_denied", "admin_policy_enforced":
		return &Error{
			Kind:    ErrOrgInternal,
			Message: "Google refused the login (" + detail + "); only accounts of the organization that owns the OAuth app can sign in, and consent must be granted",
			Hint:    "sign in with your organization account (e.g. @digio.es) and accept the requested permissions",
			Err:     err,
		}
	case "invalid_client", "unauthorized_client", "deleted_client":
		return &Error{
			Kind:    ErrNoCredentials,
			Message: "the OAuth client was rejected (" + detail + ")",
			Hint:    "check --credentials / GWORK_CREDENTIALS; " + SetupDocHint,
			Err:     err,
		}
	}
	return err
}

// classifyAPI handles googleapi.Error responses.
func classifyAPI(err error, ctx string, ge *googleapi.Error, svc Service) error {
	reasons := apiReasons(ge)
	msg := ge.Message
	if msg == "" {
		msg = http.StatusText(ge.Code)
	}
	has := func(rs ...string) bool {
		for _, r := range rs {
			for _, got := range reasons {
				if strings.EqualFold(got, r) {
					return true
				}
			}
		}
		return false
	}

	switch {
	case ge.Code == http.StatusUnauthorized:
		return &Error{Kind: ErrReauthRequired, Message: withContext("Google rejected the credentials", ctx, msg), Hint: "run: gwork auth login", Err: err}
	case has("insufficientPermissions", "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
		strings.Contains(strings.ToLower(msg), "insufficient authentication scopes"):
		hint := "run: gwork auth login --services <service>"
		if svc != "" {
			hint = LoginHint(svc)
		}
		return &Error{Kind: ErrInsufficientScope, Message: withContext("missing OAuth scope", ctx, msg), Hint: hint, Err: err}
	case has("accessNotConfigured", "SERVICE_DISABLED", "API_DISABLED") ||
		strings.Contains(ge.Body, "SERVICE_DISABLED") || strings.Contains(msg, "has not been used in project"):
		return &Error{Kind: ErrAPIDisabled, Message: withContext("the API is not enabled for the OAuth client's project", ctx, msg), Hint: SetupDocHint, Err: err}
	case ge.Code == http.StatusTooManyRequests || has("rateLimitExceeded", "userRateLimitExceeded", "RATE_LIMIT_EXCEEDED", "quotaExceeded"):
		return &Error{Kind: ErrRateLimited, Message: withContext("Google API rate limit exceeded", ctx, msg), Hint: "wait a moment and retry, or reduce --max", Err: err}
	case ge.Code == http.StatusNotFound:
		return &Error{Kind: ErrNotFound, Message: withContext("not found", ctx, msg), Hint: "check the ID and that your account can access it", Err: err}
	case ge.Code == http.StatusForbidden:
		return &Error{Kind: ErrPermissionDenied, Message: withContext("permission denied", ctx, msg), Hint: "the resource may be restricted, or an administrator may have blocked this app; " + SetupDocHint, Err: err}
	}
	return err
}

// apiReasons collects the legacy "errors[].reason" values and the
// google.rpc.ErrorInfo "reason" values found in Details.
func apiReasons(ge *googleapi.Error) []string {
	var out []string
	for _, it := range ge.Errors {
		if it.Reason != "" {
			out = append(out, it.Reason)
		}
	}
	for _, d := range ge.Details {
		if m, ok := d.(map[string]any); ok {
			if r, ok := m["reason"].(string); ok && r != "" {
				out = append(out, r)
			}
		}
	}
	// Some responses only carry details in the raw body.
	if len(ge.Details) == 0 && ge.Body != "" {
		var body struct {
			Error struct {
				Details []struct {
					Reason string `json:"reason"`
				} `json:"details"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(ge.Body), &body) == nil {
			for _, d := range body.Error.Details {
				if d.Reason != "" {
					out = append(out, d.Reason)
				}
			}
		}
	}
	return out
}
