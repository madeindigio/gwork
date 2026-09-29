---
id: GWORK-US-0031
type: story
title: "Write foundation: write scopes, login --write, mcp --allow-write, addWriteTool"
status: done
priority: high
parent: GWORK-EP-0010
author: mcp
labels: [write, auth, mcp]
created: 2026-09-29T22:32:28Z
updated: 2026-09-29T22:38:25Z
started: 2026-09-29T22:32:35Z
closed: 2026-09-29T22:38:25Z
---

## Description
Shared infrastructure for write operations (dedicated task touching shared files):
- `internal/auth`: write scope registry (gmail: gmail.modify; calendar: calendar.events; chat: chat.messages.create; drive: none), ParseWriteServices, write-grant checks, `WriteClientOptions` / `WriteGrantedServices` on ClientProvider, write scope errors with `gwork auth login --write <svc>` hints, login requesting write scopes.
- `internal/cli`: `auth login --write`, write services in `auth status/list`, `mcp --allow-write` and `--allow-send`, shared write flags (`--yes`, `--dry-run`) and TTY confirmation helper.
- `internal/mcpserver`: `addWriteTool` (readOnlyHint=false, destructive/idempotent hints, audit log), write registrars with empty per-service stubs, whoami reports write services, server instructions.
- `internal/testutil`: FakeProvider write support.
- Docs: `docs/architecture.md`, `AGENTS.md`.

## Acceptance Criteria
- Without `--write`/`--allow-write` behaviour is unchanged (read-only).
- Tests cover scope parsing, grant checks, registration gating and hints.
