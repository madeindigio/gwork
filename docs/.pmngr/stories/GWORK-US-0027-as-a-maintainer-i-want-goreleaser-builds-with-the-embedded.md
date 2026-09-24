---
id: GWORK-US-0027
type: story
title: As a maintainer I want GoReleaser builds with the embedded OAuth client
status: done
priority: high
parent: GWORK-EP-0008
milestone: GWORK-M-0003
author: mcp
estimate: 3
created: 2026-09-24T21:17:24Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
.goreleaser.yaml (linux/darwin/windows, amd64/arm64), ldflags for version/commit/date/OAuthClientID/OAuthClientSecret/HostedDomain from CI secrets; release workflow on tag; checksums.

## Acceptance Criteria
- `goreleaser release --snapshot --clean` works locally without secrets (no embedded client).
