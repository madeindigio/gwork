---
id: GWORK-US-0022
type: story
title: As a user I want to list and read Chat messages of a space
status: done
priority: high
parent: GWORK-EP-0006
milestone: GWORK-M-0002
author: mcp
estimate: 3
created: 2026-09-24T21:17:24Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork chat messages <space> [--since] [--until] [--thread] [--max] [--order asc|desc]` using createTime filter; `gwork chat get <messageName>`. Accept `spaces/XXX` or bare id. Sender resolution: show sender name/type; resolve human display names via memberships when available.

## Acceptance Criteria
- Relative times (`7d`, `24h`) and RFC3339 supported.
