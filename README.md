# gwork

**Read-only Google Workspace from the terminal and from AI agents.**

`gwork` is a single Go binary that lets people and AI agents search and read
**Gmail**, **Google Calendar**, **Google Drive** and **Google Chat** with their
own Google account. It works as:

- a **CLI** for humans and scripts (text tables, or `--json` for machines);
- an **MCP server** (`gwork mcp`, stdio) so Claude Code, Claude Desktop and
  other MCP clients can use the same read-only tools.

It was built for digio's Google Workspace (`digio.es`), with an Internal OAuth
app, but the code is generic and MIT licensed: any organization can use it with
its own OAuth client.

Why: agents and scripts need context from mail, calendars, documents and chats,
and the usual options either need write access, send data to a third-party
service, or require an admin-installed bot. `gwork` asks only for read-only
scopes, runs on your machine and talks directly to Google's APIs.

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
| Auth | `auth login`, `status`, `list`, `use`, `logout` | Browser login with PKCE, several accounts, incremental per-service consent, revocation |
| MCP | `mcp` | 16 read-only tools (15 plus `whoami`) mirroring the commands above (see [docs/mcp.md](docs/mcp.md)) |

Times such as `--from`, `--since` or `--modified-after` accept RFC 3339, dates
(`2026-09-24`), `today`/`tomorrow`/`yesterday`/`now` and relative values
(`7d` ago, `+3d` ahead; units `m`, `h`, `d`, `w`).

## Security model

- **Read-only scopes only.** `gwork` requests `gmail.readonly`,
  `calendar.readonly`, `drive.readonly`, `chat.spaces.readonly`,
  `chat.messages.readonly` and `chat.memberships.readonly` (plus `openid` and
  `userinfo.email` to know the account). It cannot send, modify or delete
  anything. Services are consented incrementally: log in with only the ones you
  need.
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
from the [GitHub releases](https://github.com/digio/gwork-cli/releases), verify
it and put `gwork` on your `PATH`:

```sh
sha256sum -c checksums.txt --ignore-missing
tar xzf gwork_*_linux_amd64.tar.gz gwork
sudo install gwork /usr/local/bin/
gwork version
```

Release builds embed digio's OAuth client, so digio users can run
`gwork auth login` right away. The binaries are not code-signed yet (planned):
on macOS remove the quarantine flag with
`xattr -d com.apple.quarantine /usr/local/bin/gwork`; on Windows SmartScreen
may ask for confirmation.

### go install

Requires Go 1.26+:

```sh
go install github.com/digio/gwork-cli/cmd/gwork@latest
```

This build has **no embedded OAuth client**: provide a `credentials.json` (see
below).

### From source

```sh
git clone https://github.com/digio/gwork-cli.git && cd gwork-cli
make build                                   # bin/gwork, no embedded client
make build GWORK_OAUTH_CLIENT_ID=... GWORK_OAUTH_CLIENT_SECRET=... GWORK_HOSTED_DOMAIN=digio.es
```

## First-time setup

`gwork` needs a Google OAuth client of type **Desktop app** in a Google Cloud
project with the Gmail, Calendar, Drive and Chat APIs enabled.

- Maintainers creating that project and client: follow
  [docs/setup-google-cloud.md](docs/setup-google-cloud.md).
- Workspace super admins (trusting the client, Chat settings, revocation):
  [docs/workspace-admin.md](docs/workspace-admin.md).
- digio users with a release binary: nothing to do, the client is embedded.

The OAuth client is resolved in this order (first match wins):

1. `--credentials /path/to/credentials.json`
2. `GWORK_CREDENTIALS=/path/to/credentials.json`
3. `<config>/credentials.json` (e.g. `~/.config/gwork/credentials.json` on
   Linux, `~/Library/Application Support/gwork/credentials.json` on macOS,
   `%AppData%\gwork\credentials.json` on Windows)
4. The client embedded at build time

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

# Chat
gwork chat spaces --type space
gwork chat dm bob@digio.es
gwork chat messages spaces/AAAA1234 --since 2d
gwork chat get spaces/AAAA1234/messages/BBBB5678
gwork chat search "release date" --since 14d
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

Build-time only (Makefile / release pipeline): `GWORK_OAUTH_CLIENT_ID`,
`GWORK_OAUTH_CLIENT_SECRET`, `GWORK_HOSTED_DOMAIN`.

## Limitations

- **Read-only by design**: no sending, editing, labeling or deleting.
- **Chat search is client-side**: Google Chat has no user-level full-text
  search API, so `chat search` lists the messages of your spaces since `--since`
  and filters them locally, stopping after `--max-scan` messages (default
  2000). Narrow `--since` or `--space` for complete results.
- **Chat sender names**: the Chat API often returns only `users/{id}` for
  senders. `gwork` fills in display names from space memberships when Google
  provides them; otherwise the sender is shown as `users/{id}`.
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
make build        # bin/gwork (add GWORK_OAUTH_* to embed a client)
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
  builds with GoReleaser and embeds the OAuth client from the repository
  secrets `GWORK_OAUTH_CLIENT_ID`, `GWORK_OAUTH_CLIENT_SECRET` and
  `GWORK_HOSTED_DOMAIN`. macOS notarization and Windows Authenticode signing
  can be enabled later (digio has the certificates); placeholders are in
  `.goreleaser.yaml` and the workflow.
- Manual end-to-end check before a release:
  [docs/smoke-test.md](docs/smoke-test.md).

Never commit OAuth client secrets, tokens or `credentials*.json`.

## License

[MIT](LICENSE)
