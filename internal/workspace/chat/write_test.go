package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/testutil"
)

type sent struct {
	path  string
	query map[string][]string
	body  map[string]any
}

func sendMux(t *testing.T, got *[]sent) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		*got = append(*got, sent{r.URL.Path, r.URL.Query(), body})
		space := "spaces/" + r.PathValue("id")
		resp := map[string]any{"name": space + "/messages/M1", "text": body["text"],
			"space": map[string]any{"name": space}, "createTime": "2026-09-24T12:00:00Z"}
		if th, ok := body["thread"].(map[string]any); ok {
			resp["thread"] = th
		} else {
			resp["thread"] = map[string]any{"name": space + "/threads/T1"}
		}
		testutil.WriteJSON(t, w, resp)
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "users/bob@digio.es" {
			testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
			return
		}
		testutil.WriteGoogleError(w, 404, "notFound", "nope")
	})
	return mux
}

func TestSendMessage(t *testing.T) {
	tests := []struct {
		name       string
		in         SendInput
		wantPath   string
		wantThread string
		wantErr    string
	}{
		{name: "space", in: SendInput{Space: "spaces/AAA", Text: "hi"}, wantPath: "/v1/spaces/AAA/messages"},
		{name: "bare space", in: SendInput{Space: "AAA", Text: "hi"}, wantPath: "/v1/spaces/AAA/messages"},
		{name: "dm", in: SendInput{UserEmail: "bob@digio.es", Text: "hi"}, wantPath: "/v1/spaces/DM1/messages"},
		{name: "thread", in: SendInput{Space: "AAA", Text: "hi", Thread: "spaces/AAA/threads/TT"},
			wantPath: "/v1/spaces/AAA/messages", wantThread: "spaces/AAA/threads/TT"},
		{name: "bare thread", in: SendInput{Space: "AAA", Text: "hi", Thread: "TT"},
			wantPath: "/v1/spaces/AAA/messages", wantThread: "spaces/AAA/threads/TT"},
		{name: "dm thread", in: SendInput{UserEmail: "bob@digio.es", Text: "hi", Thread: "spaces/DM1/threads/X"},
			wantPath: "/v1/spaces/DM1/messages", wantThread: "spaces/DM1/threads/X"},
		{name: "thread mismatch", in: SendInput{Space: "AAA", Text: "hi", Thread: "spaces/BBB/threads/T"}, wantErr: "does not belong"},
		{name: "dm thread mismatch", in: SendInput{UserEmail: "bob@digio.es", Text: "hi", Thread: "spaces/BBB/threads/T"}, wantErr: "does not belong"},
		{name: "dm bare thread", in: SendInput{UserEmail: "bob@digio.es", Text: "hi", Thread: "T"}, wantErr: "full name"},
		{name: "missing dm", in: SendInput{UserEmail: "eve@digio.es", Text: "hi"}, wantErr: "start the conversation from Google Chat"},
		{name: "empty text", in: SendInput{Space: "AAA", Text: "  \n"}, wantErr: "text is required"},
		{name: "too long", in: SendInput{Space: "AAA", Text: strings.Repeat("é", MaxTextLength+1)}, wantErr: "limit is 4096"},
		{name: "max length ok", in: SendInput{Space: "AAA", Text: strings.Repeat("é", MaxTextLength)}, wantPath: "/v1/spaces/AAA/messages"},
		{name: "no target", in: SendInput{Text: "hi"}, wantErr: "exactly one target"},
		{name: "two targets", in: SendInput{Space: "A", UserEmail: "bob@digio.es", Text: "hi"}, wantErr: "exactly one target"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []sent
			svc := newTestService(t, sendMux(t, &got))
			m, err := SendMessage(context.Background(), svc, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if tc.name == "missing dm" && !errors.Is(err, ErrDirectMessageNotFound) {
					t.Errorf("want ErrDirectMessageNotFound: %v", err)
				}
				if len(got) != 0 {
					t.Errorf("unexpected send: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].path != tc.wantPath {
				t.Fatalf("sent %+v, want path %s", got, tc.wantPath)
			}
			if got[0].body["text"] != tc.in.Text {
				t.Errorf("body text mismatch")
			}
			th, _ := got[0].body["thread"].(map[string]any)
			opt := got[0].query["messageReplyOption"]
			if tc.wantThread != "" {
				if th["name"] != tc.wantThread {
					t.Errorf("thread = %v, want %s", th, tc.wantThread)
				}
				if len(opt) != 1 || opt[0] != "REPLY_MESSAGE_OR_FAIL" {
					t.Errorf("messageReplyOption = %v", opt)
				}
			} else if th != nil || len(opt) != 0 {
				t.Errorf("unexpected thread %v / option %v", th, opt)
			}
			if m.Name == "" || m.Space == "" || m.Text != tc.in.Text || m.CreateTime.IsZero() {
				t.Errorf("result %+v", m)
			}
		})
	}
}
