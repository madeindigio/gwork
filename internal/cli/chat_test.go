package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
)

// chatCLIFake serves two spaces (A with a resolvable member, B whose
// members listing is forbidden) and records query parameters.
type chatCLIFake struct {
	mu      sync.Mutex
	queries map[string][]string
}

func (f *chatCLIFake) record(key, val string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries[key] = append(f.queries[key], val)
}

func (f *chatCLIFake) get(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries[key]...)
}

func chatCLIMsg(space, id, text string, at time.Time) map[string]any {
	return map[string]any{
		"name":       space + "/messages/" + id,
		"space":      map[string]any{"name": space},
		"thread":     map[string]any{"name": space + "/threads/t" + id},
		"createTime": at.Format(time.RFC3339),
		"sender":     map[string]any{"name": "users/1", "type": "HUMAN"},
		"text":       text,
	}
}

func newChatCLIFake(t *testing.T) (*chatCLIFake, *http.ServeMux) {
	t.Helper()
	f := &chatCLIFake{queries: map[string][]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		f.record("spaces.filter", r.URL.Query().Get("filter"))
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{
			map[string]any{"name": "spaces/A", "displayName": "Team A", "spaceType": "SPACE",
				"lastActiveTime": "2026-09-24T10:00:00Z", "membershipCount": map[string]any{"joinedDirectHumanUserCount": 3}},
			map[string]any{"name": "spaces/B", "spaceType": "DIRECT_MESSAGE"},
		}})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "users/ana@digio.es" {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/B", "spaceType": "DIRECT_MESSAGE"})
	})
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.record(id+".filter", r.URL.Query().Get("filter"))
		f.record(id+".orderBy", r.URL.Query().Get("orderBy"))
		space := "spaces/" + id
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{
			chatCLIMsg(space, "m1", "Deploy done in "+id, testNow.Add(-time.Hour)),
			chatCLIMsg(space, "m2", "lunch?", testNow.Add(-2*time.Hour)),
		}})
	})
	mux.HandleFunc("GET /v1/spaces/A/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		m := chatCLIMsg("spaces/A", "m1", "Deploy done", testNow.Add(-time.Hour))
		m["attachment"] = []any{map[string]any{"name": "att1", "contentName": "log.txt", "contentType": "text/plain"}}
		testutil.WriteJSON(t, w, m)
	})
	mux.HandleFunc("GET /v1/spaces/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		f.record("members", r.PathValue("id"))
		if r.PathValue("id") != "A" {
			testutil.WriteGoogleError(w, 403, "forbidden", "no access")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"memberships": []any{
			map[string]any{"member": map[string]any{"name": "users/1", "displayName": "Ana", "type": "HUMAN"}},
		}})
	})
	return f, mux
}

func TestChatSpacesCLI(t *testing.T) {
	f, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "spaces", "--type", "space")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"NAME", "spaces/A", "Team A", "SPACE", "3", "2026-09-24 10:00", "spaces/B"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if got := f.get("spaces.filter"); len(got) != 1 || got[0] != `spaceType = "SPACE"` {
		t.Errorf("filter = %q", got)
	}

	out, _, code = runCLI(t, p, "chat", "spaces", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var spaces []map[string]any
	if err := json.Unmarshal([]byte(out), &spaces); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(spaces) != 2 || spaces[0]["display_name"] != "Team A" || spaces[0]["member_count"] != float64(3) || spaces[0]["last_active_time"] == nil {
		t.Errorf("json = %v", spaces)
	}

	_, errOut, code = runCLI(t, p, "chat", "spaces", "--type", "nope")
	if code != 1 || !strings.Contains(errOut, "invalid space type") {
		t.Errorf("invalid type: code %d, %s", code, errOut)
	}
}

func TestChatDMCLI(t *testing.T) {
	_, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "dm", "ana@digio.es")
	if code != 0 || !strings.Contains(out, "spaces/B") || !strings.Contains(out, "DIRECT_MESSAGE") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	_, errOut, code = runCLI(t, p, "chat", "dm", "ghost@digio.es")
	if code != 1 || !strings.Contains(errOut, "no direct message with ghost@digio.es") {
		t.Errorf("not found: code %d, %s", code, errOut)
	}
}

