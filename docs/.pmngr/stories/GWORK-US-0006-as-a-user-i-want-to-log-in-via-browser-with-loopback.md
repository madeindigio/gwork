---
id: GWORK-US-0006
type: story
title: As a user I want to log in via browser with loopback redirect and PKCE
status: backlog
priority: critical
parent: GWORK-EP-0002
milestone: GWORK-M-0001
author: mcp
estimate: 5
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T21:16:31Z
---

## Description
`gwork auth login [--services ...] [--no-browser]`: listener on 127.0.0.1:0, PKCE S256, state, offline access, prompt=consent, include_granted_scopes, `hd` hint, timeout; fetch account email.

## Acceptance Criteria
- Success page shown in browser; token stored.
- State mismatch / timeout / denied handled.
- Tested with fake auth + token endpoints.
