---
id: GWORK-US-0009
type: story
title: As a service implementer I want a client factory with scope checks and error classification
status: backlog
priority: critical
parent: GWORK-EP-0002
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:16:32Z
updated: 2026-09-24T21:16:32Z
---

## Description
Scope registry per service (read-only), `auth.ClientFor(ctx, service) (*http.Client, error)` that checks granted scopes; error classification: `invalid_grant` -> ErrReauthRequired, `org_internal`, 403 insufficientPermissions, API disabled -> actionable messages.

## Acceptance Criteria
- Missing scope error says `run: gwork auth login --services <svc>`.
- Unit tests for each classification.
