---
id: GWORK-T-0044
type: task
title: Design opt-in write operations for Gmail, Calendar and Chat
status: done
priority: medium
author: mcp
labels: [future, write, design]
created: 2026-09-29T22:20:42Z
updated: 2026-09-29T22:50:41Z
closed: 2026-09-29T22:50:41Z
---

## Description
Design (and later split into stories) opt-in write support for Gmail, Calendar and Chat. Drive writes are out of scope for now. Related epic: GWORK-EP-0009 (which also covers public release / Google verification, kept separate).

Today read-only is enforced in `internal/auth/scopes.go` (one readonly scope set per service), `internal/mcpserver/tools.go` (`addReadOnlyTool` only) and in `AGENTS.md` / `docs/architecture.md`.

### Proposed design (draft)
1. **Access level per service** (`read` | `write`), write scopes requested in addition to readonly:
   - gmail: `gmail.compose` (drafts, send, reply in thread); optional `gmail.modify` (labels, archive, mark read, trash)
   - calendar: `calendar.events` (create/update/delete, RSVP)
   - chat: `chat.messages.create` (post / reply in thread as the user); optional `chat.messages.reactions.create`
   - Internal OAuth client: no Google verification needed. Chat app config already exists (reads work).
   - Login: `gwork auth login --services gmail,chat --write gmail,chat` (incremental auth via `include_granted_scopes`).
2. **Double opt-in**: write scopes only on explicit login; MCP registers write tools only with `gwork mcp --allow-write gmail,calendar`.
3. **Prompt-injection mitigations**:
   - `addWriteTool` helper: `readOnlyHint=false`, `destructiveHint` / `idempotentHint` per tool.
   - Gmail draft-only by default; direct send behind `--allow-send`.
   - Recipients restricted to the hosted domain by default; `--allow-external` to widen.
   - Optional MCP elicitation confirmation before send.
   - CLI: `--dry-run`, TTY confirmation, `--yes`.
   - Audit log of every write (stderr / file).
4. **MVP tools**: `gmail_create_draft`, `gmail_send_draft`, (later `gmail_modify_labels`); `calendar_create_event`, `calendar_update_event`, `calendar_delete_event`, `calendar_respond_event`; `chat_send_message`.
5. **Order**: dedicated shared task first (access model in scopes.go, login `--write`, `addWriteTool`, `--allow-write`, docs/AGENTS.md update), then one story per service (workspace -> cli -> mcpserver).

## Open questions
- Gmail: drafts only, or also direct send behind `--allow-send`?
- Include `gmail.modify` (labels/archive) in this scope or defer?
- Backlog: new epic "Write operations: Gmail, Calendar, Chat" split from GWORK-EP-0009?

## Acceptance Criteria
- Open questions answered.
- Design documented in `docs/architecture.md`.
- Backlog split into stories/tasks per service plus the shared auth/MCP task.
