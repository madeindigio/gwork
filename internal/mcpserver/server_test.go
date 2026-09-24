package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/googleapi"

	"github.com/digio/gwork-cli/internal/auth"
)

func TestWhoami(t *testing.T) {
	deps := testDeps(t, http.NotFoundHandler(), auth.Gmail, auth.Drive)
	cs, srv := newTestSession(t, deps, auth.Gmail, auth.Chat)

	if !slices.Equal(srv.Enabled, []auth.Service{auth.Gmail}) {
		t.Fatalf("enabled = %v (chat is not granted, drive not requested)", srv.Enabled)
	}

	tools := listTools(t, cs)
	who, ok := tools["whoami"]
	if !ok {
		t.Fatalf("whoami not registered: %v", tools)
	}
	if who.Annotations == nil || !who.Annotations.ReadOnlyHint {
		t.Fatal("whoami must be annotated readOnlyHint")
	}

	out, res := callTool[WhoamiOutput](t, cs, "whoami", map[string]any{})
	if res.IsError {
		t.Fatalf("whoami error: %s", resultText(res))
	}
	if out.Account != "tester@digio.es" {
		t.Fatalf("account %q", out.Account)
	}
	if !slices.Equal(out.GrantedServices, []string{"gmail", "drive"}) || !slices.Equal(out.EnabledServices, []string{"gmail"}) {
		t.Fatalf("unexpected output %+v", out)
	}
}

func TestWhoamiNotLoggedIn(t *testing.T) {
	deps := Deps{ProviderErr: &auth.Error{Kind: auth.ErrNotLoggedIn, Message: "no stored credentials", Hint: "run: gwork auth login"}}
	cs, srv := newTestSession(t, deps)
	if len(srv.Enabled) != 0 {
		t.Fatalf("no services should be enabled, got %v", srv.Enabled)
	}
	_, res := callTool[WhoamiOutput](t, cs, "whoami", nil)
	if !res.IsError || !strings.Contains(resultText(res), "gwork auth login") {
		t.Fatalf("expected tool error with hint, got %+v", res)
	}
}

type echoIn struct {
	Text     string `json:"text" jsonschema:"text to echo"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"maximum characters to return"`
	Fail     bool   `json:"fail,omitempty"`
}

type echoOut struct {
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
	At        time.Time `json:"at"`
}

// TestAddReadOnlyTool demonstrates the pattern Phase 2 service files use.
func TestAddReadOnlyTool(t *testing.T) {
	deps := testDeps(t, http.NotFoundHandler())
	s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{Name: "echo", Description: "echo"},
		func(ctx context.Context, in echoIn) (echoOut, error) {
			if _, ok := ctx.Deadline(); !ok {
				return echoOut{}, errors.New("tool context must carry a deadline")
			}
			if in.Fail {
				return echoOut{}, &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}}
			}
			text, truncated := TruncateText(in.Text, effectiveMaxChars(in.MaxChars))
			return echoOut{Text: text, Truncated: truncated, At: deps.CurrentTime()}, nil
		})

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tool := listTools(t, cs)["echo"]
	if tool == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatal("echo must be registered read-only")
	}

	out, res := callTool[echoOut](t, cs, "echo", map[string]any{"text": "héllo world", "max_chars": 4})
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(res))
	}
	if out.Text != "héll" || !out.Truncated || !out.At.Equal(testNow) {
		t.Fatalf("unexpected output %+v", out)
	}

	_, res = callTool[echoOut](t, cs, "echo", map[string]any{"text": "x", "fail": true})
	if !res.IsError || !strings.Contains(resultText(res), "gwork auth login --services chat") {
		t.Fatalf("expected classified scope error, got %q", resultText(res))
	}

	// Schema validation rejects wrong types before reaching the handler.
	_, res = callTool[echoOut](t, cs, "echo", map[string]any{"text": 5})
	if !res.IsError {
		t.Fatal("expected validation error")
	}
}

func TestTruncateText(t *testing.T) {
	cases := []struct {
		in    string
		max   int
		want  string
		trunc bool
	}{
		{"hello", 10, "hello", false},
		{"hello", 5, "hello", false},
		{"hello", 3, "hel", true},
		{"ñandú", 2, "ña", true},
		{"hello", 0, "hello", false},
		{"", 3, "", false},
	}
	for _, c := range cases {
		got, trunc := TruncateText(c.in, c.max)
		if got != c.want || trunc != c.trunc {
			t.Errorf("TruncateText(%q,%d) = %q,%v want %q,%v", c.in, c.max, got, trunc, c.want, c.trunc)
		}
	}
	if effectiveMaxChars(0) != DefaultMaxChars || effectiveMaxChars(7) != 7 {
		t.Fatal("effectiveMaxChars mismatch")
	}
}
