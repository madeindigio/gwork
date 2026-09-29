# gwork — Architecture and technical decisions

`gwork` is a single Go binary that exposes **read-only** access to Gmail, Google
Drive, Google Chat and Google Calendar through two front-ends that share the same
core:

- a **CLI** (cobra) for humans and scripts (`--output json` for machines);
- an **MCP server** (`gwork mcp`, stdio) for AI agents.

v1 targets digio's Google Workspace organization with an **Internal** OAuth app,
so no Google verification is needed. The code stays generic (MIT licensed) so it
can be opened later with "bring your own OAuth client".

## Decisions

| Topic | Decision |
|---|---|
| Module / binary | `github.com/digio/gwork-cli`, binary `gwork` (`cmd/gwork`) |
| Go | 1.26 |
| CLI | `github.com/spf13/cobra` |
| Google APIs | `google.golang.org/api` REST clients: `gmail/v1`, `calendar/v3`, `drive/v3`, `chat/v1` |
| OAuth | `golang.org/x/oauth2` + `golang.org/x/oauth2/google`, Desktop client, loopback redirect + PKCE S256 |
| MCP | `github.com/modelcontextprotocol/go-sdk` (official), stdio transport |
| Token storage | `github.com/zalando/go-keyring`; fallback to a `0600` JSON file when no keyring is available |
| Browser | `github.com/pkg/browser` (always print the URL too, for headless/SSH use) |
| Scope policy v1 | Read-only scopes only; requested incrementally per service |
| License | MIT |

## Layout

```
cmd/gwork/main.go            entry point, calls cli.Execute()
internal/buildinfo/          version, commit, embedded OAuth client (ldflags)
internal/config/             config dir (XDG / os.UserConfigDir), config.json, accounts
internal/auth/               OAuth config, login flow, token store, persisting TokenSource,
                             scope registry, auth error classification
internal/output/             text/json renderers and text formatting helpers (timestamps,
                             sizes, "Name <email>") shared by all commands
internal/fsutil/             atomic 0600 file writing (downloads, config, token files)
internal/workspace/          business logic, one package per API, no cobra/MCP imports
  gmail/  calendar/  drive/  chat/
internal/cli/                cobra commands (thin adapters over workspace/*)
internal/mcpserver/          MCP server + tool registration (thin adapters over workspace/*)
docs/                        knowledge base + gintrack backlog (docs/.pmngr)
```

Rule: `workspace/*` packages expose plain Go types and functions (`Search(ctx, opts)
([]MessageSummary, error)`), take an `*http.Client` / `option.ClientOption`
and know nothing about cobra, MCP or output formats. CLI and MCP adapters are
thin and must not duplicate logic.

## Authentication

### OAuth client resolution (first match wins)

1. `--credentials <path>` flag
2. `GWORK_CREDENTIALS` env var (path to a Google `credentials.json`)
3. `<configDir>/credentials.json`
4. Client embedded at build time:
   `-ldflags "-X github.com/digio/gwork-cli/internal/buildinfo.OAuthClientID=... -X ...OAuthClientSecret=..."`

Desktop client secrets are not confidential per Google; embedding them in the
internal build is acceptable. They must never be committed to the repo.

### Login flow (`gwork auth login [--services gmail,calendar,drive,chat]`)

1. Listen on `127.0.0.1:0`; redirect URI `http://127.0.0.1:<port>/callback`.
2. Auth URL with `access_type=offline`, `prompt=consent`, PKCE S256,
   random `state`, `include_granted_scopes=true`, and `hd=<domain>` hint when
   `GWORK_HOSTED_DOMAIN` / build-time default is set.
3. Open browser (print URL as well), wait for callback with timeout (default 5m),
   validate `state`, exchange code with verifier.
4. Fetch the account email (`openid email` scopes, userinfo / id_token).
5. Store token + granted scopes under the account email.

Scopes (read-only):

