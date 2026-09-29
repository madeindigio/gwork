---
id: GWORK-EP-0010
type: epic
title: "Write operations: Gmail, Calendar, Chat"
status: in_progress
priority: high
author: mcp
labels: [write]
created: 2026-09-29T22:32:12Z
updated: 2026-09-29T22:32:12Z
started: 2026-09-29T22:32:12Z
---

## Description
Opt-in write support for Gmail, Calendar and Chat (CLI + MCP). Drive writes stay out of scope. Public release / Google verification remain in GWORK-EP-0009. Design: GWORK-T-0044.

## Decisions (2026-09-30)
- Gmail: drafts always available with write access; sending (`gmail_send_draft`, `gmail_send_message`) only when the MCP server runs with `--allow-send`. Scope: `gmail.modify` (covers compose/send/labels/trash).
- `gmail.modify` operations included: labels, archive, mark read/unread, trash.
- Recipients outside the hosted domain are allowed (no domain restriction).
- Calendar scope: `calendar.events`. Chat scope: `chat.messages.create`.
- Double opt-in: `gwork auth login --write <svcs>` requests write scopes; `gwork mcp --allow-write <svcs>` registers write tools.

## Stories
Shared foundation first, then one story per service.

## Acceptance Criteria
- All stories done, `make test`, `go vet`, `gofmt -l .`, `make lint` clean.
- `docs/architecture.md` and `AGENTS.md` describe the write model.
