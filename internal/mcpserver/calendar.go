package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerCalendar registers the calendar_* tools. It is called by New only when the
// calendar service is requested and granted.
func registerCalendar(s *mcp.Server, deps Deps) {
	_, _ = s, deps // Phase 2: add calendar_* tools with addReadOnlyTool(s, deps, auth.Calendar, ...).
}
