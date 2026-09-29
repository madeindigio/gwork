---
id: GWORK-EP-0006
type: epic
title: Chat read-only
status: done
priority: high
milestone: GWORK-M-0002
author: mcp
labels: [chat]
created: 2026-09-24T21:16:00Z
updated: 2026-09-29T21:43:52Z
started: 2026-09-24T22:13:46Z
closed: 2026-09-29T21:43:52Z
---

## Description
List spaces, find DM by email, list/get messages, client-side text search over a time window. CLI + MCP tools.

## Acceptance Criteria
- Commands `gwork chat spaces|dm|messages|get|search`.
- MCP tools `chat_list_spaces`, `chat_find_dm`, `chat_list_messages`, `chat_get_message`, `chat_search_messages`.

## Notes
Chat API has no user-level full-text search; search is list + filter, capped by `--max-scan`.
