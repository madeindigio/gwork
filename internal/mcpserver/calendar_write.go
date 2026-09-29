package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// registerCalendarWrite registers the Calendar write tools with addWriteTool. It is
// called only when the operator enabled calendar writes (--allow-write) and the
// account granted the calendar write scopes.
func registerCalendarWrite(s *mcp.Server, deps Deps) {}
