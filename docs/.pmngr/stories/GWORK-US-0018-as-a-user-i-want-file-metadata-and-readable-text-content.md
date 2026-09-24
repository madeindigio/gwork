---
id: GWORK-US-0018
type: story
title: As a user I want file metadata and readable text content
status: done
priority: high
parent: GWORK-EP-0005
milestone: GWORK-M-0002
author: mcp
estimate: 5
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork drive get <id>`; `gwork drive read <id> [--max-bytes] [--format]`: Docs export text/markdown (fallback text/plain), Sheets text/csv, Slides text/plain; text-like files downloaded; binaries rejected with hint. Export size limit (10MB) surfaced clearly.

## Acceptance Criteria
- Each mime branch tested against fake API.
