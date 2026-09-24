---
id: GWORK-EP-0007
type: epic
title: MCP server
status: done
priority: high
milestone: GWORK-M-0002
author: mcp
labels: [mcp]
created: 2026-09-24T21:16:00Z
updated: 2026-09-24T22:13:46Z
closed: 2026-09-24T22:13:46Z
---

## Description
`gwork mcp` stdio server on the official go-sdk; shared registration pattern, service filtering, truncation, `whoami`. Service-specific tools live in the service epics.

## Acceptance Criteria
- Works from Claude Code / Claude Desktop config.
- Only protocol on stdout; logs on stderr.
- In-memory transport tests.
