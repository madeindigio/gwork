package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

func chatWriteMux(t *testing.T, mu *sync.Mutex, bodies *[]map[string]any) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*bodies = append(*bodies, body)
		mu.Unlock()
		space := "spaces/" + r.PathValue("id")
		testutil.WriteJSON(t, w, map[string]any{"name": space + "/messages/M1", "text": body["text"],
			"space": map[string]any{"name": space}, "thread": map[string]any{"name": space + "/threads/T1"},
			"createTime": "2026-09-24T12:00:00Z"})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
	})
	return mux
}

func TestChatSendMessageRegistration(t *testing.T) {
	for _, allow := range []bool{false, true} {
		deps := testDeps(t, http.NotFoundHandler())
		if allow {
			deps.Write = WriteOptions{Services: []auth.Service{auth.Chat}}
		}
		cs, _ := newTestSession(t, deps, auth.Chat)
		tool, has := listTools(t, cs)["chat_send_message"]
		if has != allow {
			t.Fatalf("allow=%v: registered=%v", allow, has)
		}
		if !allow {
			continue
		}
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.IdempotentHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("annotations %+v", a)
		}
		if !strings.Contains(tool.Description, "explicit confirmation") {
			t.Errorf("description lacks confirmation guidance: %s", tool.Description)
		}
	}
}

func TestChatSendMessageCalls(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		wantErr  string
		wantName string
	}{
		{name: "space", args: map[string]any{"space": "AAA", "text": "hi"}, wantName: "spaces/AAA/messages/M1"},
		{name: "dm thread", args: map[string]any{"user_email": "bob@digio.es", "text": "hi", "thread": "spaces/DM1/threads/T"}, wantName: "spaces/DM1/messages/M1"},
		{name: "no target", args: map[string]any{"text": "hi"}, wantErr: "exactly one target"},
		{name: "both targets", args: map[string]any{"space": "A", "user_email": "b@digio.es", "text": "hi"}, wantErr: "exactly one target"},
		{name: "empty text", args: map[string]any{"space": "A", "text": " "}, wantErr: "text is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []map[string]any
			deps := testDeps(t, chatWriteMux(t, &mu, &bodies))
			deps.Write = WriteOptions{Services: []auth.Service{auth.Chat}}
			cs, _ := newTestSession(t, deps, auth.Chat)
			out, res := callTool[chatSendMessageOutput](t, cs, "chat_send_message", tc.args)
			mu.Lock()
			defer mu.Unlock()
			if tc.wantErr != "" {
				if !res.IsError || !strings.Contains(resultText(res), tc.wantErr) || len(bodies) != 0 {
					t.Fatalf("res %s, bodies %v", resultText(res), bodies)
				}
				return
			}
			if res.IsError {
				t.Fatal(resultText(res))
			}
			if out.Message.Name != tc.wantName || len(bodies) != 1 || bodies[0]["text"] != "hi" {
				t.Errorf("out %+v bodies %v", out, bodies)
			}
		})
	}
}
