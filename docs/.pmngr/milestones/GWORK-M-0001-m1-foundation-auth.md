---
id: GWORK-M-0001
type: milestone
title: M1 Foundation + Auth
status: done
author: mcp
created: 2026-09-24T21:15:32Z
updated: 2026-09-24T22:13:46Z
closed: 2026-09-24T22:13:46Z
due: 2026-10-09
---

## Description
Repo scaffold, CI, OAuth login (loopback + PKCE), token store, account handling, output layer, MCP skeleton.

## Acceptance Criteria
- `gwork auth login` works against digio Internal OAuth client.
- `go test ./...` green in CI.
