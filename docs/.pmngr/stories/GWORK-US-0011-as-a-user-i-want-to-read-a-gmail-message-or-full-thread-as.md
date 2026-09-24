---
id: GWORK-US-0011
type: story
title: As a user I want to read a Gmail message or full thread as text
status: done
priority: high
parent: GWORK-EP-0003
milestone: GWORK-M-0002
author: mcp
estimate: 5
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork gmail get <id>` and `gwork gmail thread <id>`: walk MIME parts, decode base64url, prefer text/plain, fall back to HTML-to-text; list attachments (id, filename, mime, size); headers.

## Acceptance Criteria
- Multipart/alternative, nested multipart/mixed, charset handling tested with fixtures.
