# Smoke test against the real tenant

Run this checklist before tagging a release (and after changing auth or a
service) with a real `@digio.es` account. Unit tests fake Google; this is the
only check against the real APIs, the real OAuth client and the admin
policies.

Two parts:

1. **Interactive** steps a person has to do (browser login, reading the
   output, logout/revocation), recorded in the [results table](#results).
2. **Automated** read checks with [`scripts/smoke.sh`](../scripts/smoke.sh)
   (`make smoke`): every read command with `--json`, JSON validity with `jq`,
   and the MCP server over stdio.

Everything is read-only. The script keeps command output in a private
temporary directory (mode 0700, deleted at exit) and prints only ids, counts
and PASS/FAIL, never message content or tokens. Do not paste tokens,
`auth status` storage paths of other people or message contents into the
results.

## Prerequisites

- The binary under test: the release candidate (`make snapshot`, then
  `dist/gwork_<os>_<arch>*/gwork`) or `make build` with the same
  `GWORK_OAUTH_CLIENT_ID` / `GWORK_OAUTH_CLIENT_SECRET` /
  `GWORK_HOSTED_DOMAIN` as the release.
- `jq` and `bash` 4+ (macOS: `brew install bash jq`).
- A `digio.es` account with at least: one mail in the last 30 days (ideally one
  with an attachment), a calendar event in the last/next 30 days, a Google Doc
  in Drive, and a Chat space with a message in the last 90 days.
- Optional: a clean config to test the first-run experience:
  `export GWORK_CONFIG_DIR=$(mktemp -d)`.

```sh
export GWORK_BIN=$PWD/dist/gwork_linux_amd64_v1/gwork   # or bin/gwork
$GWORK_BIN version --json     # check version, commit and "embedded_client": true
```

## 1. Interactive checks

| # | Step | Expected |
|---|---|---|
| I1 | `$GWORK_BIN auth status` before any login | `error: no account logged in` + `hint: run: gwork auth login`, exit 1 |
| I2 | `$GWORK_BIN auth login --services gmail` | Browser opens on the Google consent page with the `digio.es` account preselected; only Gmail read access is requested; terminal prints the URL too and ends with the account and services |
| I3 | `$GWORK_BIN chat spaces` | Fails with a missing-scope message and `hint: run: gwork auth login --services chat` |
| I4 | `$GWORK_BIN auth login` (all services) | Consent adds Calendar, Drive and Chat; `auth status` lists `calendar, chat, drive, gmail` |
| I5 | `$GWORK_BIN auth login --no-browser` (optional, e.g. over SSH) | Only the URL is printed; opening it in a browser on the same machine (or through an SSH port forward of the printed loopback port) completes the login |
| I6 | Personal `@gmail.com` account at the consent screen | Google refuses (`org_internal`); gwork explains that only organization accounts can sign in |
| I7 | `$GWORK_BIN auth status` | Account, services, storage `keyring` (or `file` with `GWORK_KEYRING=file`), token expiry, refresh token present. No token value printed |
| I8 | Token location | macOS Keychain / Windows Credential Manager / Secret Service entry `gwork` for the account; with `GWORK_KEYRING=file` the file `<config>/tokens/<email>.json` has mode `0600` (`ls -l`) |
| I9 | `$GWORK_BIN gmail get <id>` for a message you know | Headers and body readable; HTML-only mail rendered as text |
| I10 | `$GWORK_BIN drive read <docId>` and `<sheetId>` | Doc as Markdown; Sheet as CSV with the "first sheet only" note on stderr |
| I11 | `$GWORK_BIN chat messages <space>` | Sender display names shown where Google provides them, `users/{id}` otherwise |
| I12 | Claude Code: `claude mcp add gwork-smoke -- $GWORK_BIN mcp`, then ask "who am I in gwork?" and "what meetings do I have tomorrow?" | `whoami` and `calendar_list_events` are called and answer correctly; `claude mcp remove gwork-smoke` afterwards |
| I13 | `$GWORK_BIN auth logout` | Token revoked at Google and deleted locally; `https://myaccount.google.com/connections` no longer lists gwork (may take a minute); `auth status` says not logged in |

## 2. Automated checks

Log in first (step I4), then:

```sh
make smoke                       # builds bin/gwork and runs scripts/smoke.sh
# or against another binary:
GWORK_BIN=dist/gwork_linux_amd64_v1/gwork scripts/smoke.sh
```

Configuration (all optional):

| Variable | Default | Used for |
|---|---|---|
| `GWORK_BIN` | `bin/gwork`, else `gwork` on `PATH` | binary under test |
| `GWORK_SMOKE_ACCOUNT` | default account | `--account` for every command |
| `GWORK_SMOKE_SERVICES` | all granted services | limit to e.g. `gmail,drive` |
| `GWORK_SMOKE_MESSAGE_QUERY` | `newer_than:30d` | `gmail search`; must match a message |
| `GWORK_SMOKE_CALENDAR_FROM` / `_TO` | `-30d` / `+30d` | `calendar events` window |
| `GWORK_SMOKE_DRIVE_QUERY` | none (newest files) | `drive search` text |
| `GWORK_SMOKE_DRIVE_FILE_ID` | newest Google Doc | `drive read` and `drive download --export-format pdf` |
| `GWORK_SMOKE_CHAT_SPACE` | first space listed | `chat messages`, `chat get` |
| `GWORK_SMOKE_DM_EMAIL` | skip | `chat dm` |
| `GWORK_SMOKE_CHAT_TEXT` | skip | `chat search` (last 7 days, 500 messages max) |
| `GWORK_SMOKE_MCP_TIMEOUT` | `60` | seconds to wait for each MCP response |
| `GWORK_SMOKE_KEEP` | unset | `1` keeps the output directory for inspection |

What it checks, stopping at the first failure:

| Area | Checks |
|---|---|
| Basics | `version --json`; an invalid `--output` exits non-zero; `auth status --json` has an account and a refresh token; `auth list --json`; requested services are granted |
| Gmail | `labels` contains `INBOX`; `search` finds messages; `get` and `thread` of the first result; `attachment` download of the first attachment when there is one |
| Calendar | `calendars` has a primary calendar; `events` in the window; `get` of the first event |
| Drive | `search`; `get` of the first file; `read` of a Google Doc; `download` of that Doc exported as PDF (checks the `%PDF` header) |
| Chat | `spaces`; `messages` of a space in the last 90 days; `get` of the newest message; `dm` and `search` when configured |
| MCP | `gwork mcp` over stdio: `initialize`, `tools/list` contains `whoami` and every tool of the tested services, all tools `readOnlyHint`; `whoami` returns the same account; `gmail_list_labels` works |

Each `--json` output is validated with `jq -e .`. Example run:

```
gwork smoke test: /home/me/gwork/bin/gwork
PASS  version                            v1.0.0
PASS  invalid --output rejected
PASS  auth-status                        account=me@digio.es services=calendar,chat,drive,gmail storage=keyring
...
PASS  mcp-tools-list                     16 tools, all read-only
PASS  mcp-whoami                         gmail,calendar,drive,chat
all smoke checks passed (21 gwork runs)
```

## Results

Copy this table into the release PR or issue and fill it in. Use `PASS`,
`FAIL` (with a short note or issue id) or `N/A`.

- Version / commit:
- Tester / date:
- Binary (OS/arch, release archive or local build):
- Token storage (keyring / file):

| Check | Linux | macOS | Windows | Notes |
|---|---|---|---|---|
| I1 not logged in message | | | | |
| I2 login, Gmail only | | | | |
| I3 missing scope hint | | | | |
| I4 incremental login, all services | | | | |
| I5 `--no-browser` (optional) | | | | |
| I6 non-digio account refused | | | | |
| I7 `auth status` | | | | |
| I8 token storage location/mode | | | | |
| I9 Gmail message rendering | | | | |
| I10 Drive Doc/Sheet read | | | | |
| I11 Chat sender names | | | | |
| I12 MCP in Claude Code | | | | |
| I13 logout and revocation | | | | |
| `scripts/smoke.sh` basics | | | | |
| `scripts/smoke.sh` Gmail | | | | |
| `scripts/smoke.sh` Calendar | | | | |
| `scripts/smoke.sh` Drive | | | | |
| `scripts/smoke.sh` Chat | | | | |
| `scripts/smoke.sh` MCP | | | | |

On Windows run the script from Git Bash or WSL with `GWORK_BIN` pointing to
`gwork.exe`, or do the automated checks by hand following the table above.