| Service | Scopes |
|---|---|
| base | `openid`, `https://www.googleapis.com/auth/userinfo.email` |
| gmail | `gmail.readonly` |
| calendar | `calendar.readonly` |
| drive | `drive.readonly` |
| chat | `chat.spaces.readonly`, `chat.messages.readonly`, `chat.memberships.readonly` |

Commands check that the stored token covers the service's scopes; if not they
fail with an actionable message: `run: gwork auth login --services chat`.

### Token store

- Keyring service name `gwork`, key = account email, value = JSON
  `{token, scopes, email, created}`.
- `GWORK_KEYRING=file` or keyring failure: `<configDir>/tokens/<email>.json`
  with mode `0600` (warn once on stderr).
- A persisting `oauth2.TokenSource` wrapper saves the token whenever it is
  refreshed.

### Accounts

`--account <email>` / `GWORK_ACCOUNT`; otherwise the default account in
`<configDir>/config.json` (set by the first login or `gwork auth use <email>`).

### Error classification

- `invalid_grant` (revoked, expired by session-control policy): `ErrReauthRequired`,
  message tells the user to run `gwork auth login`.
- `org_internal` / `access_denied`: clear message that only digio accounts can log in.
- HTTP 403 `insufficientPermissions`: missing scope message per service.
- API not enabled / admin blocked: surface Google's message plus a hint to
  `docs/setup-google-cloud.md`.
- HTTP 404, 429 and other 403s: `ErrNotFound`, `ErrRateLimited`,
  `ErrPermissionDenied` with a hint.
- Classified messages keep the context added by the code that failed and
  Google's message: `not found: get message 18a...: Requested entity was not
  found.` followed by `hint: ...`.

## CLI surface (v1)

Generated from `gwork <group> <cmd> --help`; keep in sync when adding
flags.

```
gwork version
gwork auth login [--services gmail,calendar,drive,chat|all] [--no-browser]
gwork auth logout [email] [--all] [--no-revoke]
gwork auth status
gwork auth list
gwork auth use <email>
gwork gmail search <query> [--max 20] [--include-spam-trash]   # Gmail query syntax
gwork gmail get <messageId> [--raw-html]
gwork gmail thread <threadId>
gwork gmail labels
gwork gmail attachment <messageId> <attachmentId> --out <path> [--force]
gwork calendar calendars
gwork calendar events [--calendar primary] [--from today] [--to +7d] [-q|--query] [--max 50]
gwork calendar get <eventId> [--calendar primary]
gwork drive search [text] [--name] [--type doc|sheet|slides|pdf|image|folder|form|drawing]
                   [--mime] [--owner] [--folder] [--modified-after] [--query raw]
                   [--order-by] [--max 25]
