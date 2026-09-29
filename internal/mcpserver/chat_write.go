package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// registerChatWrite registers the Chat write tools with addWriteTool. It is
// called only when the operator enabled chat writes (--allow-write) and the
// account granted the chat write scopes.
func registerChatWrite(s *mcp.Server, deps Deps) {}