func TestChatMessagesCLI(t *testing.T) {
	f, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "messages", "A", "--since", "7d", "--until", "2026-09-24T12:00:00Z", "--thread", "T", "--order", "asc")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"2026-09-24 11:00  Ana (users/1)  [spaces/A/messages/m1]", "  Deploy done in A", "lunch?"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	wantFilter := `createTime > "2026-09-17T11:59:59Z" AND createTime < "2026-09-24T12:00:00Z" AND thread.name = spaces/A/threads/T`
	if got := f.get("A.filter"); len(got) != 1 || got[0] != wantFilter {
		t.Errorf("filter = %q", got)
	}
	if got := f.get("A.orderBy"); got[0] != "createTime asc" {
		t.Errorf("orderBy = %q", got)
	}

	// JSON, no name resolution, default desc order.
	out, _, code = runCLI(t, p, "chat", "messages", "spaces/A", "--json", "--no-resolve-names")
	if code != 0 {
		t.Fatal(code)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(out), &msgs); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sender, _ := msgs[0]["sender"].(map[string]any)
	if len(msgs) != 2 || sender["name"] != "users/1" || sender["display_name"] != nil || msgs[0]["create_time"] != "2026-09-24T11:00:00Z" {
		t.Errorf("json = %v", msgs)
	}
	if att, ok := msgs[0]["attachments"].([]any); !ok || len(att) != 0 {
		t.Errorf("attachments = %v", msgs[0]["attachments"])
	}
	if got := f.get("A.orderBy"); got[1] != "createTime desc" || f.get("A.filter")[1] != "" {
		t.Errorf("defaults: orderBy %q filter %q", got, f.get("A.filter"))
	}
	if got := f.get("members"); len(got) != 1 {
		t.Errorf("members listed %v, want once (first run only)", got)
	}

	// Forbidden memberships degrade to users/{id}.
	out, errOut, code = runCLI(t, p, "chat", "messages", "B")
	if code != 0 || !strings.Contains(out, "users/1  [spaces/B/messages/m1]") {
		t.Errorf("degrade: code %d\n%s\n%s", code, out, errOut)
	}

	_, errOut, code = runCLI(t, p, "chat", "messages", "A", "--since", "bogus")
	if code != 1 || !strings.Contains(errOut, "invalid time") {
		t.Errorf("bad since: %d %s", code, errOut)
	}
}

func TestChatGetCLI(t *testing.T) {
	_, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux)
	out, errOut, code := runCLI(t, p, "chat", "get", "spaces/A/messages/m1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"Name:", "spaces/A/messages/m1", "Ana (users/1)", "log.txt", "Deploy done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	out, _, code = runCLI(t, p, "chat", "get", "spaces/A/messages/m1", "--json")
	var m map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &m) != nil || m["text"] != "Deploy done" || m["space"] != "spaces/A" {
		t.Errorf("json: %d %s", code, out)
	}
	_, errOut, code = runCLI(t, p, "chat", "get", "m1")
	if code != 1 || !strings.Contains(errOut, "invalid message name") {
		t.Errorf("bad name: %d %s", code, errOut)
	}
}

func TestChatSearchCLI(t *testing.T) {
	f, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "search", "deploy DONE")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"spaces/A/messages/m1", "spaces/B/messages/m1", "2 of 2 matches; scanned 4 messages in 2/2 spaces"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "lunch") {
		t.Errorf("non-matching message in output:\n%s", out)
	}
	if got := f.get("A.filter"); got[0] != `createTime > "2026-09-17T11:59:59Z"` {
		t.Errorf("filter = %q", got)
	}

	out, errOut, code = runCLI(t, p, "chat", "search", "lunch", "--space", "A", "--max-scan", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var res struct {
		Matches    []map[string]any `json:"matches"`
		Scanned    int              `json:"scanned"`
		CapReached bool             `json:"cap_reached"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The fake ignores pageSize and returns 2 messages; only the newest
	// one fits the scan budget, so "lunch?" is never examined.
	if res.Matches == nil || len(res.Matches) != 0 || res.Scanned != 1 || !res.CapReached {
		t.Errorf("json = %+v", res)
	}
	if !strings.Contains(errOut, "results may be incomplete") {
		t.Errorf("missing cap warning on stderr: %q", errOut)
	}
}

func TestChatNotGranted(t *testing.T) {
	_, mux := newChatCLIFake(t)
	p := testutil.NewFakeProvider(t, mux, auth.Gmail)
	_, errOut, code := runCLI(t, p, "chat", "spaces")
	if code != 1 || !strings.Contains(errOut, "gwork auth login --services chat") {
		t.Errorf("code %d: %s", code, errOut)
	}
}
