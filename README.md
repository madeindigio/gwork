# gwork

**Google Workspace from the terminal and from AI agents: read-only by default, opt-in writes.**

`gwork` is a single Go binary that lets people and AI agents search and read
**Gmail**, **Google Calendar**, **Google Drive** and **Google Chat** with their
own Google account, and, only when you opt in, create drafts and send mail,
manage events and post Chat messages. It works as:

- a **CLI** for humans and scripts (text tables, or `--json` for machines);
- an **MCP server** (`gwork mcp`, stdio) so Claude Code, Claude Desktop and
  other MCP clients can use the same tools.

It was built for digio's Google Workspace (`digio.es`), with an Internal OAuth
app, but the code is generic and MIT licensed: any organization can use it with
its own OAuth client.

Why: agents and scripts need context from mail, calendars, documents and chats,
and the usual options either need write access, send data to a third-party
service, or require an admin-installed bot. `gwork` asks only for read-only
scopes unless you explicitly grant write access, runs on your machine and talks
directly to Google's APIs.

## Contents

- [Features](#features)
- [Security model](#security-model)
- [Install](#install)
- [First-time setup](#first-time-setup)
- [Quick start](#quick-start)
- [Output formats](#output-formats)
- [Accounts](#accounts)
- [MCP server](#mcp-server)
- [Environment variables](#environment-variables)
- [Limitations](#limitations)
- [Development](#development)
- [License](#license)

## Features

| Service | CLI commands | What you get |
|---|---|---|
| Gmail | `gmail search`, `get`, `thread`, `labels`, `attachment` | Gmail query syntax search, messages and whole threads as text (HTML converted), labels, attachment download |
| Calendar | `calendar calendars`, `events`, `get` | Calendar list, events in a time window (recurring events expanded, free-text filter), full event detail with attendees and Meet link |
| Drive | `drive search`, `get`, `read`, `download` | Search My Drive and shared drives by text, name, type, owner, folder or date; metadata; Docs as Markdown, Sheets as CSV, Slides as text; download or export any file |
| Chat | `chat spaces`, `dm`, `messages`, `get`, `search` | Spaces, group chats and DMs; find the DM with a person; messages by time window or thread; text search across spaces |
| Gmail (write) | `gmail draft create`, `draft send`, `send`, `label`, `archive`, `mark-read`, `mark-unread`, `star`, `unstar`, `trash`, `untrash` | Plain-text drafts and replies (threaded, reply-all), sending, labels by name or id, archive, read state, trash |
| Calendar (write) | `calendar event create`, `update`, `delete`, `respond` | Timed or all-day events with guests and Meet link, partial updates that keep guests' responses, delete, RSVP |
| Chat (write) | `chat send` | Post to a space, an existing DM or a thread |
| Auth | `auth login`, `status`, `list`, `use`, `logout` | Browser login with PKCE, several accounts, incremental per-service consent, revocation |
| MCP | `mcp` | 16 read-only tools (15 plus `whoami`) and, with `--allow-write`, 11 write tools mirroring the commands above (see [docs/mcp.md](docs/mcp.md)) |

Write commands need write scopes (`gwork auth login --write gmail,calendar,chat`),
ask for confirmation before sending, deleting or inviting people (`--yes`
skips it) and accept `--dry-run` to print the request without calling Google.

Times such as `--from`, `--since` or `--modified-after` accept RFC 3339, dates
(`2026-09-24`), `today`/`tomorrow`/`yesterday`/`now` and relative values
(`7d` ago, `+3d` ahead; units `m`, `h`, `d`, `w`).

## Security model

- **Read-only scopes by default.** `gwork` requests `gmail.readonly`,
  `calendar.readonly`, `drive.readonly`, `chat.spaces.readonly`,
  `chat.messages.readonly`, `chat.memberships.readonly`,
  `chat.users.readstate.readonly` and `chat.users.sections.readonly` (plus
  `openid` and `userinfo.email` to know the account). Services are consented incrementally:
  log in with only the ones you need.
- **Writes are opt-in, twice.** `gwork auth login --write gmail,calendar,chat`
  adds `gmail.modify`, `calendar.events` and `chat.messages.create` (Drive has
  no write operations). The MCP server still exposes only read tools unless it
  is started with `--allow-write <services>`, and sending mail needs
  `--allow-send` on top. Write tools are annotated as such, and every write
  call is logged on stderr (without content). CLI write commands confirm before
  sending, deleting or inviting people.
- **Your data stays local.** There is no gwork server: the binary calls Google's
  APIs directly with your token and prints to your terminal (or to the MCP
  client you started). What an AI agent then does with tool results depends on
  that agent.
- **Tokens are stored in the OS keyring** (macOS Keychain, Windows Credential
  Manager, Secret Service/KWallet on Linux), under the service name `gwork`. If
  no keyring is available, or with `GWORK_KEYRING=file`, they go to
  `<config>/tokens/<email>.json` with mode `0600`. Tokens never appear in
  output or logs.
- **Standard OAuth for desktop apps**: loopback redirect on `127.0.0.1`, PKCE
  (S256) and a random `state`. With an Internal OAuth app only accounts of the
  owning organization can sign in.
- `gwork auth logout` revokes the token at Google and deletes it locally.
  Workspace admins can trust, restrict or revoke the app centrally
  ([docs/workspace-admin.md](docs/workspace-admin.md)).
- Files written by `gmail attachment` and `drive download` are created with mode
  `0600` and never overwrite an existing file without `--force`.

## Install

### Release binaries

Download the archive for your platform (Linux, macOS, Windows; amd64 or arm64)
from the [GitHub releases](https://github.com/madeindigio/gwork/releases), verify
it and put `gwork` on your `PATH`:

```sh
sha256sum -c checksums.txt --ignore-missing
tar xzf gwork_*_linux_amd64.tar.gz gwork
sudo install gwork /usr/local/bin/
gwork version
```

Binaries never contain an OAuth client: before `gwork auth login` you need a
`credentials.json` (see [First-time setup](#first-time-setup)). macOS binaries are signed with digio's
Developer ID and notarized; Windows binaries are Authenticode-signed (Azure
Trusted Signing). Linux archives are unsigned; use `checksums.txt`.

### go install

Requires Go 1.26+:

```sh
go install github.com/madeindigio/gwork/cmd/gwork@latest
```

As with every build, provide a `credentials.json` (see below).

### From source

```sh
git clone https://github.com/madeindigio/gwork.git && cd gwork
make build                                   # bin/gwork
make build GWORK_HOSTED_DOMAIN=digio.es      # with a default login domain hint
```

## First-time setup

`gwork` needs a Google OAuth client of type **Desktop app** in a Google Cloud
project with the Gmail, Calendar, Drive and Chat APIs enabled.

- Maintainers creating that project and client: follow
  [docs/setup-google-cloud.md](docs/setup-google-cloud.md).
- Workspace super admins (trusting the client, Chat settings, revocation):
  [docs/workspace-admin.md](docs/workspace-admin.md).
- digio users: every user needs the `credentials.json` of the digio Internal
  Desktop client. The maintainers distribute it internally; it is never
  committed to the repository and never compiled into the binary. Save it as
  `<config>/credentials.json` or pass it with `--credentials` /
  `GWORK_CREDENTIALS`.

The OAuth client is resolved in this order (first match wins):

1. `--credentials /path/to/credentials.json`
2. `GWORK_CREDENTIALS=/path/to/credentials.json`
3. `<config>/credentials.json` (e.g. `~/.config/gwork/credentials.json` on
   Linux, `~/Library/Application Support/gwork/credentials.json` on macOS,
   `%AppData%\gwork\credentials.json` on Windows)

If none is found, commands fail with `no OAuth client configured` and a hint
listing these three options.

Then log in (a browser opens; over SSH use `--no-browser` and open the printed
URL):

```sh
gwork auth login                          # all services
gwork auth login --services gmail,drive   # only some; run again later to add more
gwork auth status
```

## Quick start

```sh
# Gmail
gwork gmail search 'from:alice@digio.es newer_than:7d' --max 10
gwork gmail get <messageId>
gwork gmail thread <threadId>
gwork gmail labels
gwork gmail attachment <messageId> <attachmentId> --out report.pdf

# Calendar
gwork calendar calendars
gwork calendar events                                    # today to +7d, primary calendar
gwork calendar events --from tomorrow --to +14d --query standup
gwork calendar get <eventId>

# Drive
gwork drive search budget --type sheet
gwork drive search --name "Q3 plan" --modified-after 30d
gwork drive get <fileId>
gwork drive read <docId>                                 # Markdown for Docs, CSV for Sheets
gwork drive download <docId> --out plan.pdf --export-format pdf

# Writes (after: gwork auth login --write gmail,calendar,chat)
gwork gmail draft create --to bob@digio.es --subject "Notes" --body-file notes.txt
gwork gmail draft create --reply-to <messageId> --reply-all --body "Thanks!"
gwork gmail draft create --to bob@digio.es --subject "Report" --body "Attached." --attach report.pdf
gwork gmail archive <messageId>
gwork calendar event create --summary "1:1" --start "2026-10-02T10:00:00+02:00" --duration 30m --attendee bob@digio.es --meet
gwork calendar event respond <eventId> --response accepted
gwork chat send --space spaces/AAAA1234 --text "Deploy done" --dry-run
gwork chat send --to bob@digio.es --text "Logs" --attach build.log

# Chat
gwork chat spaces --type space
gwork chat dm bob@digio.es
gwork chat messages spaces/AAAA1234 --since 2d
gwork chat get spaces/AAAA1234/messages/BBBB5678
gwork chat search "release date" --since 14d
gwork chat unread                               # messages after your read position, per space
gwork chat sections                             # sidebar sections, e.g. a custom "Favorites"
gwork chat spaces --section Favorites
gwork chat unread --section Favorites
```

Every command has `--help` with all flags and examples.

## Output formats

Text output is meant for people (tables and readable messages). Use `--json`
(or `--output json`) for scripts: results go to stdout as JSON with snake_case
fields; warnings and errors go to stderr, and the exit code is `1` on error.
Text output replaces terminal control characters found in mail, Chat and
Drive content (escape sequences, bidi overrides) with `�`, so a message
cannot drive your terminal; `--json` keeps the exact data.

```sh
gwork gmail search 'is:unread' --json | jq -r '.[] | "\(.date)  \(.from)  \(.subject)"'
gwork calendar events --from today --to today --json | jq length
```

Global flags: `--account`, `--credentials`, `--output text|json` / `--json`,
`--timeout` (default `1m`, `0` disables).

## Accounts

You can log in with several Google accounts. The first login becomes the
default.

```sh
gwork auth list                              # accounts with stored tokens
gwork auth use other@digio.es                # change the default
gwork --account other@digio.es gmail labels  # one command with another account
GWORK_ACCOUNT=other@digio.es gwork mcp       # same through the environment
gwork auth logout [email] [--all] [--no-revoke]
```

Account selection: `--account`, then `GWORK_ACCOUNT`, then the default account
in `<config>/config.json`.

## MCP server

```sh
gwork auth login                                  # once, in a terminal
claude mcp add gwork -- gwork mcp                 # Claude Code
claude mcp add --scope user gwork -- gwork mcp --services gmail,calendar

# Opt-in writes: drafts, labels, events and chat messages (no sending mail)
gwork auth login --write gmail,calendar,chat
claude mcp add gwork -- gwork mcp --allow-write gmail,calendar,chat
```

Only tools of services the account has granted are registered; `whoami` is
always available. Claude Desktop configuration, the full tool reference and
troubleshooting are in [docs/mcp.md](docs/mcp.md).

## Environment variables

| Variable | Meaning |
|---|---|
| `GWORK_CREDENTIALS` | Path to an OAuth client `credentials.json` (after `--credentials`) |
| `GWORK_ACCOUNT` | Account email when `--account` is not given |
| `GWORK_CONFIG_DIR` | Config directory (default: `os.UserConfigDir()/gwork`, e.g. `~/.config/gwork`) |
| `GWORK_KEYRING` | `file` stores tokens in `<config>/tokens/<email>.json` (0600) instead of the OS keyring |
| `GWORK_HOSTED_DOMAIN` | Workspace domain sent as the `hd` login hint (default: build-time value, `digio.es` in releases) |

Build-time only (Makefile / release pipeline): `GWORK_HOSTED_DOMAIN` (the
default `hd` hint). The OAuth client is never a build input.

## Limitations

- **Writes are limited**: Drive is read-only; Gmail bodies are plain text
  (attachments up to 25 MB in total; Chat up to 200 MB per file, several per message only if all are images or videos); `chat send` only posts to existing spaces and DMs (it
  never creates spaces).
- **Chat search is client-side**: Google Chat has no user-level full-text
  search API, so `chat search` lists the messages of your spaces since `--since`
  and filters them locally, stopping after `--max-scan` messages (default
  2000). Narrow `--since` or `--space` for complete results.
- **Chat sender names**: Google normally returns the sender's display name.
  When it is missing, `gwork` looks it up in the space memberships; if that
  fails too, the sender is shown as `users/{id}`.
- **Chat spaces**: group chats and DMs appear only once they have a message;
  `chat dm` fails if you never exchanged a message with that person.
- **Sheets**: `drive read` exports only the **first sheet** as CSV (use
  `drive download` for the whole workbook as xlsx).
- **Drive exports** of Google Docs/Sheets/Slides are limited by Google to about
  **10 MB**; larger files must be opened in the browser. `drive read` returns up
  to `--max-bytes` (default 5 MB) and rejects binary files (PDF, images, Office):
  use `drive download`.
- Gmail search excludes Spam and Trash unless `--include-spam-trash` is given.

## Development

```sh
make build        # bin/gwork (GWORK_HOSTED_DOMAIN=... sets the login hint)
make test         # go test -race ./...
make lint         # golangci-lint v2
make fmt vet      # gofmt -s, go vet
make all          # fmt vet lint test build
make snapshot     # GoReleaser snapshot of every platform into dist/
make release-check
make smoke        # smoke test against the real tenant (docs/smoke-test.md)
```

- Architecture and decisions: [docs/architecture.md](docs/architecture.md).
- How the code is organized and how to add commands, tools or services:
  [AGENTS.md](AGENTS.md).
- The backlog lives in the repository under `docs/.pmngr` and is managed with
  gintrack (project key `GWORK`).
- Releases: pushing a tag `v*` runs `.github/workflows/release.yml`, which
  builds every platform (without any OAuth client), signs and notarizes macOS, Authenticode-signs Windows and publishes
  a GitHub release with `checksums.txt`. Required secrets and variables:
  [docs/release.md](docs/release.md). GoReleaser (`make snapshot`) is only
  used for local, unsigned snapshots.
- Manual end-to-end check before a release:
  [docs/smoke-test.md](docs/smoke-test.md).

Never commit OAuth client secrets, tokens or `credentials*.json`.

## License

[MIT](LICENSE)
