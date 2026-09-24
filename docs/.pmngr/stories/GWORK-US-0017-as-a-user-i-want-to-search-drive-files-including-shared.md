---
id: GWORK-US-0017
type: story
title: As a user I want to search Drive files including shared drives
status: backlog
priority: high
parent: GWORK-EP-0005
milestone: GWORK-M-0002
author: mcp
estimate: 3
created: 2026-09-24T21:16:59Z
updated: 2026-09-24T21:16:59Z
---

## Description
`gwork drive search [text] [--name] [--mime|--type doc|sheet|slides|pdf|folder] [--owner] [--folder] [--query raw] [--max]`. Safe query builder (escape quotes), trashed=false, supportsAllDrives, includeItemsFromAllDrives, corpora=allDrives.

## Acceptance Criteria
- Query builder unit-tested incl. escaping.
