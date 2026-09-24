---
id: GWORK-US-0002
type: story
title: As a user I want a gwork root command with global flags, config and version
status: done
priority: critical
parent: GWORK-EP-0001
milestone: GWORK-M-0001
author: mcp
estimate: 3
created: 2026-09-24T21:16:31Z
updated: 2026-09-24T22:13:34Z
closed: 2026-09-24T22:13:34Z
---

## Description
Cobra root with `--account`, `--credentials`, `--output text|json`, `--json`, `--timeout`; `internal/config` (os.UserConfigDir()/gwork, `GWORK_CONFIG_DIR` override, config.json); `internal/buildinfo`; `gwork version`.

## Acceptance Criteria
- `gwork version --json` prints version/commit/date and whether an embedded client exists.
- Service command groups registered as stubs so later work only fills own files.
