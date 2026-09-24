---
id: GWORK-US-0010
type: story
title: As a user I want to search Gmail with Gmail query syntax
status: done
priority: high
parent: GWORK-EP-0003
milestone: GWORK-M-0002
author: mcp
estimate: 3
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork gmail search <query> [--max N] [--include-spam-trash]`: messages.list + batched metadata get (From, To, Subject, Date, snippet, labels). Bounded concurrency.

## Acceptance Criteria
- Pagination up to `--max` (default 20).
- Text table + JSON.
