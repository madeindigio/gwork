---
id: GWORK-US-0023
type: story
title: As a user I want to search text across Chat spaces
status: done
priority: medium
parent: GWORK-EP-0006
milestone: GWORK-M-0002
author: mcp
estimate: 3
created: 2026-09-24T21:17:24Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork chat search <text> [--space ...] [--since 7d] [--max 50] [--max-scan 2000]`: list messages across selected (default all) spaces in window, case-insensitive match, bounded concurrency; report scanned count and whether cap was hit.

## Acceptance Criteria
- Deterministic result ordering (newest first); tests with multiple fake spaces.
