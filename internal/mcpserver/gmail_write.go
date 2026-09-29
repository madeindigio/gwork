package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// registerGmailWrite registers the Gmail write tools with addWriteTool. It is
// called only when the operator enabled gmail writes (--allow-write) and the
// account granted the gmail write scopes.
func registerGmailWrite(s *mcp.Server, deps Deps) {}
