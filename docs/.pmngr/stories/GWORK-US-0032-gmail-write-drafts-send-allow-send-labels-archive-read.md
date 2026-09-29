---
id: GWORK-US-0032
type: story
title: "Gmail write: drafts, send (--allow-send), labels, archive, read state, trash"
status: done
priority: high
parent: GWORK-EP-0010
author: mcp
labels: [write, gmail]
created: 2026-09-29T22:32:28Z
updated: 2026-09-29T22:50:37Z
started: 2026-09-29T22:38:25Z
closed: 2026-09-29T22:50:37Z
---

## Description
Workspace functions, CLI commands and MCP tools for Gmail writes:
- Create draft (new or reply in a thread with In-Reply-To/References), send draft, send message.
- Modify labels on messages/threads (archive, mark read/unread, add/remove labels), trash/untrash.
- MCP: `gmail_create_draft`, `gmail_send_draft` and `gmail_send_message` (only with `--allow-send`), `gmail_modify_labels`, `gmail_trash`.

## Acceptance Criteria
- Send tools are not registered without `--allow-send`.
- Tests with FakeGoogle for every operation, including reply headers.
