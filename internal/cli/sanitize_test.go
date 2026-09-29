package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/testutil"
)

// Terminal escape payloads: OSC 52 writes the clipboard, CSI clears the
// screen, U+202E reverses the text direction.
const (
	evilOSC52 = "\x1b]52;c;cm0gLXJmIH4=\x07"
	evilCSI   = "\x1b[2J\x1b[H"
	evilBidi  = "\u202E"
)

// assertNoEscapes fails when out carries ESC, BEL, C1 CSI or bidi overrides.
func assertNoEscapes(t *testing.T, what, out string) {
	t.Helper()
	if strings.ContainsAny(out, "\x1b\x07\u009b"+evilBidi) {
		t.Errorf("%s: terminal control characters reached stdout: %q", what, out)
	}
}

func TestTextOutputSanitizesGmail(t *testing.T) {
	isolateConfig(t)
	evil := "Invoice" + evilOSC52 + evilCSI + evilBidi
	msg := map[string]any{
		"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX"}, "snippet": evil,
		"payload": map[string]any{
			"mimeType": "multipart/mixed",
			"headers":  gmailHeaders("From", "Eve"+evilCSI+" <eve@evil.test>", "Subject", evil, "Date", "Wed, 23 Sep 2026 10:30:00 +0000"),
			"parts": []any{
				map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": gmailB64("line 1\r\n" + evil + "\nline 3")}},
				map[string]any{"mimeType": "application/pdf", "filename": "a" + evilBidi + "fdp.exe",
					"body": map[string]any{"attachmentId": "att1", "size": 10}},
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{map[string]any{"id": "m1"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, msg)
	})
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "gmail", "get", "m1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assertNoEscapes(t, "gmail get", out)
	if !strings.Contains(out, "line 1\n") || !strings.Contains(out, "\nline 3") {
		t.Errorf("body lines must be kept:\n%s", out)
	}

	out, _, _ = runCLI(t, p, "gmail", "search", "x")
	assertNoEscapes(t, "gmail search", out)

	// JSON keeps the exact data (encoding/json escapes the controls).
	out, _, _ = runCLI(t, p, "gmail", "get", "m1", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["subject"] != evil {
		t.Errorf("json subject = %q, want the raw value", got["subject"])
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("raw ESC in JSON output")
	}
}

func TestTextOutputSanitizesChatAndDrive(t *testing.T) {
	isolateConfig(t)
	evil := "hi" + evilOSC52 + evilCSI
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		m := chatCLIMsg("spaces/A", "m1", evil+"\nsecond line", testNow.Add(-time.Hour))
		m["attachment"] = []any{map[string]any{"name": "att1", "contentName": "x" + evilBidi + "gpj.exe"}}
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{m}})
	})
	mux.HandleFunc("GET /v1/spaces/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"memberships": []any{
			map[string]any{"member": map[string]any{"name": "users/1", "displayName": "Ana" + evilCSI}},
		}})
	})
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") == "media" {
			_, _ = w.Write([]byte("text" + evilOSC52 + "\nmore"))
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"id": "f1", "name": "n" + evilCSI, "mimeType": "text/plain", "size": "30"})
	})
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"files": []any{
			map[string]any{"id": "f1", "name": "n" + evilCSI, "mimeType": "text/plain"},
		}})
	})
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "messages", "A")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assertNoEscapes(t, "chat messages", out)
	if !strings.Contains(out, "\n  second line") {
		t.Errorf("multi-line chat text must be kept:\n%s", out)
	}

	for _, args := range [][]string{{"drive", "read", "f1"}, {"drive", "get", "f1"}, {"drive", "search"}} {
		out, errOut, code := runCLI(t, p, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut)
		}
		assertNoEscapes(t, strings.Join(args, " "), out)
	}
}
