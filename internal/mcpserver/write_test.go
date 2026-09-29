package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/googleapi"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

type noteIn struct {
	Body string `json:"body" jsonschema:"secret body"`
	Fail bool   `json:"fail,omitempty"`
	Deny bool   `json:"deny,omitempty"`
}

type noteOut struct {
	OK bool `json:"ok"`
}

// useTestWriteTool replaces the Gmail write registrar with one registering
// "gmail_test_note" for the duration of the test.
func useTestWriteTool(t *testing.T) {
	t.Helper()
	orig := writeRegistrars[auth.Gmail]
	t.Cleanup(func() { writeRegistrars[auth.Gmail] = orig })
	writeRegistrars[auth.Gmail] = func(s *mcp.Server, deps Deps) {
		destructive := false
		addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
			Name:        "gmail_test_note",
			Description: "test",
			Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: new(bool)},
		}, func(_ context.Context, in noteIn) (noteOut, error) {
			switch {
			case in.Deny:
				return noteOut{}, &googleapi.Error{Code: http.StatusForbidden, Message: "Request had insufficient authentication scopes.",
					Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}}
			case in.Fail:
				return noteOut{}, errors.New("boom")
			}
			return noteOut{OK: true}, nil
		})
	}
}

func TestWriteRegistrationGating(t *testing.T) {
	useTestWriteTool(t)
	cases := []struct {
		name      string
		provider  func(*testing.T) *testutil.FakeProvider
		write     []auth.Service
		services  []auth.Service
		wantTool  bool
		wantWrite []auth.Service
	}{
		{"no allow-write", func(t *testing.T) *testutil.FakeProvider { return testutil.NewFakeProvider(t, http.NotFoundHandler()) }, nil, nil, false, []auth.Service{}},
		{"allowed and granted", func(t *testing.T) *testutil.FakeProvider { return testutil.NewFakeProvider(t, http.NotFoundHandler()) }, []auth.Service{auth.Gmail}, nil, true, []auth.Service{auth.Gmail}},
		{"not write-granted", func(t *testing.T) *testutil.FakeProvider {
			return testutil.NewFakeProvider(t, http.NotFoundHandler()).GrantWrite(auth.Chat)
		}, []auth.Service{auth.Gmail}, nil, false, []auth.Service{}},
		{"other service allowed only", func(t *testing.T) *testutil.FakeProvider { return testutil.NewFakeProvider(t, http.NotFoundHandler()) }, []auth.Service{auth.Chat}, nil, false, []auth.Service{auth.Chat}},
		{"read tools not registered", func(t *testing.T) *testutil.FakeProvider { return testutil.NewFakeProvider(t, http.NotFoundHandler()) }, []auth.Service{auth.Gmail}, []auth.Service{auth.Drive}, false, []auth.Service{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			deps := Deps{Provider: c.provider(t), Logger: slog.New(slog.NewTextHandler(&logs, nil)), Write: WriteOptions{Services: c.write}}
			services := c.services
			if services == nil {
				services = auth.AllServices
			}
			cs, srv := newTestSession(t, deps, services...)
			_, has := listTools(t, cs)["gmail_test_note"]
			if has != c.wantTool {
				t.Fatalf("tool registered = %v, want %v", has, c.wantTool)
			}
			if !slices.Equal(srv.WriteEnabled, c.wantWrite) {
				t.Fatalf("WriteEnabled = %v", srv.WriteEnabled)
			}
			if c.name == "not write-granted" && !strings.Contains(logs.String(), "gwork auth login --services gmail --write gmail") {
				t.Errorf("missing warning with write hint: %s", logs.String())
			}
		})
	}
}

