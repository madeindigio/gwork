package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerGmail registers the gmail_* tools. It is called by New only when the
// gmail service is requested and granted.
func registerGmail(s *mcp.Server, deps Deps) {
	_, _ = s, deps // Phase 2: add gmail_* tools with addReadOnlyTool(s, deps, auth.Gmail, ...).
}