gwork drive get <fileId>                        # metadata
gwork drive read <fileId> [--format md|txt|csv] [--max-bytes 5242880]
gwork drive download <fileId> --out <path> [--export-format docx|xlsx|pptx|pdf|...] [--force]
gwork chat spaces [--type space|group|dm] [--max 100]
gwork chat dm <email>                           # find direct message space
gwork chat messages <space> [--since] [--until] [--thread] [--order desc|asc] [--max 50] [--no-resolve-names]
gwork chat get <messageName> [--no-resolve-names]
gwork chat search <text> [--space ...] [--since 7d] [--max 50] [--max-scan 2000] [--no-resolve-names]
gwork mcp [--services gmail,calendar,drive,chat|all] [--log-level info]
```

Global flags: `--account`, `--credentials`, `-o/--output text|json` (`--json`
shortcut), `--timeout` (default 1m, 0 disables).

Time expressions (`--from/--to/--since/--until`, MCP `time_min/time_max/
since/until`): RFC 3339, `YYYY-MM-DD`, `today`, `tomorrow`, `yesterday`,
`now`, or relative `+3d` (future) / `7d` or `-7d` (past) with units
`m h d w`. Text output shows timestamps as `YYYY-MM-DD HH:MM` in the local
time zone.
Text output is sanitized (`output.Sanitize`, applied by the Printer): C0/C1
control characters other than newline and tab, and bidi override/isolate
characters, become U+FFFD so untrusted content cannot inject terminal escape
sequences. JSON output is unchanged.

Content rules:
- Gmail body: prefer `text/plain`, fall back to HTML converted to text.
- Drive read: Docs export `text/markdown` (fallback `text/plain`), Sheets
  `text/csv`, Slides `text/plain`, Drawings unsupported; plain-text-like files
  downloaded directly; binaries (PDF, images, Office) return an error suggesting
  `drive download`. Shared drives supported (`supportsAllDrives`,
  `includeItemsFromAllDrives`).
- Downloads (`gmail attachment`, `drive download`) are written atomically
  (temporary file in the destination directory, fsync, rename) with mode
  `0600`, and never replace an existing file without `--force`.
- Chat has no user-level full-text search API: `chat search` lists messages in
  the selected spaces within a time window and filters client-side, capped by
  `--max-scan`.
- Chat sender display names: verified against the digio tenant (2026-09-29),
  the Chat API returns `sender.displayName` for human senders with user
  authentication. When a message lacks it, gwork fills it from space
  memberships (disable with `--no-resolve-names`); if that also fails the
  sender is shown as `users/{id}`.

## MCP server

`gwork mcp` runs over stdio with the official go-sdk. Tools (all annotated
`readOnlyHint: true`), each with typed input/output structs:

| Tool | Inputs |
|---|---|
| `whoami` | (none) current account + granted services |
| `gmail_search` | `query`, `max_results` |
| `gmail_get_message` | `message_id`, `max_chars`, `include_html` |
| `gmail_get_thread` | `thread_id`, `max_chars` (budget shared by all bodies) |
| `gmail_list_labels` | (none) |
| `calendar_list_calendars` | (none) |
| `calendar_list_events` | `calendar_id`, `time_min`, `time_max`, `query`, `max_results` (max 250) |
| `calendar_get_event` | `calendar_id`, `event_id`, `max_chars` |
| `drive_search` | `query_text`, `name`, `type`, `mime_type`, `owner`, `folder_id`, `modified_after`, `raw_query`, `max_results` |
| `drive_get_file` | `file_id` |
| `drive_read_file` | `file_id`, `max_chars` |
| `chat_list_spaces` | `type`, `max_results` (max 1000) |
| `chat_find_dm` | `email` |
| `chat_list_messages` | `space`, `since`, `until`, `thread`, `order`, `max_results` (max 1000), `max_chars` |
| `chat_get_message` | `message_name`, `max_chars` |
| `chat_search_messages` | `text`, `spaces`, `since`, `max_results` (max 1000), `max_scan` (max 20000), `max_chars` |

There is intentionally no download tool: MCP clients receive text, not
files.

- `--services` limits which tool groups are registered; tools for services
  whose scopes are not granted are not registered (logged to stderr).
- Long text is truncated with an explicit `truncated: true` flag and a
  `max_chars` input parameter (default 20000; per text, or a shared budget
  for thread bodies).
- Each tool call is bounded by 2m; `gwork mcp --timeout <d>` replaces it
  and `--timeout 0` disables it (the CLI-wide 1m default does not apply).
- Errors are classified like in the CLI and returned as tool errors
  (`isError: true`) with the hint.
- Nothing is written to stdout except protocol messages; logs go to stderr.

## Testing

- Unit tests per package; Google APIs faked with `httptest.Server` +
  `option.WithEndpoint` / `option.WithHTTPClient`.
- Auth flow tested with a fake token endpoint and a simulated browser callback.
- MCP tools tested with the go-sdk in-memory transport.
- `go vet`, `gofmt`, `golangci-lint`, `go test -race ./...` in CI.

## Build and release

- `Makefile`: `build`, `test`, `lint`, `install`.
- GoReleaser for linux/darwin/windows (amd64/arm64); OAuth client injected from
  CI secrets via ldflags.
