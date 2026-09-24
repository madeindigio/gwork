---
id: GWORK-US-0001
type: story
title: As a developer I want a scaffolded Go repo with tooling and CI
status: backlog
priority: critical
parent: GWORK-EP-0001
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T21:16:31Z
---

## Description
Module `github.com/digio/gwork-cli`, layout per [[architecture]], Makefile, golangci-lint config, GitHub Actions CI, MIT LICENSE, .gitignore.

## Acceptance Criteria
- `make build test lint` work locally.
- CI runs vet, gofmt check, lint, `go test -race`.
