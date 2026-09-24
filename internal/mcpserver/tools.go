package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/digio/gwork-cli/internal/auth"
)

// timeExpressions documents the time expressions accepted by tool inputs
// parsed with timeutil (calendar time_min/time_max, chat since/until). Tool
// descriptions append it so every tool explains the syntax the same way.
const timeExpressions = "Accepts RFC 3339 (2026-09-24T10:00:00+02:00 or ...Z), a date YYYY-MM-DD, " +
	"today, tomorrow, yesterday, now, or a relative value: +3d (3 days from now), 7d or -7d (7 days ago); units m, h, d, w."

// ToolFunc is the business function behind a typed tool: it receives the
// decoded, schema-validated input and returns the structured output.
type ToolFunc[In, Out any] func(ctx context.Context, in In) (Out, error)

// addReadOnlyTool registers a typed tool annotated with readOnlyHint=true.
//
// In and Out are structs: their JSON schemas are inferred by the SDK
// (property descriptions come from `jsonschema:"..."` struct tags; use
// snake_case json tags and `omitempty` for optional inputs). Each call
// runs with Deps' timeout; errors are classified with svc (so hints such
// as "run: gwork auth login --services chat" reach the model) and returned
// as tool errors (isError=true), never as protocol errors.
//
// svc may be empty for tools that are not tied to a service.
func addReadOnlyTool[In, Out any](s *mcp.Server, deps Deps, svc auth.Service, tool *mcp.Tool, fn ToolFunc[In, Out]) {
	t := *tool
	ann := mcp.ToolAnnotations{}
	if tool.Annotations != nil {
		ann = *tool.Annotations
	}
	ann.ReadOnlyHint = true
	t.Annotations = &ann
	name := t.Name
	log := deps.logger()

	mcp.AddTool(s, &t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		ctx, cancel := context.WithTimeout(ctx, deps.timeout())
		defer cancel()
		start := time.Now()
		out, err := fn(ctx, in)
		if err != nil {
			err = auth.ClassifyService(err, svc)
			log.Warn("tool call failed", "tool", name, "duration", time.Since(start), "err", err)
			var zero Out
			return nil, zero, err
		}
		log.Debug("tool call", "tool", name, "duration", time.Since(start))
		return nil, out, nil
	})
}
