# gwork as an MCP server

`gwork mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io)
server over **stdio**, so AI agents (Claude Code, Claude Desktop and any other
MCP client) can search and read your Gmail, Google Calendar, Google Drive and
Google Chat. Every tool is **read-only** (annotated `readOnlyHint: true`) and
uses the same code as the CLI.

- [Before you start](#before-you-start)
- [Claude Code](#claude-code)
- [Claude Desktop](#claude-desktop)
- [Other MCP clients](#other-mcp-clients)
- [Server options](#server-options)
- [Tool reference](#tool-reference)
- [Tips for prompts and agents](#tips-for-prompts-and-agents)
- [Troubleshooting](#troubleshooting)

## Before you start

1. Install `gwork` (see the [README](../README.md#install)) and make sure the
   binary is on your `PATH`, or note its absolute path.
2. Log in once from a terminal. The MCP server never opens a browser:

   ```sh
   gwork auth login                          # all services
   gwork auth login --services gmail,drive   # or only some of them
   gwork auth status                         # check account and granted services
   ```

3. Tools are registered at startup for the services the account has granted.
   After logging in again or granting more services, **restart the MCP server**
   (restart the client, or `/mcp` > reconnect in Claude Code).

## Claude Code

Add the server with `claude mcp add`; everything after `--` is the command
Claude Code starts:

```sh
# Current project only (default scope "local", stored in your user config)
claude mcp add gwork -- gwork mcp

# Every project on this machine
claude mcp add --scope user gwork -- gwork mcp

# Shared with the team through .mcp.json in the repository
claude mcp add --scope project gwork -- gwork mcp
```

Variants:

```sh
# A specific account (when you logged in with more than one)
claude mcp add gwork -- gwork mcp --account me@digio.es
# or through the environment
claude mcp add gwork -e GWORK_ACCOUNT=me@digio.es -- gwork mcp

# Only some tool groups (smaller tool list, less context used)
claude mcp add gwork -- gwork mcp --services gmail,calendar

