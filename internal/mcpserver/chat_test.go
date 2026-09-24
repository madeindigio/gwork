package mcpserver

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
)

func chatToolMsg(space, id, text string, at time.Time) map[string]any {
	return map[string]any{
		"name":       space + "/messages/" + id,
		"space":      map[string]any{"name": space},
		"createTime": at.Format(time.RFC3339),
		"sender":     map[string]any{"name": "users/1", "type": "HUMAN"},
		"text":       text,
	}
}

// chatToolMux fakes spaces A and B; members of B are forbidden. It records
// messages.list filters by space id.
func chatToolMux(t *testing.T) (*http.ServeMux, *sync.Map) {
	t.Helper()
	filters := &sync.Map{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		filters.Store("spaces", r.URL.Query().Get("filter"))
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{
			map[string]any{"name": "spaces/A", "displayName": "Team", "spaceType": "SPACE"},
			map[string]any{"name": "spaces/B", "spaceType": "GROUP_CHAT"},
		}})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "users/ana@digio.es" {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM", "spaceType": "DIRECT_MESSAGE"})
	})
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		filters.Store(id, r.URL.Query().Get("filter")+"|"+r.URL.Query().Get("orderBy"))
		sp := "spaces/" + id
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{
			chatToolMsg(sp, "m1", "release notes for "+id, testNow.Add(-time.Hour)),
			chatToolMsg(sp, "m2", strings.Repeat("x", 50), testNow.Add(-2*time.Hour)),
		}})
	})
	mux.HandleFunc("GET /v1/spaces/A/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, chatToolMsg("spaces/A", "m1", "hello world", testNow))
	})
	mux.HandleFunc("GET /v1/spaces/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "A" {
			testutil.WriteGoogleError(w, 403, "forbidden", "no")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"memberships": []any{
			map[string]any{"member": map[string]any{"name": "users/1", "displayName": "Ana"}},
		}})
	})
	return mux, filters
}

func TestChatToolsRegistered(t *testing.T) {
	mux, _ := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	tools := listTools(t, cs)
	for _, name := range []string{"chat_list_spaces", "chat_find_dm", "chat_list_messages", "chat_get_message", "chat_search_messages"} {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("%s not registered", name)
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be readOnlyHint", name)
		}
	}
	if d := tools["chat_search_messages"].Description; !strings.Contains(d, "no server-side text search") {
		t.Errorf("search description must state the limitation: %q", d)
	}
}

func TestChatListSpacesTool(t *testing.T) {
	mux, filters := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	out, res := callTool[chatListSpacesOutput](t, cs, "chat_list_spaces", map[string]any{"type": "group"})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if len(out.Spaces) != 2 || out.Spaces[0].DisplayName != "Team" {
		t.Errorf("spaces = %+v", out.Spaces)
	}
	if f, _ := filters.Load("spaces"); f != `spaceType = "GROUP_CHAT"` {
		t.Errorf("filter = %v", f)
	}
}

func TestChatFindDMTool(t *testing.T) {
	mux, _ := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	out, res := callTool[chatFindDMOutput](t, cs, "chat_find_dm", map[string]any{"email": "ana@digio.es"})
	if res.IsError || out.Space.Name != "spaces/DM" {
		t.Fatalf("got %+v %s", out, resultText(res))
	}
	_, res = callTool[chatFindDMOutput](t, cs, "chat_find_dm", map[string]any{"email": "ghost@digio.es"})
	if !res.IsError || !strings.Contains(resultText(res), "no direct message with ghost@digio.es") {
		t.Errorf("not found: %s", resultText(res))
	}
}

func TestChatListMessagesTool(t *testing.T) {
	mux, filters := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	out, res := callTool[chatMessagesOutput](t, cs, "chat_list_messages", map[string]any{
		"space": "A", "since": "24h", "until": "2026-09-24T12:00:00Z", "thread": "T", "max_chars": 10,
	})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if len(out.Messages) != 2 || !out.Truncated || out.Messages[1].Text != strings.Repeat("x", 10) {
		t.Errorf("out = %+v", out)
	}
	if out.Messages[0].Sender.DisplayName != "Ana" {
		t.Errorf("sender = %+v", out.Messages[0].Sender)
	}
	want := `createTime > "2026-09-23T11:59:59Z" AND createTime < "2026-09-24T12:00:00Z" AND thread.name = spaces/A/threads/T|createTime desc`
	if f, _ := filters.Load("A"); f != want {
		t.Errorf("filter = %v", f)
	}

	// Forbidden memberships: names stay unresolved, no error.
	out, res = callTool[chatMessagesOutput](t, cs, "chat_list_messages", map[string]any{"space": "spaces/B"})
	if res.IsError || out.Messages[0].Sender.Name != "users/1" || out.Messages[0].Sender.DisplayName != "" || out.Truncated {
		t.Errorf("degrade: %+v %s", out, resultText(res))
	}

	_, res = callTool[chatMessagesOutput](t, cs, "chat_list_messages", map[string]any{"space": "A", "since": "never"})
	if !res.IsError {
		t.Error("invalid since: want tool error")
	}
}

