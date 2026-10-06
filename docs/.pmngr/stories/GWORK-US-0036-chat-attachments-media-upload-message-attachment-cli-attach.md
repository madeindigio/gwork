---
id: GWORK-US-0036
type: story
title: "Chat attachments: media.upload + message attachment, CLI --attach, MCP attachments"
status: done
parent: GWORK-EP-0011
author: mcp
labels: [write, chat, mcp]
created: 2026-10-06T10:21:42Z
updated: 2026-10-06T10:32:37Z
started: 2026-10-06T10:22:30Z
closed: 2026-10-06T10:32:37Z
---

## Description

- `workspace/chat`: `SendInput.Attachments` (filename, data). Resolve the space (DM via `FindDirectMessage`) first, then `Media.Upload(space, {filename}).Media(r)` per file, then `Messages.Create` with `Attachment: [{AttachmentDataRef}]`. Text optional when there are attachments. Max 200 MB per file.
- CLI: `--attach <path>` repeatable on `chat send`; files checked before network; dry-run and confirmation list names and sizes.
- MCP: `attachments` on `chat_send_message`: `{path}` (no directory restriction) or `{filename, content_base64}`.

## Acceptance Criteria

- Tests cover the upload endpoint (`/upload/v1/spaces/X/attachments:upload`), message creation with attachment refs, text-less messages, CLI and MCP.

## Notes

Verify against the real API whether several uploaded attachments per message are accepted.
