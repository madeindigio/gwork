---
id: GWORK-US-0035
type: story
title: "Gmail attachments: multipart MIME, media upload, CLI --attach, MCP attachments"
status: done
parent: GWORK-EP-0011
author: mcp
labels: [write, gmail, mcp]
created: 2026-10-06T10:21:42Z
updated: 2026-10-06T10:32:37Z
started: 2026-10-06T10:22:29Z
closed: 2026-10-06T10:32:37Z
---

## Description

- `workspace/gmail`: `ComposeInput.Attachments` (filename, content type, data). `buildMessage` emits `multipart/mixed` (text/plain QP part + base64 parts, `Content-Disposition` via `mime.FormatMediaType`, RFC 2231 names, CR/LF validation). Send, draft create via `.Media(r, googleapi.ContentType("message/rfc822"))`. Helper to fetch an existing attachment (`message_id` + `attachment_id`) as an attachment input. Pre-check total size (25 MB).
- CLI: `--attach <path>` repeatable on `gmail draft create` and `gmail send`; files opened/stat'ed before network; dry-run and confirmation list names and sizes.
- MCP: `attachments` on `gmail_create_draft` and `gmail_send_message`: `{path}` (no directory restriction), `{filename, content_base64}`, or `{message_id, attachment_id}`.

## Acceptance Criteria

- Tests cover MIME structure, UTF-8 filenames, upload endpoints (`/upload/gmail/v1/users/me/messages/send`, `/upload/gmail/v1/users/me/drafts`), CLI and MCP paths.
- Messages without attachments keep working.
