# gwork — Architecture and technical decisions

`gwork` is a single Go binary that exposes access to Gmail, Google Drive, Google
Chat and Google Calendar through two front-ends that share the same core. It is
**read-only by default**; write operations for Gmail, Calendar and Chat are an
explicit double opt-in (see [Write operations](#write-operations)):

- a **CLI** (cobra) for humans and scripts (`--output json` for machines);
- an **MCP server** (`gwork mcp`, stdio) for AI agents.

v1 targets digio's Google Workspace organization with an **Internal** OAuth app,
so no Google verification is needed. The code stays generic (MIT licensed) so it
can be opened later with "bring your own OAuth client".

## Decisions

| Topic | Decision |
|---|---|
| Module / binary | `github.com/madeindigio/gwork`, binary `gwork` (`cmd/gwork`) |
| Go | 1.26 |
| CLI | `github.com/spf13/cobra` |
| Google APIs | `google.golang.org/api` REST clients: `gmail/v1`, `calendar/v3`, `drive/v3`, `chat/v1` |
| OAuth | `golang.org/x/oauth2` + `golang.org/x/oauth2/google`, Desktop client, loopback redirect + PKCE S256 |
| MCP | `github.com/modelcontextprotocol/go-sdk` (official), stdio transport |
| Token storage | `github.com/zalando/go-keyring`; fallback to a `0600` JSON file when no keyring is available |
| Browser | `github.com/pkg/browser` (always print the URL too, for headless/SSH use) |
| Scope policy | Read-only scopes by default, requested incrementally per service; write scopes only with `auth login --write` (see Write operations) |
| License | MIT |

## Layout

```
cmd/gwork/main.go            entry point, calls cli.Execute()
internal/buildinfo/          version, commit, date, default hosted domain (ldflags)
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

If none is found, the error lists these three options and points to
`docs/setup-google-cloud.md`.

The OAuth client is never compiled into the binary (no ldflags, no build
secrets). Every user needs the `credentials.json` of the digio Internal
Desktop client, distributed internally by the maintainers. Desktop client
secrets are not confidential per Google, but they must never be committed to
the repo.

### Login flow (`gwork auth login [--services gmail,calendar,drive,chat] [--write gmail,calendar,chat]`)

1. Listen on `127.0.0.1:0`; redirect URI `http://127.0.0.1:<port>/callback`.
2. Auth URL with `access_type=offline`, `prompt=consent`, PKCE S256,
   random `state`, `include_granted_scopes=true`, and `hd=<domain>` hint when
   `GWORK_HOSTED_DOMAIN` / build-time default is set.
3. Open browser (print URL as well), wait for callback with timeout (default 5m),
   validate `state`, exchange code with verifier.
4. Fetch the account email (`openid email` scopes, userinfo / id_token).
5. Store token + granted scopes under the account email.

Scopes (read-only; write scopes are listed under Write operations):

| Service | Scopes |
|---|---|
| base | `openid`, `https://www.googleapis.com/auth/userinfo.email` |
| gmail | `gmail.readonly` |
| calendar | `calendar.readonly` |
| drive | `drive.readonly` |
| chat | `chat.spaces.readonly`, `chat.messages.readonly`, `chat.memberships.readonly`; optional: `chat.users.readstate.readonly`, `chat.users.sections.readonly` |

Commands check that the stored token covers the service's scopes; if not they
fail with an actionable message: `run: gwork auth login --services chat`.

Optional scopes are requested by every login of the service but are not part
of that check, so tokens stored before a scope was added keep working. Only
the features that need them (`chat unread`, `chat sections`,
`chat spaces --section`) fail, with Google's 403 classified as a missing scope
and the same login hint.

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
gwork chat spaces [--type space|group|dm] [--section <name>] [--max 100]
gwork chat sections                             # sidebar sections (default and custom, e.g. "Favorites")
gwork chat unread [--space ...] [--type] [--section] [--max-per-space 20] [--no-resolve-names]
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
- Chat has no unread flag or counter: `chat unread` reads the user's read
  state of each selected space (`users/me/spaces/{space}/spaceReadState`) and
  lists the messages created after `lastReadTime`, skipping spaces whose last
  activity is older. Thread read states are not considered.
- Chat has no "starred" or "favorite" attribute on spaces: the user's grouping
  of conversations is exposed as sidebar sections (`users/me/sections`, system
  and custom). `--section` accepts a custom section's display name, a section
  id or a resource name.
- Chat sender display names: verified against the digio tenant (2026-09-29),
  the Chat API returns `sender.displayName` for human senders with user
  authentication. When a message lacks it, gwork fills it from space
  memberships (disable with `--no-resolve-names`); if that also fails the
  sender is shown as `users/{id}`.

## MCP server

`gwork mcp` runs over stdio with the official go-sdk. The tools below are
annotated `readOnlyHint: true`; write tools exist only with `--allow-write`
(see Write operations). Each tool has typed input/output structs:

| Tool | Inputs |
|---|---|
| `whoami` | (none) current account, granted services, `write_services`, `allow_send` |
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
| `chat_list_spaces` | `type`, `section`, `max_results` (max 1000) |
| `chat_list_sections` | none |
| `chat_list_unread_messages` | `spaces`, `type`, `section`, `max_per_space` (max 200), `max_chars` |
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

## Write operations

gwork is read-only unless the user opts in twice: once when granting OAuth
scopes and once when starting the MCP server. Drive has no write operations.

### Scopes

Write scopes are requested **in addition to** the read scopes, never instead
of them. A service counts as write-granted only when both its read and write
scopes are stored.

| Service | Extra write scope | Covers |
|---|---|---|
| gmail | `gmail.modify` | drafts, send, labels, trash |
| calendar | `calendar.events` | create, update, delete events |
| chat | `chat.messages.create` | post messages |
| drive | none | not writable; `--write drive` is an error |

### Double opt-in

1. `gwork auth login --write gmail,calendar,chat|all` requests the write scopes
   of the listed services (implying their read scopes). Default: none.
   Missing write access yields `run: gwork auth login --services <svc> --write <svc>`.
2. `gwork mcp --allow-write gmail,calendar,chat|all` registers the write tools
   of the listed services, only if they are also write-granted (otherwise a
   warning with the login hint is logged). Default: none, so the MCP server
   stays read-only. `whoami` reports `write_services` and `allow_send`.

CLI write commands only need step 1: the user is at the keyboard.

### Sending email (`--allow-send`)

Gmail tools that send mail are registered only with `gwork mcp --allow-send`,
which requires `gmail` in `--allow-write` (otherwise an error at startup).
Without it an agent can create drafts but not send them. Calendar and Chat
have no equivalent switch: `--allow-write calendar` already lets an agent
email invitations and `--allow-write chat` post messages to other people. There are no
recipient or domain restrictions.

### CLI write commands

All write commands take `--yes/-y` and `--dry-run` (shared in
`internal/cli/write.go`):

- Without `--yes` the command prints a summary and asks `Proceed? [y/N]` on
  stderr when stdin is a terminal; when it is not, it fails asking for `--yes`.
- `--dry-run` prints the request that would be sent (JSON with `--json`) and
  exits without calling Google; nothing is changed.
- Errors from write calls carry the write login hint.

### MCP write tools

Registered with `addWriteTool`: `readOnlyHint: false`, plus per-tool
`destructiveHint`, `idempotentHint` and `openWorldHint` (tools that reach
other people, like sending mail or posting in Chat, are open-world). Every call
writes an audit line on stderr (Warn level, kept with `--log-level warn`: tool, account, service, duration,
ok/error) that never includes message bodies or other content. When write
tools are enabled the server instructions tell the model that they can change
the user's data, to prefer drafts and to ask the user for explicit
confirmation before sending, deleting or changing anything.

### Prompt-injection rationale

Mail, events and chat messages are untrusted text that an agent reads; an
attacker can embed instructions in them. Write access turns such an injection
from a data leak into an action (sending mail, deleting events). Hence the
opt-in scopes, the opt-in tools, a separate switch for sending, draft-first
guidance, tool annotations that let clients ask for confirmation, and an audit
trail.

Attachments add an exfiltration path: an injected instruction could ask the
agent to attach a local file (keys, tokens) and send it. MCP `path` inputs are
deliberately **not** restricted to a directory: the agent that calls gwork
already has filesystem access and could copy any file into an allowed folder,
so an allowlist would not remove the risk. The control is the client's
tool-call risk classification and user confirmation (send tools are
destructive and open-world), plus `--allow-send` for mail. The audit line
records no attachment content.

### Gmail

Scope `gmail.modify`. `workspace/gmail` builds RFC 5322 messages itself (`net/mail` address validation, CR/LF rejected
in every header value, RFC 2047 Q-encoding for non-ASCII subject and display names, `text/plain;
charset=UTF-8`, quoted-printable body). With attachments the message is `multipart/mixed`: the text part
first, then one base64 part per file (Content-Type from the given type, the extension or content sniffing;
`Content-Disposition` built with `mime.FormatMediaType`, so non-ASCII names use RFC 2231). Messages and
drafts are sent with media upload (`/upload/gmail/v1/...`, `message/rfc822`, single multipart request),
not as `raw` in the JSON body. Attachments total at most 25 MB (`gmail.MaxAttachmentBytes`). Replies fetch the original (metadata),
set `In-Reply-To`/`References`, prefix `Re: ` once (case-insensitive) and reuse its `threadId`.
The recipient defaults to Reply-To or From; `--reply-all` adds the original To and Cc minus the
account's own address (from `users.getProfile`), deduplicated case-insensitively.

| CLI | Confirmation | MCP tool (destructive/idempotent/openWorld) |
|---|---|---|
| `gmail draft create [--reply-to ID [--reply-all]]` | no | `gmail_create_draft` (F/F/F) |
| `gmail draft send ID` | yes | `gmail_send_draft` (T/F/T, needs `--allow-send`) |
| `gmail send` | yes | `gmail_send_message` (T/F/T, needs `--allow-send`) |
| `gmail label ID --add X --remove Y [--thread]` | no | `gmail_modify_labels` (F/T/F) |
| `gmail archive\|mark-read\|mark-unread\|star\|unstar ID [--thread]` | no | `gmail_modify_labels` (remove INBOX, remove UNREAD, add UNREAD, add STARRED, remove STARRED) |
| `gmail trash ID [--thread]` | yes | `gmail_trash` (T/T/F) |
| `gmail untrash ID [--thread]` | no | `gmail_untrash` (F/T/F) |

Labels are given by ID or name (case-insensitive, resolved with one `labels.list` call only when a
non-system label is used; unknown names are an error). Message vs thread: `--thread` on the CLI,
exactly one of `message_id`/`thread_id` in MCP. Bodies are plain text only (`--body`, or
`--body-file PATH|-`; `-` needs `--yes`); HTML is not supported. `draft create` and `send` take
`--attach PATH` (repeatable); files are checked and read before any network call, and the dry run and the
confirmation list names and sizes (never content). The MCP tools `gmail_create_draft` and
`gmail_send_message` take `attachments[]`, each with exactly one source: `path` (read on the machine
running gwork), `content_base64` + `filename`, or `message_id` + `attachment_id` (re-attach an existing
Gmail attachment); `filename` and `content_type` are optional overrides. All commands
support `--dry-run`, which prints the request as JSON without any network call.
MCP descriptions instruct the model to ask the user for explicit confirmation before sending or
trashing, and to prefer drafts.

Stories: GWORK-US-0032, GWORK-US-0035 (attachments).

### Calendar

Scope `calendar.events`. Commands (`gwork calendar event ...`, all take `--dry-run`, `--calendar ID`; all but `respond` take `--yes` and `--send-updates all|external_only|none`):

| Command | Notes |
|---|---|
| `create --summary S --start T [--end T \| --duration 45m] [--all-day] [--attendee EMAIL]... [--description] [--location] [--meet] [--time-zone] [--visibility] [--transparency]` | Timed events default to 30m; `--end` is exclusive for `--all-day`. Confirms only when inviting others and send-updates is not `none`. |
| `update ID [same fields] [--add-attendee] [--remove-attendee]` | PATCH: only given flags change; moving `--start` keeps the duration; existing guests keep their responses. Confirms when the event has other guests. |
| `delete ID` | Always confirms (or `--yes`). An instance id deletes one occurrence of a recurring event. |
| `respond ID --response accepted\|declined\|tentative [--comment]` | Never confirms; the organizer is notified. Fails if you are not an attendee. |

MCP tools (registered only with `--allow-write calendar`, all `readOnlyHint=false`, `openWorldHint=true`):

| Tool | destructive | idempotent |
|---|---|---|
| `calendar_create_event` | false | false |
| `calendar_update_event` | true | true |
| `calendar_delete_event` | true | true |
| `calendar_respond_event` | false | true |

Times use the shared `timeutil` syntax. `send_updates` maps to the API values `all|externalOnly|none` (default `all`).
Descriptions tell the model to get explicit user confirmation before inviting people or changing or deleting events.
Logic lives in `internal/workspace/calendar/write.go` (`CreateEvent`, `UpdateEvent`, `DeleteEvent`, `RespondEvent`).

Story: GWORK-US-0033.

### Chat

Scope `chat.messages.create`.

| Surface | Name | Notes |
|---|---|---|
| CLI | `gwork chat send (--space SPACE \| --to EMAIL) (--text TEXT \| --text-file PATH\|-) [--thread THREAD] [--attach PATH]...` | write flags `--yes`, `--dry-run`; confirmation is always required |
| MCP | `chat_send_message` (`space` xor `user_email`, `text?`, `thread?`, `attachments?`) | destructive=false, idempotent=false, openWorld=true |

- Posts as the user via `spaces.messages.create`. The target is a space (`spaces/X` or bare `X`)
  or, with `--to`/`user_email`, the **existing** DM with that user, resolved through
  `spaces.findDirectMessage`. gwork never creates spaces: if there is no DM yet the command fails
  and asks the user to start the conversation from Chat.
- `text` is limited to 4096 characters (Chat API limit), validated before any call. It is required
  unless the message has attachments.
- Attachments: after resolving the space, each file is uploaded with `media.upload`
  (`/upload/v1/spaces/X/attachments:upload`, up to 200 MB per file, `chat.MaxAttachmentSize`), and the
  returned `attachmentDataRef`s are set on the created message. If an upload fails nothing is posted.
  The API accepts several attachments in one message only when all are images or videos
  (`400: Can't create a message that contains multiple attachments unless the attachments contain only
  media`), so `Validate` rejects mixed sets before any upload.
  CLI `--attach PATH` is repeatable; MCP `attachments[]` items have exactly one of `path` (read on
  the machine running gwork) or `content_base64` + `filename`, plus an optional `content_type`.
  Chat blocks some file types; Drive files cannot be attached through the API (put the link in the text).
- `--thread` / `thread` (`spaces/X/threads/Y`) replies in that thread using
  `messageReplyOption=REPLY_MESSAGE_OR_FAIL`, so it fails instead of starting a new thread. The
  thread must belong to the target space.
- Because a message reaches other people, the CLI always asks for confirmation (summary shows the
  target and an ellipsized preview); `--text-file -` reads stdin and therefore needs `--yes`.
  The MCP tool description instructs the model to show the exact text and target and obtain
  explicit user confirmation before calling.
- Returns the created message (`name`, `space`, `thread`, `create_time`, `text`, ...), the same
  `Message` type as the read tools.

Stories: GWORK-US-0034, GWORK-US-0036 (attachments).

## Testing

- Unit tests per package; Google APIs faked with `httptest.Server` +
  `option.WithEndpoint` / `option.WithHTTPClient`.
- Auth flow tested with a fake token endpoint and a simulated browser callback.
- MCP tools tested with the go-sdk in-memory transport. `testutil.FakeProvider`
  grants all write services by default; `GrantWrite(...)` restricts them.
- `go vet`, `gofmt`, `golangci-lint`, `go test -race ./...` in CI.

## Build and release

- `Makefile`: `build`, `test`, `lint`, `install`.
- GoReleaser for linux/darwin/windows (amd64/arm64); OAuth client injected from
  CI secrets via ldflags.
