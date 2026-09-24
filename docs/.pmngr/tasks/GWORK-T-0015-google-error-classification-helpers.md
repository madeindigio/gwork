---
id: GWORK-T-0015
type: task
title: Google error classification helpers
status: backlog
parent: GWORK-US-0009
milestone: GWORK-M-0001
author: mcp
created: 2026-09-24T21:18:01Z
updated: 2026-09-24T21:18:01Z
---

## Description
Map oauth2.RetrieveError (invalid_grant, org_internal) and googleapi.Error (403 insufficientPermissions, accessNotConfigured/SERVICE_DISABLED, 404, 429) to typed errors with hints.
