---
id: GWORK-EP-0011
type: epic
title: "Attachments: send files with Gmail and Google Chat"
status: backlog
priority: medium
author: mcp
labels: [write, gmail, chat, mcp]
created: 2026-10-06T10:21:30Z
updated: 2026-10-06T10:21:30Z
---

## Description

Allow attaching files when creating Gmail drafts, sending Gmail messages and sending Google Chat messages, from the CLI and from the MCP server. No new OAuth scopes: `gmail.modify` and `chat.messages.create` already cover it.

- Gmail: build `multipart/mixed` messages and send them with media upload (`message/rfc822`, up to 35 MB) instead of `Raw` in the JSON body. Also re-attach an existing Gmail attachment (`message_id` + `attachment_id`).
- Chat: `media.upload` to the space (up to 200 MB) returns an `AttachmentDataRef`, then `spaces.messages.create` with `attachment[]`. Text becomes optional when there is an attachment.
- CLI: `--attach <path>` (repeatable), shown in `--dry-run` and the confirmation.
- MCP: attachments by local path (no directory restriction: the agent already has filesystem access, so a restriction would not remove the risk; tool-call risk classifiers must control it), inline base64 content, and existing Gmail attachments.

## Acceptance Criteria

- `gwork gmail draft|send --attach f` produces a message with the file attached; names are RFC 2231 encoded.
- `gwork chat send --attach f` posts a message with the uploaded file.
- MCP write tools accept attachments; the audit log records names and sizes, never content.
- Tests with the fake Google server cover the upload endpoints; `make test`, `go vet`, `gofmt`, `make lint` pass.
- `docs/architecture.md` documents the feature and its risk model.

## Notes

Threat model decision (2026-10-06): no path allowlist in MCP; the user accepted that the agent's filesystem access makes it ineffective.
