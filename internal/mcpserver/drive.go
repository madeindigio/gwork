package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerDrive registers the drive_* tools. It is called by New only when the
// drive service is requested and granted.
func registerDrive(s *mcp.Server, deps Deps) {
	_, _ = s, deps // Phase 2: add drive_* tools with addReadOnlyTool(s, deps, auth.Drive, ...).
}
