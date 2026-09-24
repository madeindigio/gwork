---
id: GWORK-US-0008
type: story
title: As a user I want to manage accounts (status, list, use, logout)
status: done
priority: high
parent: GWORK-EP-0002
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:16:32Z
updated: 2026-09-24T22:13:34Z
closed: 2026-09-24T22:13:34Z
---

## Description
`gwork auth status|list|use <email>|logout [--all]`; default account in config.json; `--account` / `GWORK_ACCOUNT`. Logout revokes token (best effort) and deletes it.

## Acceptance Criteria
- Status shows email, granted services, token backend, expiry.
