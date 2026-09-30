package mcpserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
)

// timeExpressions documents the time expressions accepted by tool inputs
// parsed with timeutil (calendar time_min/time_max, chat since/until). Tool
// descriptions append it so every tool explains the syntax the same way.
const timeExpressions = "Accepts RFC 3339 (2026-09-24T10:00:00+02:00 or ...Z), a date YYYY-MM-DD, " +
	"today, tomorrow, yesterday, now, or a relative value: +3d (3 days from now), 7d or -7d (7 days ago); units m, h, d, w."

// ToolFunc is the business function behind a typed tool: it receives the
// decoded, schema-validated input and returns the structured output.
type ToolFunc[In, Out any] func(ctx context.Context, in In) (Out, error)

// auditLevel is the level of write-tool audit lines. It is Warn, not Info,
// so that the audit trail survives --log-level warn; only --log-level error
// hides it.
const auditLevel = slog.LevelWarn

// addReadOnlyTool registers a typed tool annotated with readOnlyHint=true.
//
// In and Out are structs: their JSON schemas are inferred by the SDK
// (property descriptions come from `jsonschema:"..."` struct tags; use
// snake_case json tags and `omitempty` for optional inputs). Each call
// runs with Deps' timeout (if any); errors are classified with svc (so hints such
// as "run: gwork auth login --services chat" reach the model) and returned
// as tool errors (isError=true), never as protocol errors.
//
// svc may be empty for tools that are not tied to a service.
func addReadOnlyTool[In, Out any](s *mcp.Server, deps Deps, svc auth.Service, tool *mcp.Tool, fn ToolFunc[In, Out]) {
	addTool(s, deps, svc, tool, true, fn)
}

// addWriteTool registers a typed tool that may change the user's data
// (readOnlyHint=false). Register it only from a register<Svc>Write function.
//
// It behaves like addReadOnlyTool, except that errors are classified with
// auth.ClassifyWrite (a missing scope yields the write login hint) and that
// every call is recorded in an audit log line (auditLevel, stderr) with the
// tool, account, service, duration and outcome, never the input or output.
//
// The caller sets DestructiveHint (a pointer; nil means the MCP default,
// true), IdempotentHint and OpenWorldHint on tool.Annotations; they are kept.
// Tools that send email or messages to other people should set OpenWorldHint.
func addWriteTool[In, Out any](s *mcp.Server, deps Deps, svc auth.Service, tool *mcp.Tool, fn ToolFunc[In, Out]) {
	addTool(s, deps, svc, tool, false, fn)
}

// addTool is the shared implementation of addReadOnlyTool and addWriteTool.
func addTool[In, Out any](s *mcp.Server, deps Deps, svc auth.Service, tool *mcp.Tool, readOnly bool, fn ToolFunc[In, Out]) {
	t := *tool
	ann := mcp.ToolAnnotations{}
	if tool.Annotations != nil {
		ann = *tool.Annotations
	}
	ann.ReadOnlyHint = readOnly
	t.Annotations = &ann
	name := t.Name
	log := deps.logger()

	mcp.AddTool(s, &t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if d := deps.timeout(); d > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		start := time.Now()
		out, err := fn(ctx, in)
		if err != nil {
			if readOnly {
				err = auth.ClassifyService(err, svc)
				log.Warn("tool call failed", "tool", name, "duration", time.Since(start), "err", err)
			} else {
				err = auth.ClassifyWrite(err, svc)
				log.Log(ctx, auditLevel, "write tool call", "audit", true, "tool", name, "account", accountOf(deps),
					"service", string(svc), "duration", time.Since(start), "ok", false, "err", err)
			}
			var zero Out
			return nil, zero, err
		}
		if readOnly {
			log.Debug("tool call", "tool", name, "duration", time.Since(start))
		} else {
			log.Log(ctx, auditLevel, "write tool call", "audit", true, "tool", name, "account", accountOf(deps),
				"service", string(svc), "duration", time.Since(start), "ok", true)
		}
		return nil, out, nil
	})
}
