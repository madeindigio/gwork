---
id: GWORK-EP-0001
type: epic
title: Project foundation
status: done
priority: critical
milestone: GWORK-M-0001
author: mcp
labels: [foundation]
created: 2026-09-24T21:16:00Z
updated: 2026-09-24T22:13:45Z
closed: 2026-09-24T22:13:45Z
---

## Description
Go module, layout, cobra root, config, output layer, CI, tooling, base docs (README, AGENTS.md, LICENSE). See [[architecture]].

## Acceptance Criteria
- `go build ./... && go test -race ./...` green.
- Layout matches docs/architecture.md.
