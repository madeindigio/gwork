---
id: GWORK-US-0033
type: story
title: "Calendar write: create, update, delete events and RSVP"
status: backlog
priority: high
parent: GWORK-EP-0010
author: mcp
labels: [write, calendar]
created: 2026-09-29T22:32:28Z
updated: 2026-09-29T22:32:28Z
---

## Description
Workspace functions, CLI commands and MCP tools for Calendar writes (scope `calendar.events`):
- Create event (timed or all-day, attendees, description, location, optional Meet link), patch event, delete event, respond to an invitation (accepted/declined/tentative).
- `send_updates` (all|external_only|none) exposed on every change.
- MCP: `calendar_create_event`, `calendar_update_event`, `calendar_delete_event`, `calendar_respond_event`.

## Acceptance Criteria
- Tests with FakeGoogle for every operation, including all-day events and RSVP of the current user only.
