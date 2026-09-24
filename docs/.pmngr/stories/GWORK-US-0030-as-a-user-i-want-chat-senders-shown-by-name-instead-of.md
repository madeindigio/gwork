---
id: GWORK-US-0030
type: story
title: As a user I want Chat senders shown by name instead of users/{id}
status: backlog
priority: medium
parent: GWORK-EP-0006
author: mcp
estimate: 3
created: 2026-09-24T21:50:32Z
updated: 2026-09-24T21:50:32Z
---

## Description
With user auth, Chat API `Message.sender` and `Membership.member` only return `users/{id}` and type, no displayName. Resolve names via People API (`people.getBatchGet` on `people/{id}` or `otherContacts`/directory search) with scope `directory.readonly`, cached per invocation; opt-in service `people`.

## Acceptance Criteria
- Chat CLI and MCP outputs show display names when the directory scope is granted.
- Degrades to `users/{id}` without the scope.

## Notes
Found during Phase 2 (Chat agent). Verify People API directory lookup by user id works for Workspace users.
