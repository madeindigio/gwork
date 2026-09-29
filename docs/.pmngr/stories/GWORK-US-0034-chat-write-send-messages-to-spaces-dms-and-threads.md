---
id: GWORK-US-0034
type: story
title: "Chat write: send messages to spaces, DMs and threads"
status: backlog
priority: high
parent: GWORK-EP-0010
author: mcp
labels: [write, chat]
created: 2026-09-29T22:32:28Z
updated: 2026-09-29T22:32:28Z
---

## Description
Workspace function, CLI command and MCP tool to post a Chat message as the user (scope `chat.messages.create`):
- Target a space name, or a user email resolved to the existing DM (spaces.findDirectMessage).
- Optional reply in an existing thread.
- MCP: `chat_send_message`.

## Acceptance Criteria
- Tests with FakeGoogle for space, DM and thread reply, and for a missing DM.