func TestAddWriteToolAnnotationsAndAudit(t *testing.T) {
	useTestWriteTool(t)
	var logs bytes.Buffer
	deps := Deps{
		Provider: testutil.NewFakeProvider(t, http.NotFoundHandler()),
		Logger:   slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Write:    WriteOptions{Services: []auth.Service{auth.Gmail}},
	}
	cs, _ := newTestSession(t, deps, auth.Gmail)
	tool := listTools(t, cs)["gmail_test_note"]
	if tool == nil {
		t.Fatal("tool missing")
	}
	a := tool.Annotations
	if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || !a.IdempotentHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
		t.Fatalf("annotations %+v", a)
	}

	logs.Reset()
	out, res := callTool[noteOut](t, cs, "gmail_test_note", map[string]any{"body": "TOP-SECRET-BODY"})
	if res.IsError || !out.OK {
		t.Fatalf("call failed: %s", resultText(res))
	}
	line := logs.String()
	for _, want := range []string{"level=INFO", "tool=gmail_test_note", "account=tester@digio.es", "service=gmail", "duration=", "ok=true"} {
		if !strings.Contains(line, want) {
			t.Errorf("audit line lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "TOP-SECRET-BODY") {
		t.Errorf("audit log leaked content: %s", line)
	}

	logs.Reset()
	_, res = callTool[noteOut](t, cs, "gmail_test_note", map[string]any{"body": "TOP-SECRET-BODY", "fail": true})
	if !res.IsError || !strings.Contains(logs.String(), "ok=false") || strings.Contains(logs.String(), "TOP-SECRET-BODY") {
		t.Fatalf("failed call: %v / %s", res.IsError, logs.String())
	}

	_, res = callTool[noteOut](t, cs, "gmail_test_note", map[string]any{"body": "x", "deny": true})
	if !res.IsError || !strings.Contains(resultText(res), "gwork auth login --services gmail --write gmail") {
		t.Fatalf("expected write hint, got %q", resultText(res))
	}
}

func TestWhoamiWriteFields(t *testing.T) {
	useTestWriteTool(t)
	for _, send := range []bool{false, true} {
		deps := Deps{
			Provider: testutil.NewFakeProvider(t, http.NotFoundHandler()),
			Write:    WriteOptions{Services: []auth.Service{auth.Gmail}, AllowSend: send},
		}
		cs, _ := newTestSession(t, deps, auth.Gmail)
		out, res := callTool[WhoamiOutput](t, cs, "whoami", map[string]any{})
		if res.IsError || !slices.Equal(out.WriteServices, []string{"gmail"}) || out.AllowSend != send {
			t.Errorf("send=%v: %+v", send, out)
		}
	}
	cs, _ := newTestSession(t, testDeps(t, http.NotFoundHandler()), auth.Gmail)
	out, _ := callTool[WhoamiOutput](t, cs, "whoami", map[string]any{})
	if out.WriteServices == nil || len(out.WriteServices) != 0 || out.AllowSend {
		t.Errorf("read-only: %+v", out)
	}
}

func TestServerInstructionsWrite(t *testing.T) {
	useTestWriteTool(t)
	readOnly := New(testDeps(t, http.NotFoundHandler()), []auth.Service{auth.Gmail})
	deps := testDeps(t, http.NotFoundHandler())
	deps.Write.Services = []auth.Service{auth.Gmail}
	rw := New(deps, []auth.Service{auth.Gmail})
	cs := connect(t, readOnly)
	if got := cs.InitializeResult().Instructions; !strings.HasPrefix(got, "Read-only access") {
		t.Errorf("read-only instructions changed: %q", got)
	}
	cs = connect(t, rw)
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "explicit confirmation") || !strings.Contains(got, "drafts") {
		t.Errorf("write instructions: %q", got)
	}
}

func connect(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.MCP.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestDepsWriteClientOptions(t *testing.T) {
	p := testutil.NewFakeProvider(t, http.NotFoundHandler()).GrantWrite(auth.Gmail)
	d := Deps{Provider: p}
	if _, err := d.WriteClientOptions(context.Background(), auth.Gmail); err != nil {
		t.Fatal(err)
	}
	_, err := d.WriteClientOptions(context.Background(), auth.Chat)
	if !errors.Is(err, auth.ErrInsufficientScope) || auth.HintFor(err) != "run: gwork auth login --services chat --write chat" {
		t.Fatalf("err %v", err)
	}
	if _, err := (Deps{}).WriteClientOptions(context.Background(), auth.Gmail); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("no provider: %v", err)
	}
}
