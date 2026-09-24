---
id: GWORK-US-0019
type: story
title: As a user I want to download any Drive file
status: done
priority: medium
parent: GWORK-EP-0005
milestone: GWORK-M-0002
author: mcp
estimate: 2
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T22:13:35Z
closed: 2026-09-24T22:13:35Z
---

## Description
`gwork drive download <id> --out <path> [--export-format pdf|docx|xlsx|...] [--force]`, streaming to disk.

## Acceptance Criteria
- Google-native files require/choose export format; no overwrite without `--force`.
