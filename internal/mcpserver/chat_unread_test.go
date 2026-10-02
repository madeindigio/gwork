package mcpserver

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

// addChatUnreadHandlers adds sections and read states to the chat tool fake:
// section "Favorites" holds space B; A was read 90 minutes ago (m1 is
// unread) and B was never read.
func addChatUnreadHandlers(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /v1/users/me/sections", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"sections": []any{
			map[string]any{"name": "users/9/sections/fav", "displayName": "Favorites", "type": "CUSTOM_SECTION", "sortOrder": 1},
		}})
	})
	mux.HandleFunc("GET /v1/users/9/sections/fav/items", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"sectionItems": []any{map[string]any{"space": "spaces/B"}}})
	})
	mux.HandleFunc("GET /v1/users/me/spaces/{id}/spaceReadState", func(w http.ResponseWriter, r *http.Request) {
		rs := map[string]any{}
		if r.PathValue("id") == "A" {
			rs["lastReadTime"] = testNow.Add(-90 * time.Minute).Format(time.RFC3339)
		}
		testutil.WriteJSON(t, w, rs)
	})
}

func TestChatSectionTools(t *testing.T) {
	mux, _ := chatToolMux(t)
	addChatUnreadHandlers(t, mux)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)

	tools := listTools(t, cs)
	for _, name := range []string{"chat_list_sections", "chat_list_unread_messages"} {
		if tool, ok := tools[name]; !ok || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be registered as readOnlyHint", name)
		}
	}

	sec, res := callTool[chatListSectionsOutput](t, cs, "chat_list_sections", map[string]any{})
	if res.IsError || len(sec.Sections) != 1 || sec.Sections[0].DisplayName != "Favorites" {
		t.Fatalf("sections = %+v %s", sec, resultText(res))
	}

	out, res := callTool[chatListSpacesOutput](t, cs, "chat_list_spaces", map[string]any{"section": "favorites"})
	if res.IsError || len(out.Spaces) != 1 || out.Spaces[0].Name != "spaces/B" || out.Spaces[0].Type != "GROUP_CHAT" {
		t.Errorf("spaces = %+v %s", out, resultText(res))
	}

	_, res = callTool[chatListSpacesOutput](t, cs, "chat_list_spaces", map[string]any{"section": "nope"})
	if !res.IsError || !strings.Contains(resultText(res), "no chat section matches") {
		t.Errorf("unknown section: %s", resultText(res))
	}
}

func TestChatListUnreadMessagesTool(t *testing.T) {
	mux, filters := chatToolMux(t)
	addChatUnreadHandlers(t, mux)
	cs, _ := newTestSession(t, testDeps(t, mux), auth.Chat)

	out, res := callTool[chatListUnreadMessagesOutput](t, cs, "chat_list_unread_messages", map[string]any{"max_chars": 10})
	if res.IsError {
		t.Fatal(resultText(res))
	}
	if out.SpacesTotal != 2 || out.SpacesChecked != 2 || len(out.Spaces) != 2 || !out.Truncated {
		t.Fatalf("out = %+v", out)
	}
	var a, b = out.Spaces[0], out.Spaces[1]
	if a.Space.Name != "spaces/A" {
		a, b = b, a
	}
	if len(a.Messages) != 1 || a.Messages[0].Sender.DisplayName != "Ana" || len([]rune(a.Messages[0].Text)) > 10 {
		t.Errorf("A = %+v", a)
	}
	if len(b.Messages) != 2 || !b.LastReadTime.IsZero() {
		t.Errorf("B = %+v", b)
	}
	if f, _ := filters.Load("A"); f != `createTime > "2026-09-24T10:30:00Z"|createTime desc` {
		t.Errorf("filter = %v", f)
	}

	out, res = callTool[chatListUnreadMessagesOutput](t, cs, "chat_list_unread_messages", map[string]any{
		"section": "Favorites", "max_per_space": 1,
	})
	if res.IsError || len(out.Spaces) != 1 || out.Spaces[0].Space.Name != "spaces/B" ||
		len(out.Spaces[0].Messages) != 1 || !out.Spaces[0].More {
		t.Errorf("section = %+v %s", out, resultText(res))
	}
}
