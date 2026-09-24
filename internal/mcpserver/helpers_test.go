package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
)

// testNow is the fixed clock used by testDeps.
var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// testDeps returns Deps backed by a fake provider whose API clients talk to
// h. With no services, all services are granted.
func testDeps(t *testing.T, h http.Handler, granted ...auth.Service) Deps {
	t.Helper()
	return Deps{
		Provider: testutil.NewFakeProvider(t, h, granted...),
		Timeout:  10 * time.Second,
		Now:      func() time.Time { return testNow },
	}
}

// newTestSession builds the server with deps and the requested services
// (all when empty) and connects an in-memory MCP client to it.
func newTestSession(t *testing.T, deps Deps, services ...auth.Service) (*mcp.ClientSession, *Server) {
	t.Helper()
	if len(services) == 0 {
		services = auth.AllServices
	}
	srv := New(deps, services)
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := srv.MCP.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, srv
}

// callTool calls a tool and decodes its structured output into Out. It
// fails the test on protocol errors; tool errors are returned in the result
// (res.IsError) with a zero Out.
func callTool[Out any](t *testing.T, cs *mcp.ClientSession, name string, args any) (Out, *mcp.CallToolResult) {
	t.Helper()
	var out Out
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError || res.StructuredContent == nil {
		return out, res
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s output: %v\n%s", name, err, data)
	}
	return out, res
}

// resultText concatenates the text content of a tool result (useful to
// check error messages).
func resultText(res *mcp.CallToolResult) string {
	var s string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			s += tc.Text
		}
	}
	return s
}

// listTools returns the registered tools by name.
func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	m := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		m[tool.Name] = tool
	}
	return m
}
