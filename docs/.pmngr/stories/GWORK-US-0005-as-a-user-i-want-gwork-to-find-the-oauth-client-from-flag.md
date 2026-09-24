---
id: GWORK-US-0005
type: story
title: As a user I want gwork to find the OAuth client from flag, env, config or embedded build
status: done
priority: critical
parent: GWORK-EP-0002
milestone: GWORK-M-0001
author: mcp
estimate: 2
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T22:13:34Z
closed: 2026-09-24T22:13:34Z
---

## Description
Resolution order: `--credentials`, `GWORK_CREDENTIALS`, `<configDir>/credentials.json`, ldflags-embedded client (`buildinfo.OAuthClientID/Secret`). Parse Google `installed` credentials JSON.

## Acceptance Criteria
- Clear error listing all options when none found.
- Unit tests for each source and precedence.
