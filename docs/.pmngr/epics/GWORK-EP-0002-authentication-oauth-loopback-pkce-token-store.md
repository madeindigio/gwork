---
id: GWORK-EP-0002
type: epic
title: Authentication (OAuth loopback + PKCE, token store)
status: backlog
priority: critical
milestone: GWORK-M-0001
author: mcp
labels: [auth]
created: 2026-09-24T21:16:00Z
updated: 2026-09-24T21:16:00Z
---

## Description
Desktop OAuth flow with loopback redirect and PKCE, credential resolution (flag/env/config/ldflags), keyring token store with file fallback, multi-account, incremental read-only scopes, error classification. See [[architecture]] §Authentication.

## Acceptance Criteria
- Login, status, logout, list, use commands work.
- Refreshed tokens persisted.
- `invalid_grant`, `org_internal`, missing scope produce actionable errors.
