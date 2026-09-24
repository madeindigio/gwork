---
id: GWORK-US-0014
type: story
title: As a user I want to list calendars and events in a time window
status: backlog
priority: high
parent: GWORK-EP-0004
milestone: GWORK-M-0002
author: mcp
estimate: 3
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T21:16:59Z
---

## Description
`gwork calendar calendars`; `gwork calendar events [--calendar primary] [--from] [--to] [--query] [--max]`. Default window today 00:00 to +7d local time; accepts RFC3339, YYYY-MM-DD, relative (`today`, `tomorrow`, `+3d`). singleEvents=true, orderBy=startTime.

## Acceptance Criteria
- All-day vs timed events rendered correctly; time zones tested.
