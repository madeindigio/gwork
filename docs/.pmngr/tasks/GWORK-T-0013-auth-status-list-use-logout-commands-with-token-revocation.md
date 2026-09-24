---
id: GWORK-T-0013
type: task
title: auth status/list/use/logout commands with token revocation
status: backlog
parent: GWORK-US-0008
milestone: GWORK-M-0001
author: mcp
created: 2026-09-24T21:18:01Z
updated: 2026-09-24T21:18:01Z
---

## Description
Commands + JSON output; logout posts to https://oauth2.googleapis.com/revoke (best effort) then deletes token.
