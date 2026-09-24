---
id: GWORK-US-0007
type: story
title: As a user I want my tokens stored securely and refreshed transparently
status: backlog
priority: critical
parent: GWORK-EP-0002
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T21:16:31Z
---

## Description
Token store interface; keyring backend (service `gwork`); file backend 0600 (`GWORK_KEYRING=file` or keyring unavailable); persisting TokenSource wrapper.

## Acceptance Criteria
- Refreshed token persisted.
- File backend never world-readable.
- Tests with in-memory/mocked keyring (`keyring.MockInit`).
