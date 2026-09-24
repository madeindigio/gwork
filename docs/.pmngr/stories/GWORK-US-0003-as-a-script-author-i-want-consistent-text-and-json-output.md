---
id: GWORK-US-0003
type: story
title: As a script author I want consistent text and JSON output
status: backlog
priority: high
parent: GWORK-EP-0001
milestone: GWORK-M-0001
author: mcp
estimate: 2
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T21:16:31Z
---

## Description
`internal/output`: renderer chosen by `--output`; JSON (indented, stable field names, snake_case), text tables via text/tabwriter, errors to stderr, non-zero exit codes.

## Acceptance Criteria
- Every command renders via output package.
- JSON output is valid for all commands (tested).
