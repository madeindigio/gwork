---
id: GWORK-US-0013
type: story
title: As an AI agent I want Gmail MCP tools
status: done
priority: high
parent: GWORK-EP-0003
milestone: GWORK-M-0002
author: mcp
estimate: 2
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gmail_search`, `gmail_get_message`, `gmail_get_thread`, `gmail_list_labels` with typed IO, `max_chars` truncation, readOnlyHint.

## Acceptance Criteria
- In-memory transport tests call each tool against fake Gmail API.
