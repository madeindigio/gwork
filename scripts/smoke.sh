#!/usr/bin/env bash
# Smoke test of a gwork binary against the real Google Workspace tenant.
#
# Runs the non-interactive part of docs/smoke-test.md: it needs an account
# that is already logged in (gwork auth login) and only reads data. It stops
# at the first failure (exit 1). Command output is kept in a temporary
# directory and never echoed: only ids, counts and pass/fail lines are
# printed, so no message content and no token material reach the terminal.
#
# Usage:
#   scripts/smoke.sh                      # or: make smoke
#   GWORK_SMOKE_SERVICES=gmail,drive scripts/smoke.sh
#
# Environment (all optional):
#   GWORK_BIN                    gwork binary (default: bin/gwork, else gwork in PATH)
#   GWORK_SMOKE_ACCOUNT          account to use (passed as --account)
#   GWORK_SMOKE_SERVICES         services to test: gmail,calendar,drive,chat (default: all granted)
#   GWORK_SMOKE_MESSAGE_QUERY    Gmail query that matches at least one message (default: newer_than:30d)
#   GWORK_SMOKE_CALENDAR_FROM    start of the events window (default: -30d)
#   GWORK_SMOKE_CALENDAR_TO      end of the events window (default: +30d)
#   GWORK_SMOKE_DRIVE_QUERY      text for drive search (default: none, newest files)
#   GWORK_SMOKE_DRIVE_FILE_ID    Google Doc id for drive read/download (default: newest Doc found)
#   GWORK_SMOKE_CHAT_SPACE       space for chat messages (default: first space listed)
#   GWORK_SMOKE_CHAT_TEXT        text for chat search (default: skip chat search)
#   GWORK_SMOKE_DM_EMAIL         email for chat dm (default: skip chat dm)
#   GWORK_SMOKE_KEEP=1           keep the temporary output directory
set -euo pipefail
set +x # never trace: commands may carry account data

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${GWORK_BIN:-}" ]]; then
  BIN="$GWORK_BIN"
elif [[ -x "$ROOT/bin/gwork" ]]; then
  BIN="$ROOT/bin/gwork"
else
  BIN="$(command -v gwork || true)"
fi
[[ -n "$BIN" && -x "$BIN" ]] || { echo "smoke: gwork binary not found (set GWORK_BIN or run make build)" >&2; exit 1; }
command -v jq >/dev/null || { echo "smoke: jq is required" >&2; exit 1; }

GLOBAL=()
[[ -n "${GWORK_SMOKE_ACCOUNT:-}" ]] && GLOBAL+=(--account "$GWORK_SMOKE_ACCOUNT")

OUT="$(mktemp -d "${TMPDIR:-/tmp}/gwork-smoke.XXXXXX")"
chmod 700 "$OUT"
cleanup() {
  if [[ "${GWORK_SMOKE_KEEP:-}" == 1 ]]; then
    echo "output kept in $OUT"
  else
    rm -rf "$OUT"
  fi
}
trap cleanup EXIT

STEP=0
pass() { printf 'PASS  %-34s %s\n' "$1" "${2:-}"; }
skip() { printf 'SKIP  %-34s %s\n' "$1" "${2:-}"; }
fail() {
  printf 'FAIL  %-34s %s\n' "$1" "${2:-}" >&2
  exit 1
}

# run NAME ARGS... : runs gwork with --json, stores stdout in $OUT/NAME.json,
# checks the exit code and that stdout is valid JSON. On failure prints only
# gwork's stderr (error + hint), which never contains tokens.
run() {
  local name="$1"
  shift
  STEP=$((STEP + 1))
  local out="$OUT/$name.json" err="$OUT/$name.err"
  if ! "$BIN" "${GLOBAL[@]}" "$@" --json >"$out" 2>"$err"; then
    sed 's/^/      /' "$err" >&2
    fail "$name" "gwork $* exited with an error"
  fi
  jq -e . "$out" >/dev/null 2>&1 || fail "$name" "stdout is not valid JSON"
}

# jqv NAME FILTER : evaluates FILTER on the output of step NAME (raw string).
jqv() { jq -r "$2" "$OUT/$1.json"; }

want() { [[ ",$SERVICES," == *",$1,"* ]]; }

echo "gwork smoke test: $BIN"

# --- basics -----------------------------------------------------------------
run version version
pass version "$(jqv version .version)"

if "$BIN" "${GLOBAL[@]}" version --output yaml >/dev/null 2>&1; then
  fail "invalid --output rejected" "expected a non-zero exit"
fi
pass "invalid --output rejected"

run auth-status auth status
ACCOUNT="$(jqv auth-status .account)"
GRANTED="$(jqv auth-status '.services | join(",")')"
[[ -n "$ACCOUNT" ]] || fail auth-status "no account; run: gwork auth login"
[[ "$(jqv auth-status .has_refresh_token)" == true ]] || fail auth-status "token has no refresh token"
pass auth-status "account=$ACCOUNT services=$GRANTED storage=$(jqv auth-status '.storage // "?"')"

