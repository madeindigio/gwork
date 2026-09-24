package mcpserver

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
	"github.com/digio/gwork-cli/internal/workspace/gmail"
)

func gmailTestB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func gmailTestMessage(id, body string) map[string]any {
	return map[string]any{
		"id": id, "threadId": "t1", "labelIds": []string{"INBOX"}, "snippet": "snip " + id,
		"payload": map[string]any{
			"mimeType": "multipart/mixed",
			"headers": []any{
				map[string]any{"name": "From", "value": "Alice <alice@digio.es>"},
				map[string]any{"name": "Subject", "value": "Subject " + id},
				map[string]any{"name": "Date", "value": "Wed, 23 Sep 2026 10:30:00 +0000"},
			},
			"parts": []any{
				map[string]any{"mimeType": "text/html", "body": map[string]any{"data": gmailTestB64("<p>" + body + "</p>")}},
				map[string]any{"mimeType": "application/pdf", "filename": "a.pdf", "body": map[string]any{"attachmentId": "att1", "size": 10}},
			},
		},
	}
}

func gmailTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "is:unread" {
			t.Errorf("q = %q", q.Get("q"))
		}
		if q.Get("maxResults") != "2" {
			t.Errorf("maxResults = %q", q.Get("maxResults"))
		}
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{map[string]any{"id": "m1"}, map[string]any{"id": "m2"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		testutil.WriteJSON(t, w, gmailTestMessage(r.PathValue("id"), "Hello from "+r.PathValue("id")))
	})
	mux.HandleFunc("GET /gmail/v1/users/me/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": r.PathValue("id"), "messages": []any{
			gmailTestMessage("m1", "0123456789"), gmailTestMessage("m2", "abcdefghij"), gmailTestMessage("m3", "zzz"),
		}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"}}})
	})
	return mux
}

func TestGmailToolsRegistered(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)
	tools := listTools(t, cs)
	for _, name := range []string{"gmail_search", "gmail_get_message", "gmail_get_thread", "gmail_list_labels"} {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %s not registered", name)
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s is not annotated read-only", name)
		}
	}
	if d := tools["gmail_search"].Description; !strings.Contains(d, "has:attachment") || !strings.Contains(d, "after:") {
		t.Errorf("gmail_search description lacks query examples: %s", d)
	}
}

func TestGmailToolsNotRegisteredWithoutGrant(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t), auth.Drive), auth.Gmail)
	if _, ok := listTools(t, cs)["gmail_search"]; ok {
		t.Error("gmail_search registered without gmail grant")
	}
}

func TestGmailSearchTool(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)
	out, res := callTool[gmailSearchOutput](t, cs, "gmail_search", map[string]any{"query": "is:unread", "max_results": 2})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.Count != 2 || len(out.Messages) != 2 || out.Messages[0].ID != "m1" || out.Messages[1].Subject != "Subject m2" {
		t.Errorf("out = %+v", out)
	}

	_, res = callTool[gmailSearchOutput](t, cs, "gmail_search", map[string]any{"query": "  "})
	if !res.IsError || !strings.Contains(resultText(res), "query is required") {
		t.Errorf("empty query: %s", resultText(res))
	}
}

func TestGmailGetMessageTool(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)

	out, res := callTool[gmailGetMessageOutput](t, cs, "gmail_get_message", map[string]any{"message_id": "m1"})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	m := out.Message
	if m.Body != "Hello from m1" || m.BodySource != "text/html" || m.HTML != "" || out.Truncated {
		t.Errorf("message = %+v truncated=%v", m, out.Truncated)
	}
	if len(m.Attachments) != 1 || m.Attachments[0] != (gmail.Attachment{AttachmentID: "att1", Filename: "a.pdf", MimeType: "application/pdf", Size: 10}) {
		t.Errorf("attachments = %+v", m.Attachments)
	}

	out, res = callTool[gmailGetMessageOutput](t, cs, "gmail_get_message", map[string]any{"message_id": "m1", "max_chars": 5, "include_html": true})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.Message.Body != "Hello" || out.Message.HTML != "<p>He" || !out.Truncated {
		t.Errorf("truncated message = %q / %q truncated=%v", out.Message.Body, out.Message.HTML, out.Truncated)
	}
}

func TestGmailGetMessageToolNotFound(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)
	_, res := callTool[gmailGetMessageOutput](t, cs, "gmail_get_message", map[string]any{"message_id": "missing"})
	if !res.IsError || !strings.Contains(resultText(res), "not found") {
		t.Errorf("want tool error, got %s", resultText(res))
	}
}

func TestGmailGetThreadTool(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)

	out, res := callTool[gmailGetThreadOutput](t, cs, "gmail_get_thread", map[string]any{"thread_id": "t1"})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.Thread.ID != "t1" || len(out.Thread.Messages) != 3 || out.Truncated {
		t.Fatalf("thread = %+v truncated=%v", out.Thread, out.Truncated)
	}

	out, res = callTool[gmailGetThreadOutput](t, cs, "gmail_get_thread", map[string]any{"thread_id": "t1", "max_chars": 15})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	var bodies []string
	for _, m := range out.Thread.Messages {
		bodies = append(bodies, m.Body)
	}
	if strings.Join(bodies, "|") != "0123456789|abcde|" || !out.Truncated {
		t.Errorf("bodies = %q truncated=%v", bodies, out.Truncated)
	}
}

func TestGmailListLabelsTool(t *testing.T) {
	cs, _ := newTestSession(t, testDeps(t, gmailTestMux(t)), auth.Gmail)
	out, res := callTool[gmailListLabelsOutput](t, cs, "gmail_list_labels", map[string]any{})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if len(out.Labels) != 1 || out.Labels[0].ID != "INBOX" {
		t.Errorf("labels = %+v", out.Labels)
	}
}

func TestTruncateThreadBodies(t *testing.T) {
	th := &gmail.Thread{Messages: []gmail.Message{{Body: "ééé"}, {Body: "b"}}}
	if truncateThreadBodies(th, 10) {
		t.Error("unexpected truncation")
	}
	if !truncateThreadBodies(th, 3) || th.Messages[0].Body != "ééé" || th.Messages[1].Body != "" {
		t.Errorf("bodies = %+v", th.Messages)
	}
}