func TestChatGetMessageTool(t *testing.T) {
	mux, _ := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	out, res := callTool[chatGetMessageOutput](t, cs, "chat_get_message", map[string]any{"message_name": "spaces/A/messages/m1", "max_chars": 5})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if out.Message.Text != "hello" || !out.Truncated || out.Message.Sender.DisplayName != "Ana" || out.Message.Attachments == nil {
		t.Errorf("out = %+v", out)
	}
}

func TestChatSearchMessagesTool(t *testing.T) {
	mux, filters := chatToolMux(t)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)
	out, res := callTool[chatSearchMessagesOutput](t, cs, "chat_search_messages", map[string]any{"text": "Release NOTES"})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if len(out.Matches) != 2 || out.Matches[0].Name != "spaces/A/messages/m1" || out.Matches[1].Name != "spaces/B/messages/m1" {
		t.Errorf("matches = %+v", out.Matches)
	}
	if out.Scanned != 4 || out.SpacesScanned != 2 || out.CapReached || out.FailedSpaces == nil {
		t.Errorf("out = %+v", out)
	}
	if f, _ := filters.Load("B"); f != `createTime > "2026-09-17T11:59:59Z"|createTime desc` {
		t.Errorf("filter = %v", f)
	}

	out, res = callTool[chatSearchMessagesOutput](t, cs, "chat_search_messages", map[string]any{
		"text": "release", "spaces": []string{"B"}, "max_scan": 1, "since": "1d",
	})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if out.Scanned != 1 || !out.CapReached || out.SpacesTotal != 1 || len(out.Matches) != 1 {
		t.Errorf("cap: %+v", out)
	}
}

func TestChatToolsNotGranted(t *testing.T) {
	mux, _ := chatToolMux(t)
	cs, srv := newTestSession(t, testDeps(t, mux, auth.Gmail), auth.Chat)
	if len(srv.Enabled) != 0 {
		t.Fatalf("enabled = %v", srv.Enabled)
	}
	if _, ok := listTools(t, cs)["chat_list_spaces"]; ok {
		t.Error("chat tools must not be registered without the chat grant")
	}
}

// endlessChatMux serves spaces/E whose messages.list always returns a full
// page of non-matching messages and another page token, and counts the
// messages served.
func endlessChatMux(t *testing.T, served *atomic.Int64) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces/E/messages", func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		msgs := make([]any, size)
		for i := range msgs {
			msgs[i] = chatToolMsg("spaces/E", strconv.Itoa(i), "hay", testNow.Add(-time.Minute))
		}
		served.Add(int64(size))
		testutil.WriteJSON(t, w, map[string]any{"messages": msgs, "nextPageToken": "more"})
	})
	mux.HandleFunc("GET /v1/spaces/E/members", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"memberships": []any{}})
	})
	return mux
}

func TestChatToolsClampLimits(t *testing.T) {
	var served atomic.Int64
	cs, _ := newTestSession(t, testDeps(t, endlessChatMux(t, &served)), auth.Chat)

	out, res := callTool[chatMessagesOutput](t, cs, "chat_list_messages", map[string]any{"space": "E", "max_results": 5000, "max_chars": 1})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if len(out.Messages) != chatMaxResults {
		t.Errorf("list messages returned %d, want cap %d", len(out.Messages), chatMaxResults)
	}

	served.Store(0)
	sout, res := callTool[chatSearchMessagesOutput](t, cs, "chat_search_messages", map[string]any{
		"text": "needle", "spaces": []string{"E"}, "max_scan": 1_000_000,
	})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if sout.Scanned != chatMaxScan || !sout.CapReached || served.Load() > chatMaxScan+1000 {
		t.Errorf("search scanned %d (served %d), want cap %d", sout.Scanned, served.Load(), chatMaxScan)
	}
}