run auth-list auth list
pass auth-list "$(jqv auth-list 'length') account(s)"

SERVICES="${GWORK_SMOKE_SERVICES:-$GRANTED}"
for s in ${SERVICES//,/ }; do
  [[ ",$GRANTED," == *",$s,"* ]] || fail "service $s" "not granted; run: gwork auth login --services $s"
done

# --- gmail ------------------------------------------------------------------
if want gmail; then
  run gmail-labels gmail labels
  jq -e 'any(.[]; .id == "INBOX")' "$OUT/gmail-labels.json" >/dev/null || fail gmail-labels "INBOX label missing"
  pass gmail-labels "$(jqv gmail-labels length) labels"

  run gmail-search gmail search "${GWORK_SMOKE_MESSAGE_QUERY:-newer_than:30d}" --max 5
  n="$(jqv gmail-search length)"
  [[ "$n" -gt 0 ]] || fail gmail-search "no messages match GWORK_SMOKE_MESSAGE_QUERY"
  MSG_ID="$(jqv gmail-search '.[0].id')"
  THREAD_ID="$(jqv gmail-search '.[0].thread_id')"
  pass gmail-search "$n messages"

  run gmail-get gmail get "$MSG_ID"
  [[ "$(jqv gmail-get .id)" == "$MSG_ID" ]] || fail gmail-get "id mismatch"
  pass gmail-get "$MSG_ID"

  run gmail-thread gmail thread "$THREAD_ID"
  pass gmail-thread "$THREAD_ID ($(jqv gmail-thread '.messages | length') messages)"

  ATT="$(jqv gmail-get '(.attachments // [])[0].attachment_id // empty')"
  if [[ -n "$ATT" ]]; then
    run gmail-attachment gmail attachment "$MSG_ID" "$ATT" --out "$OUT/attachment.bin"
    [[ -s "$OUT/attachment.bin" ]] || fail gmail-attachment "empty file"
    pass gmail-attachment "$(wc -c <"$OUT/attachment.bin") bytes"
  else
    skip gmail-attachment "first message has no attachment"
  fi
fi

# --- calendar ---------------------------------------------------------------
if want calendar; then
  run calendar-calendars calendar calendars
  jq -e 'any(.[]; .primary == true)' "$OUT/calendar-calendars.json" >/dev/null || fail calendar-calendars "no primary calendar"
  pass calendar-calendars "$(jqv calendar-calendars length) calendars"

  run calendar-events calendar events --from "${GWORK_SMOKE_CALENDAR_FROM:--30d}" --to "${GWORK_SMOKE_CALENDAR_TO:-+30d}" --max 10
  n="$(jqv calendar-events length)"
  pass calendar-events "$n events"
  if [[ "$n" -gt 0 ]]; then
    EV_ID="$(jqv calendar-events '.[0].id')"
    EV_CAL="$(jqv calendar-events '.[0].calendar_id // "primary"')"
    run calendar-get calendar get "$EV_ID" --calendar "$EV_CAL"
    pass calendar-get "$EV_ID"
  else
    skip calendar-get "no events in the window"
  fi
fi

# --- drive ------------------------------------------------------------------
if want drive; then
  if [[ -n "${GWORK_SMOKE_DRIVE_QUERY:-}" ]]; then
    run drive-search drive search "$GWORK_SMOKE_DRIVE_QUERY" --max 5
  else
    run drive-search drive search --max 5
  fi
  n="$(jqv drive-search length)"
  [[ "$n" -gt 0 ]] || fail drive-search "no files found"
  pass drive-search "$n files"

  run drive-get drive get "$(jqv drive-search '.[0].id')"
  pass drive-get "$(jqv drive-get .mime_type)"

  DOC_ID="${GWORK_SMOKE_DRIVE_FILE_ID:-}"
  if [[ -z "$DOC_ID" ]]; then
    run drive-search-doc drive search --type doc --max 1
    DOC_ID="$(jqv drive-search-doc '.[0].id // empty')"
  fi
  if [[ -n "$DOC_ID" ]]; then
    run drive-read drive read "$DOC_ID" --max-bytes 20000
    pass drive-read "$(jqv drive-read .content_mime_type), $(jqv drive-read .bytes) bytes"
    run drive-download drive download "$DOC_ID" --out "$OUT/download.pdf" --export-format pdf
    [[ "$(head -c 4 "$OUT/download.pdf")" == "%PDF" ]] || fail drive-download "not a PDF"
    pass drive-download "$(wc -c <"$OUT/download.pdf") bytes"
  else
    skip drive-read "no Google Doc found"
  fi
fi

# --- chat -------------------------------------------------------------------
if want chat; then
  run chat-spaces chat spaces --max 20
  n="$(jqv chat-spaces length)"
  pass chat-spaces "$n spaces"

  SPACE="${GWORK_SMOKE_CHAT_SPACE:-$(jqv chat-spaces '.[0].name // empty')}"
  if [[ -n "$SPACE" ]]; then
    run chat-messages chat messages "$SPACE" --since 90d --max 5
    m="$(jqv chat-messages length)"
    pass chat-messages "$SPACE ($m messages)"
    if [[ "$m" -gt 0 ]]; then
      run chat-get chat get "$(jqv chat-messages '.[0].name')"
      pass chat-get "sender display_name set: $(jqv chat-get '(.sender.display_name // "") != ""')"
    else
      skip chat-get "no messages in 90 days"
    fi
  else
    skip chat-messages "no spaces"
  fi

  if [[ -n "${GWORK_SMOKE_DM_EMAIL:-}" ]]; then
    run chat-dm chat dm "$GWORK_SMOKE_DM_EMAIL"
    pass chat-dm "$(jqv chat-dm .name)"
  else
    skip chat-dm "GWORK_SMOKE_DM_EMAIL not set"
  fi

  if [[ -n "${GWORK_SMOKE_CHAT_TEXT:-}" ]]; then
    run chat-search chat search "$GWORK_SMOKE_CHAT_TEXT" --since 7d --max 5 --max-scan 500
    pass chat-search "$(jqv chat-search '.matches | length') matches, $(jqv chat-search .scanned) scanned"
  else
    skip chat-search "GWORK_SMOKE_CHAT_TEXT not set"
  fi
fi

# --- mcp --------------------------------------------------------------------
# Speaks JSON-RPC to "gwork mcp" over a coprocess. Responses are matched by
# id; stderr (logs) goes to a file.
MCP_TIMEOUT="${GWORK_SMOKE_MCP_TIMEOUT:-60}"
coproc MCP { exec "$BIN" "${GLOBAL[@]}" mcp --services "$SERVICES" --log-level warn 2>"$OUT/mcp.err"; }

mcp_send() { printf '%s\n' "$1" >&"${MCP[1]}"; }
# mcp_call ID JSON NAME : sends a request and stores the response in $OUT/NAME.json
mcp_call() {
  local id="$1" req="$2" name="$3" line
  mcp_send "$req"
  while IFS= read -r -t "$MCP_TIMEOUT" line <&"${MCP[0]}"; do
    if [[ "$(jq -r '.id // empty' <<<"$line" 2>/dev/null)" == "$id" ]]; then
      printf '%s\n' "$line" >"$OUT/$name.json"
      jq -e '.error == null' "$OUT/$name.json" >/dev/null || fail "$name" "JSON-RPC error: $(jqv "$name" .error.message)"
      return 0
    fi
  done
  fail "$name" "no response within ${MCP_TIMEOUT}s (see $OUT/mcp.err with GWORK_SMOKE_KEEP=1)"
}

mcp_call 1 '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gwork-smoke","version":"1"}}}' mcp-initialize
pass mcp-initialize "$(jqv mcp-initialize '.result.serverInfo.name + " " + .result.serverInfo.version')"
mcp_send '{"jsonrpc":"2.0","method":"notifications/initialized"}'

mcp_call 2 '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' mcp-tools-list
declare -A EXPECTED=(
  [gmail]="gmail_search gmail_get_message gmail_get_thread gmail_list_labels"
  [calendar]="calendar_list_calendars calendar_list_events calendar_get_event"
  [drive]="drive_search drive_get_file drive_read_file"
  [chat]="chat_list_spaces chat_find_dm chat_list_messages chat_get_message chat_search_messages chat_list_sections chat_list_unread_messages"
)
TOOLS=" $(jqv mcp-tools-list '[.result.tools[].name] | join(" ")') "
[[ "$TOOLS" == *" whoami "* ]] || fail mcp-tools-list "whoami missing"
for s in ${SERVICES//,/ }; do
  for t in ${EXPECTED[$s]:-}; do
    [[ "$TOOLS" == *" $t "* ]] || fail mcp-tools-list "tool $t missing"
  done
done
jq -e 'all(.result.tools[]; .annotations.readOnlyHint == true)' "$OUT/mcp-tools-list.json" >/dev/null ||
  fail mcp-tools-list "a tool is not annotated readOnlyHint"
pass mcp-tools-list "$(jqv mcp-tools-list '.result.tools | length') tools, all read-only"

mcp_call 3 '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}' mcp-whoami
[[ "$(jqv mcp-whoami '.result.isError // false')" == false ]] || fail mcp-whoami "tool error"
[[ "$(jqv mcp-whoami '.result.structuredContent.account')" == "$ACCOUNT" ]] || fail mcp-whoami "account mismatch"
pass mcp-whoami "$(jqv mcp-whoami '.result.structuredContent.enabled_services | join(",")')"

if want gmail; then
  mcp_call 4 '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"gmail_list_labels","arguments":{}}}' mcp-gmail-labels
  [[ "$(jqv mcp-gmail-labels '.result.isError // false')" == false ]] || fail mcp-gmail-labels "tool error"
  pass mcp-gmail-labels "$(jqv mcp-gmail-labels '.result.structuredContent.labels | length') labels"
fi

exec {MCP[1]}>&-
wait "$MCP_PID" 2>/dev/null || true

echo "all smoke checks passed ($STEP gwork runs)"