# Two accounts side by side
claude mcp add gwork-work     -- gwork mcp --account me@digio.es
claude mcp add gwork-shared   -- gwork mcp --account team@digio.es --services gmail
```

Check it with `claude mcp list` or `/mcp` inside Claude Code. A project
`.mcp.json` looks like this (do not put account emails in a shared file unless
the whole team uses the same one):

```json
{
  "mcpServers": {
    "gwork": {
      "command": "gwork",
      "args": ["mcp", "--services", "gmail,calendar,drive,chat"]
    }
  }
}
```

## Claude Desktop

Edit the Claude Desktop configuration file (Settings > Developer > Edit Config
opens it):

| OS | File |
|---|---|
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| Linux (unofficial builds) | `~/.config/Claude/claude_desktop_config.json` |

```json
{
  "mcpServers": {
    "gwork": {
      "command": "/usr/local/bin/gwork",
      "args": ["mcp"],
      "env": {
        "GWORK_ACCOUNT": "me@digio.es"
      }
    }
  }
}
```

- Use the **absolute path** of the binary: desktop apps do not inherit your
  shell `PATH` (`which gwork` on macOS/Linux, `where gwork` on Windows, e.g.
  `C:\\Users\\me\\bin\\gwork.exe` with escaped backslashes).
- `env` is optional; add `GWORK_KEYRING`, `GWORK_CONFIG_DIR` or
  `GWORK_CREDENTIALS` there if you use them in your shell.
- Restart Claude Desktop after editing the file.

## Other MCP clients

Any client that launches stdio servers works. Configure:

- **command**: `gwork` (absolute path recommended)
- **args**: `["mcp"]`, plus optional `--services ...`, `--account ...`,
  `--timeout ...`, `--log-level ...`
- **transport**: stdio (the server reads JSON-RPC from stdin and writes only
  protocol messages to stdout)

Quick manual test from a shell:

```sh
{
  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}'
  sleep 3   # keep stdin open until the answers arrive
} | gwork mcp --log-level error | jq -c '{id, tools: [.result.tools[]?.name], whoami: .result.structuredContent}'
```

The [MCP Inspector](https://github.com/modelcontextprotocol/inspector) also
works: `npx @modelcontextprotocol/inspector gwork mcp`.

## Server options

| Flag | Default | Meaning |
|---|---|---|
| `--services` | `all` | Tool groups to register: `gmail`, `calendar`, `drive`, `chat` (comma separated) or `all`. Groups the account has not granted are skipped. |
| `--account` | `$GWORK_ACCOUNT`, then the default account | Google account whose token is used. |
| `--timeout` | `2m` | Bound for each tool call. The CLI-wide `1m` default does not apply to `gwork mcp`: without `--timeout` each call gets 2m; an explicit value replaces it and `--timeout 0` disables the per-call timeout. |
| `--log-level` | `info` | stderr log level: `debug`, `info`, `warn`, `error`. |
| `--credentials` | see README | Path to the OAuth client `credentials.json`; not needed when it is at `<config>/credentials.json` or `GWORK_CREDENTIALS` is set. |

## Tool reference

All tools return **structured content** (JSON matching the output schema) as
well as a text copy. Optional inputs can be omitted. Time inputs accept the
[time expressions](#time-expressions) below. Long text fields are cut to
`max_chars` (default **20000** characters) and flagged with `truncated: true`.

`whoami` is always available. The other tools appear only when their service is
selected with `--services` and granted by the account.

### General

| Tool | Inputs | Output |
|---|---|---|
| `whoami` | none | `account`, `granted_services`, `enabled_services` (tool groups registered in this server), `version` |

### Gmail (`gmail`)

| Tool | Inputs | Output |
|---|---|---|
| `gmail_search` | `query` (Gmail search syntax, required), `max_results` (default 20, max 100) | `messages[]` newest first: `id`, `thread_id`, `from`, `to`, `subject`, `date`, `snippet`, `labels`; `count` |
| `gmail_get_message` | `message_id` (required), `max_chars`, `include_html` (bool) | `message`: headers (from, to, cc, subject, date, message_id), labels, `body` as text (HTML converted), `attachments[]` (`attachment_id`, `filename`, `mime_type`, `size`); `truncated` |
| `gmail_get_thread` | `thread_id` (required), `max_chars` (budget for the whole thread) | `thread` with `messages[]` in chronological order; later bodies are cut first; `truncated` |
| `gmail_list_labels` | none | `labels[]`: `id`, `name`, `type` (`system` or `user`) |

### Calendar (`calendar`)

| Tool | Inputs | Output |
|---|---|---|
| `calendar_list_calendars` | none | `calendars[]`: `id`, `summary`, `primary`, `access_role`, `time_zone` |
| `calendar_list_events` | `calendar_id` (default `primary`), `time_min` (default today 00:00), `time_max` (default +7d; dates are inclusive), `query` (free text), `max_results` (default 50, max 250) | resolved `calendar_id`, `time_min`, `time_max`; `events[]` ordered by start (recurring events expanded): `id`, `summary`, `start`, `end`, `all_day`, `location`, `status`, `organizer`, links |
| `calendar_get_event` | `event_id` (required), `calendar_id` (default `primary`), `max_chars` | `event`: description, attendees with response status, organizer, creator, recurrence, attachments, Meet link; `truncated`, `max_chars` |

### Drive (`drive`)

| Tool | Inputs | Output |
|---|---|---|
| `drive_search` | `query_text` (names and content), `name`, `type` (`doc`, `sheet`, `slides`, `pdf`, `folder`, `image`, `form`, `drawing`), `mime_type`, `owner`, `folder_id`, `modified_after`, `raw_query` (Drive `q` syntax), `max_results` (default 25, max 1000). All ANDed; none = most recent files | `files[]`: `id`, `name`, `mime_type`, `type`, `modified_time`, `size`, `owners`, `web_view_link`, `parents`, `drive_id` |
| `drive_get_file` | `file_id` (required) | `file`: metadata (owners, dates, size, description, link, parents, export formats). No content |
| `drive_read_file` | `file_id` (required), `max_chars` | `file`, `content_mime_type` (`text/markdown`, `text/csv`, `text/plain`, ...), `exported`, `text`, `truncated`, `notes[]` (caveats such as "first sheet only") |

`drive_read_file` returns Docs as Markdown, Sheets as CSV (first sheet only),
Slides as plain text and text-like files as-is. PDFs, images, Office files,
Drawings and Forms return an error; use `gwork drive download` from a terminal
for those.

### Chat (`chat`)

| Tool | Inputs | Output |
|---|---|---|
| `chat_list_spaces` | `type` (`space`, `group`, `dm`), `max_results` (default 100, max 1000) | `spaces[]`: `name` (`spaces/...`), `display_name`, `type`, `last_active_time`, `member_count` |
| `chat_find_dm` | `email` (required) | `space`: the direct message space with that person (error if no DM exists yet) |
| `chat_list_messages` | `space` (required, `spaces/XXX` or `XXX`), `since`, `until`, `thread`, `order` (`asc` or `desc`, default `desc`), `max_results` (default 50, max 1000), `max_chars` (per message) | `messages[]`: `name`, `space`, `thread`, `sender` (`name` = `users/{id}`, `display_name` when known), `text`, `create_time`, attachments; `truncated` |
| `chat_get_message` | `message_name` (required, `spaces/S/messages/M`), `max_chars` | `message`; `truncated` |
| `chat_search_messages` | `text` (required, all words, case-insensitive), `spaces[]` (default all), `since` (default `7d`), `max_results` (default 50, max 1000), `max_scan` (default 2000, max 20000), `max_chars` | `matches[]` newest first, `total_matches`, `scanned`, `spaces_scanned`, `spaces_total`, `cap_reached`, `truncated` |

## Tips for prompts and agents

### Start with `whoami`

It tells the agent which account is in use and which tool groups exist, which
avoids calling tools that are not registered.

### max_chars

Bodies, file contents, event descriptions and chat texts are cut at
`max_chars` characters (default 20000) and `truncated: true` is set. Ask for a
larger value only when needed (it costs context), or a smaller one to skim:

> Read the Drive doc "Q3 plan" with max_chars 5000 and summarize it.

For `gmail_get_thread` the budget is shared by the whole thread, spent in
chronological order.

### Time expressions

`calendar_list_events` (`time_min`, `time_max`), `chat_list_messages`
(`since`, `until`), `chat_search_messages` (`since`) and `drive_search`
(`modified_after`) accept:

| Form | Example | Meaning |
|---|---|---|
| RFC 3339 | `2026-09-24T10:00:00+02:00` | exact instant |
| Date | `2026-09-24` | start of that day (as an end bound: end of that day) |
| Keywords | `today`, `tomorrow`, `yesterday`, `now` | local server time |
| Relative past | `7d`, `-7d`, `24h`, `30m`, `2w` | that long ago |
| Relative future | `+3d`, `+2w` | that far ahead |

### Gmail query syntax

`gmail_search` uses the same syntax as the Gmail search box, for example:

| Query | Finds |
|---|---|
| `from:alice@digio.es subject:invoice` | sender and subject |
| `after:2026/09/01 before:2026/09/15` | date range (`YYYY/MM/DD`) |
| `newer_than:7d`, `older_than:1m` | relative age (`d`, `m`, `y`) |
| `has:attachment filename:pdf` | messages with PDF attachments |
| `is:unread in:inbox` | unread inbox messages |
| `label:project-x` | a label (names from `gmail_list_labels`) |
| `"exact phrase" -from:noreply` | phrase, excluding a sender |

Spam and Trash are not searched. Reference:
<https://support.google.com/mail/answer/7190>.

### Chat search is client-side

Google Chat has no user-level full-text search API, so `chat_search_messages`
downloads messages of each space since `since` and filters them locally. Keep
`since` short and pass `spaces` when you know them; if `cap_reached` is true,
the result may be incomplete.

## Troubleshooting

Logs are written to **stderr** (never stdout). Claude Code shows them with
`claude --debug` or in `/mcp`; Claude Desktop writes them to
`~/Library/Logs/Claude/mcp-server-gwork.log` (macOS) or
`%APPDATA%\Claude\logs\mcp-server-gwork.log` (Windows). Use
`--log-level debug` for one line per tool call.

| Symptom | Cause | Fix |
|---|---|---|
| Only `whoami` is listed; it returns "no account logged in" | No token for the account (or the wrong `--account`/`GWORK_ACCOUNT`) | Run `gwork auth login` in a terminal, check `gwork auth status`, restart the MCP server |
| A service's tools are missing | The service is not in `--services`, or the token lacks its scopes (stderr: `service not granted; its tools are not registered`) | `gwork auth login --services <service>`, then restart the server |
| Tool error "the stored authorization is no longer valid" / "Google rejected the credentials" (`invalid_grant`) | Token revoked or expired by an admin policy | `gwork auth login` again, restart the server |
| Tool error "missing OAuth scope", "permission denied" or "the API is not enabled" | Scope missing or blocked by the Workspace admin | See [workspace-admin.md](workspace-admin.md) and [setup-google-cloud.md](setup-google-cloud.md#6-troubleshooting) |
| Server fails to start from Claude Desktop | Binary not found (desktop apps do not use your shell `PATH`) | Use the absolute path in `command` |
| Keyring errors under a desktop app or over SSH | No OS keyring available in that session | Set `GWORK_KEYRING=file` in the client `env` (tokens in `<config>/tokens/<email>.json`, mode 0600) and log in again with the same setting |
| Tool call times out | Large mailbox, big file or wide Chat search | Narrow the query/time window or raise `--timeout` (e.g. `gwork mcp --timeout 3m`) |
