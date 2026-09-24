---
id: GWORK-US-0025
type: story
title: As an AI agent I want gwork mcp to serve read-only Workspace tools over stdio
status: done
priority: critical
parent: GWORK-EP-0007
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:17:24Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`internal/mcpserver`: server construction with go-sdk, per-service registration hooks (`registerGmail`, ...), `--services` filter, skip services lacking scopes, `whoami` tool, truncation helper, stderr logging, graceful shutdown.

## Acceptance Criteria
- `whoami` works via in-memory transport test.
- Nothing but protocol on stdout.
