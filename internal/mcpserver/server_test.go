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

	"github.com/madeindigio/gwork/internal/auth"
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

type deadlineInput struct{}

type deadlineOutput struct {
	HasDeadline bool          `json:"has_deadline"`
	Remaining   time.Duration `json:"remaining"`
}

func TestToolTimeout(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		want    bool
		atMost  time.Duration
	}{
		{"zero uses default", 0, true, DefaultToolTimeout},
		{"explicit", 5 * time.Second, true, 5 * time.Second},
		{"negative disables", NoToolTimeout, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := testDeps(t, http.NotFoundHandler())
			deps.Timeout = c.timeout
			srv := New(deps, nil)
			addReadOnlyTool(srv.MCP, deps, "", &mcp.Tool{Name: "deadline"}, func(ctx context.Context, _ deadlineInput) (deadlineOutput, error) {
				d, ok := ctx.Deadline()
				if !ok {
					return deadlineOutput{}, nil
				}
				return deadlineOutput{HasDeadline: true, Remaining: time.Until(d)}, nil
			})
			serverT, clientT := mcp.NewInMemoryTransports()
			ss, err := srv.MCP.Connect(context.Background(), serverT, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ss.Close() })
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(context.Background(), clientT, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cs.Close() })
			out, res := callTool[deadlineOutput](t, cs, "deadline", map[string]any{})
			if res.IsError {
				t.Fatal(resultText(res))
			}
			if out.HasDeadline != c.want {
				t.Fatalf("has deadline = %v, want %v", out.HasDeadline, c.want)
			}
			if c.want && (out.Remaining > c.atMost || out.Remaining < c.atMost-time.Minute/2) {
				t.Errorf("remaining = %v, want about %v", out.Remaining, c.atMost)
			}
		})
	}
}
