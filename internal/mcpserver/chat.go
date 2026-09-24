package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerChat registers the chat_* tools. It is called by New only when the
// chat service is requested and granted.
func registerChat(s *mcp.Server, deps Deps) {
	_, _ = s, deps // Phase 2: add chat_* tools with addReadOnlyTool(s, deps, auth.Chat, ...).
}
