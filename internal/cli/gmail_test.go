package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
)

func gmailB64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func gmailHeaders(kv ...string) []any {
	var hs []any
	for i := 0; i+1 < len(kv); i += 2 {
		hs = append(hs, map[string]any{"name": kv[i], "value": kv[i+1]})
	}
	return hs
}

// gmailTestMux fakes the Gmail endpoints used by the CLI commands.
func gmailTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	full := func(id, subject, body string) map[string]any {
		return map[string]any{
			"id": id, "threadId": "t1", "labelIds": []string{"INBOX"}, "snippet": body,
			"payload": map[string]any{
				"mimeType": "multipart/mixed",
				"headers": gmailHeaders("From", "Alice <alice@digio.es>", "To", "bob@digio.es",
					"Subject", subject, "Date", "Wed, 23 Sep 2026 10:30:00 +0000"),
				"parts": []any{
					map[string]any{"mimeType": "multipart/alternative", "parts": []any{
						map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": gmailB64(body)}},
						map[string]any{"mimeType": "text/html", "body": map[string]any{"data": gmailB64("<p><b>" + body + "</b></p>")}},
					}},
					map[string]any{"mimeType": "application/pdf", "filename": "q3.pdf",
						"body": map[string]any{"attachmentId": "att1", "size": 2048}},
				},
			},
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); q != "from:alice" {
			t.Errorf("q = %q", q)
		}
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{map[string]any{"id": "m1"}, map[string]any{"id": "m2"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "missing" {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		testutil.WriteJSON(t, w, full(id, "Report "+id, "Body of "+id))
	})
	mux.HandleFunc("GET /gmail/v1/users/me/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": r.PathValue("id"), "messages": []any{
			full("m1", "Report", "first message"), full("m2", "Re: Report", "second message"),
		}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{
			map[string]any{"id": "Label_1", "name": "Projects", "type": "user"},
			map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"},
		}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}/attachments/{att}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"data": gmailB64("PDF-DATA"), "size": 8})
	})
	return mux
}

func TestGmailSearchCLI(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t))

	out, errOut, code := runCLI(t, p, "gmail", "search", "from:alice")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"DATE", "SUBJECT", "Report m1", "Report m2", "Alice <alice@digio.es>"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "m1") > strings.Index(out, "m2") {
		t.Errorf("results out of order:\n%s", out)
	}

	out, errOut, code = runCLI(t, p, "gmail", "search", "from:alice", "--json", "--max", "5")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var res []map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(res) != 2 || res[0]["id"] != "m1" || res[0]["thread_id"] != "t1" || res[0]["subject"] != "Report m1" {
		t.Errorf("json = %v", res)
	}
}

func TestGmailSearchCLIInvalidMax(t *testing.T) {
	isolateConfig(t)
	_, errOut, code := runCLI(t, testutil.NewFakeProvider(t, gmailTestMux(t)), "gmail", "search", "x", "--max", "0")
	if code != 1 || !strings.Contains(errOut, "--max") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
}

func TestGmailGetCLI(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t))

	out, errOut, code := runCLI(t, p, "gmail", "get", "m1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"Subject:", "Report m1", "Body of m1", "q3.pdf", "att1", "Attachments (1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<b>") {
		t.Errorf("html leaked into text output:\n%s", out)
	}

	out, _, code = runCLI(t, p, "gmail", "get", "m1", "--raw-html")
	if code != 0 || !strings.Contains(out, "<p><b>Body of m1</b></p>") {
		t.Errorf("--raw-html output (code %d):\n%s", code, out)
	}

	out, errOut, code = runCLI(t, p, "gmail", "get", "m1", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(out), &msg); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if msg["body"] != "Body of m1" || msg["body_source"] != "text/plain" {
		t.Errorf("json = %v", msg)
	}
	if _, ok := msg["html"]; ok {
		t.Error("html present without --raw-html")
	}
	atts, _ := msg["attachments"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["attachment_id"] != "att1" {
		t.Errorf("attachments = %v", msg["attachments"])
	}
}

func TestGmailGetCLINotFound(t *testing.T) {
	isolateConfig(t)
	_, errOut, code := runCLI(t, testutil.NewFakeProvider(t, gmailTestMux(t)), "gmail", "get", "missing")
	if code != 1 || !strings.Contains(errOut, "error:") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
}

func TestGmailCLIScopeMissing(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t), auth.Drive)
	_, errOut, code := runCLI(t, p, "gmail", "labels")
	if code != 1 || !strings.Contains(errOut, "gmail") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
}

func TestGmailThreadCLI(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t))

	out, errOut, code := runCLI(t, p, "gmail", "thread", "t1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"=== Message 1/2 ===", "=== Message 2/2 ===", "first message", "second message"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	out, _, code = runCLI(t, p, "gmail", "thread", "t1", "--json")
	var th struct {
		ID       string `json:"id"`
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &th); err != nil || code != 0 {
		t.Fatalf("decode (code %d): %v\n%s", code, err, out)
	}
	if th.ID != "t1" || len(th.Messages) != 2 || th.Messages[1].Body != "second message" {
		t.Errorf("thread = %+v", th)
	}
}

func TestGmailLabelsCLI(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t))

	out, errOut, code := runCLI(t, p, "gmail", "labels")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Projects") || strings.Index(out, "INBOX") > strings.Index(out, "Projects") {
		t.Errorf("labels output:\n%s", out)
	}

	out, _, _ = runCLI(t, p, "gmail", "labels", "--json")
	var labels []map[string]string
	if err := json.Unmarshal([]byte(out), &labels); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(labels) != 2 || labels[0]["id"] != "INBOX" || labels[1]["type"] != "user" {
		t.Errorf("labels = %v", labels)
	}
}

func TestGmailAttachmentCLI(t *testing.T) {
	isolateConfig(t)
	p := testutil.NewFakeProvider(t, gmailTestMux(t))
	dest := filepath.Join(t.TempDir(), "q3.pdf")

	out, errOut, code := runCLI(t, p, "gmail", "attachment", "m1", "att1", "--out", dest)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "wrote 8 bytes") {
		t.Errorf("output = %q", out)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "PDF-DATA" {
		t.Errorf("file = %q, %v", b, err)
	}
	if fi, err := os.Stat(dest); err != nil {
		t.Error(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}

	// Refuses to overwrite without --force.
	if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code = runCLI(t, p, "gmail", "attachment", "m1", "att1", "--out", dest)
	if code != 1 || !strings.Contains(errOut, "--force") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
	if b, _ := os.ReadFile(dest); string(b) != "keep" {
		t.Errorf("file overwritten: %q", b)
	}

	out, errOut, code = runCLI(t, p, "gmail", "attachment", "m1", "att1", "--out", dest, "--force", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil || res["size"] != float64(8) || res["path"] != dest {
		t.Errorf("json = %v (%v)", res, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "PDF-DATA" {
		t.Errorf("file = %q after --force", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Errorf("leftover temporary files: %v", entries)
	}
}

func TestGmailAttachmentCLIRequiresOut(t *testing.T) {
	isolateConfig(t)
	_, errOut, code := runCLI(t, testutil.NewFakeProvider(t, gmailTestMux(t)), "gmail", "attachment", "m1", "att1")
	if code != 1 || !strings.Contains(errOut, "--out") {
		t.Errorf("code=%d stderr=%s", code, errOut)
	}
}
