---
id: GWORK-US-0027
type: story
title: As a maintainer I want GoReleaser builds with the embedded OAuth client
status: backlog
priority: high
parent: GWORK-EP-0008
milestone: GWORK-M-0003
author: mcp
estimate: 3
created: 2026-09-24T21:17:24Z
updated: 2026-09-24T21:17:24Z
---

## Description
.goreleaser.yaml (linux/darwin/windows, amd64/arm64), ldflags for version/commit/date/OAuthClientID/OAuthClientSecret/HostedDomain from CI secrets; release workflow on tag; checksums.

## Acceptance Criteria
- `goreleaser release --snapshot --clean` works locally without secrets (no embedded client).
