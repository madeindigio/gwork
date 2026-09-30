package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/testutil"
)

// gmailWriteServer records every request to a fake Gmail and answers the
// write endpoints.
type gmailWriteServer struct {
	mu    sync.Mutex
	calls []string
	body  map[string]map[string]any // "METHOD path" -> decoded JSON body
}

func newGmailWriteMux(t *testing.T) (*http.ServeMux, *gmailWriteServer) {
	t.Helper()
	rec := &gmailWriteServer{body: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		rec.mu.Lock()
		rec.calls = append(rec.calls, key)
		rec.body[key] = b
		rec.mu.Unlock()
		switch key {
		case "POST /gmail/v1/users/me/messages/send":
			testutil.WriteJSON(t, w, map[string]any{"id": "sent1", "threadId": "t1", "labelIds": []string{"SENT"}})
		case "POST /gmail/v1/users/me/drafts":
			testutil.WriteJSON(t, w, map[string]any{"id": "d1", "message": map[string]any{"id": "m1", "threadId": "t1"}})
		case "POST /gmail/v1/users/me/drafts/send":
			testutil.WriteJSON(t, w, map[string]any{"id": "sent2", "threadId": "t2"})
		case "POST /gmail/v1/users/me/messages/m1/modify":
			testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"STARRED"}})
		case "POST /gmail/v1/users/me/messages/m1/trash":
			testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"TRASH"}})
		case "POST /gmail/v1/users/me/threads/t1/trash", "POST /gmail/v1/users/me/threads/t1/untrash", "POST /gmail/v1/users/me/threads/t1/modify":
			testutil.WriteJSON(t, w, map[string]any{"id": "t1"})
		case "GET /gmail/v1/users/me/drafts/d1":
			testutil.WriteJSON(t, w, map[string]any{"id": "d1", "message": map[string]any{"id": "m1", "payload": map[string]any{"headers": []any{
				map[string]any{"name": "To", "value": "Zed <zed@example.com>"},
				map[string]any{"name": "Subject", "value": "Quarterly plan"},
			}}}})
		case "GET /gmail/v1/users/me/profile":
			testutil.WriteJSON(t, w, map[string]any{"emailAddress": "me@digio.es"})
		case "GET /gmail/v1/users/me/messages/orig":
			testutil.WriteJSON(t, w, map[string]any{"id": "orig", "threadId": "t1", "payload": map[string]any{"headers": []any{
				map[string]any{"name": "From", "value": "Ana <ana@example.com>"},
				map[string]any{"name": "To", "value": "me@digio.es, bob@example.com"},
				map[string]any{"name": "Subject", "value": "Plan"},
			}}})
		case "GET /gmail/v1/users/me/labels":
			testutil.WriteJSON(t, w, map[string]any{"labels": []any{map[string]any{"id": "Label_7", "name": "Customers", "type": "user"}}})
		default:
			testutil.WriteGoogleError(w, 404, "notFound", "unexpected "+key)
		}
	})
	return mux, rec
}

func (s *gmailWriteServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func rawOf(t *testing.T, b map[string]any) *mail.Message {
	t.Helper()
	if m, ok := b["message"].(map[string]any); ok {
		b = m
	}
	raw, err := base64.RawURLEncoding.DecodeString(b["raw"].(string))
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGmailWriteDryRunNoNetwork(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	p := testutil.NewFakeProvider(t, mux)
	cmds := [][]string{
		{"gmail", "draft", "create", "--to", "a@b.com", "--subject", "s", "--body", "b", "--dry-run"},
		{"gmail", "draft", "send", "d1", "--dry-run"},
		{"gmail", "send", "--to", "a@b.com", "--body", "b", "--dry-run"},
		{"gmail", "label", "m1", "--add", "Customers", "--dry-run"},
		{"gmail", "archive", "m1", "--dry-run"},
		{"gmail", "mark-read", "m1", "--dry-run"},
		{"gmail", "mark-unread", "m1", "--dry-run"},
		{"gmail", "star", "m1", "--dry-run"},
		{"gmail", "unstar", "m1", "--dry-run"},
		{"gmail", "trash", "m1", "--thread", "--dry-run"},
		{"gmail", "untrash", "m1", "--dry-run"},
	}
	for _, args := range cmds {
		stdout, stderr, code := runCLI(t, p, append(args, "--json")...)
		if code != 0 {
			t.Fatalf("%v: code %d stderr %s", args, code, stderr)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(stdout), &v); err != nil || v["action"] == nil {
			t.Fatalf("%v: stdout %q: %v", args, stdout, err)
		}
	}
	if rec.count() != 0 {
		t.Fatalf("dry runs made calls: %v", rec.calls)
	}
}

func TestGmailSendConfirmation(t *testing.T) {
	args := []string{"gmail", "send", "--to", "a@b.com", "--subject", "Hi", "--body", "hello"}
	t.Run("non tty without yes", func(t *testing.T) {
		mux, rec := newGmailWriteMux(t)
		_, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), args...)
		if code == 0 || !strings.Contains(stderr, "--yes") || rec.count() != 0 {
			t.Fatalf("code %d stderr %q calls %v", code, stderr, rec.calls)
		}
	})
	for _, c := range []struct {
		in   string
		ok   bool
		want int
	}{{"y\n", true, 1}, {"n\n", false, 0}} {
		mux, rec := newGmailWriteMux(t)
		app, stdout, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
		app.In = strings.NewReader(c.in)
		app.IsTerminal = func() bool { return true }
		code := app.Run(context.Background(), args)
		if (code == 0) != c.ok || rec.count() != c.want {
			t.Fatalf("input %q: code %d stderr %q calls %v", c.in, code, stderr.String(), rec.calls)
		}
		if !strings.Contains(stderr.String(), "a@b.com") {
			t.Errorf("prompt lacks recipient: %q", stderr.String())
		}
		if c.ok && !strings.Contains(stdout.String(), "sent1") {
			t.Errorf("stdout %q", stdout.String())
		}
	}
}

func TestGmailSendYesJSON(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(bodyPath, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux),
		"gmail", "send", "--to", "a@b.com,c@d.com", "--cc", "e@f.com", "--subject", "Hi", "--body-file", bodyPath, "--yes", "--json")
	if code != 0 {
		t.Fatalf("code %d %s", code, stderr)
	}
	var res struct {
		MessageID string `json:"message_id"`
		ThreadID  string `json:"thread_id"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res.MessageID != "sent1" || res.ThreadID != "t1" {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	m := rawOf(t, rec.body["POST /gmail/v1/users/me/messages/send"])
	if m.Header.Get("Cc") == "" || m.Header.Get("Subject") != "Hi" {
		t.Errorf("headers %v", m.Header)
	}
	if l, _ := m.Header.AddressList("To"); len(l) != 2 {
		t.Errorf("to %v", l)
	}
}

func TestGmailSendValidation(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	p := testutil.NewFakeProvider(t, mux)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"gmail", "send", "--body", "x", "--yes"}, "no recipients"},
		{[]string{"gmail", "send", "--to", "a@b.com", "--body", "x", "--body-file", "f", "--yes"}, "mutually exclusive"},
		{[]string{"gmail", "send", "--to", "a@b.com", "--reply-all", "--yes"}, "--reply-all requires --reply-to"},
		{[]string{"gmail", "send", "--to", "a@b.com", "--body-file", "-"}, "--yes"},
		{[]string{"gmail", "send", "--to", "bad address", "--yes"}, "invalid to address"},
		{[]string{"gmail", "send", "--to", "a@b.com", "--subject", "x\nBcc: y@z.com", "--yes"}, "line breaks"},
		{[]string{"gmail", "label", "m1"}, "nothing to do"},
	}
	for _, c := range cases {
		_, stderr, code := runCLI(t, p, c.args...)
		if code == 0 || !strings.Contains(stderr, c.want) {
			t.Errorf("%v: code %d stderr %q, want %q", c.args, code, stderr, c.want)
		}
	}
	if rec.count() != 0 {
		t.Errorf("calls: %v", rec.calls)
	}
}

func TestGmailBodyFromStdin(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	app, _, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
	app.In = strings.NewReader("stdin body")
	code := app.Run(context.Background(), []string{"gmail", "draft", "create", "--to", "a@b.com", "--body-file", "-"})
	if code != 0 {
		t.Fatalf("code %d %s", code, stderr)
	}
	m := rawOf(t, rec.body["POST /gmail/v1/users/me/drafts"])
	b := make([]byte, 64)
	n, _ := m.Body.Read(b)
	if !strings.Contains(string(b[:n]), "stdin body") {
		t.Errorf("body %q", b[:n])
	}
}

func TestGmailDraftAndDraftSend(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	p := testutil.NewFakeProvider(t, mux)
	out, stderr, code := runCLI(t, p, "gmail", "draft", "create", "--to", "a@b.com", "--subject", "Hi", "--body", "x")
	if code != 0 || !strings.Contains(out, "d1") { // no confirmation needed
		t.Fatalf("code %d out %q err %q", code, out, stderr)
	}
	_, stderr, code = runCLI(t, p, "gmail", "draft", "send", "d1")
	if code == 0 || !strings.Contains(stderr, "--yes") {
		t.Fatalf("draft send must need confirmation: %d %q", code, stderr)
	}
	out, _, code = runCLI(t, p, "gmail", "draft", "send", "d1", "--yes")
	if code != 0 || !strings.Contains(out, "sent2") {
		t.Fatalf("code %d out %q", code, out)
	}
	if rec.body["POST /gmail/v1/users/me/drafts/send"]["id"] != "d1" {
		t.Errorf("body %v", rec.body)
	}
}

func TestGmailSendPromptShowsRecipients(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"reply all", []string{"gmail", "send", "--reply-to", "orig", "--reply-all", "--body", "x"},
			[]string{"To: Ana <ana@example.com>", "Cc: bob@example.com", `"Re: Plan"`}},
		{"draft", []string{"gmail", "draft", "send", "d1"}, []string{"zed@example.com", "Quarterly plan"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux, rec := newGmailWriteMux(t)
			app, _, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
			app.In, app.IsTerminal = strings.NewReader("n\n"), func() bool { return true }
			if code := app.Run(context.Background(), c.args); code == 0 {
				t.Fatal("declined send succeeded")
			}
			for _, w := range c.want {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("prompt %q lacks %q", stderr.String(), w)
				}
			}
			for _, k := range rec.calls {
				if strings.HasPrefix(k, "POST") {
					t.Errorf("unexpected write %s", k)
				}
			}
		})
	}
}

func TestGmailChangeCommands(t *testing.T) {
	cases := []struct {
		args     []string
		wantCall string
		wantBody map[string][]any
	}{
		{[]string{"gmail", "archive", "m1"}, "POST /gmail/v1/users/me/messages/m1/modify", map[string][]any{"removeLabelIds": {"INBOX"}}},
		{[]string{"gmail", "mark-read", "m1"}, "POST /gmail/v1/users/me/messages/m1/modify", map[string][]any{"removeLabelIds": {"UNREAD"}}},
		{[]string{"gmail", "mark-unread", "m1"}, "POST /gmail/v1/users/me/messages/m1/modify", map[string][]any{"addLabelIds": {"UNREAD"}}},
		{[]string{"gmail", "star", "m1"}, "POST /gmail/v1/users/me/messages/m1/modify", map[string][]any{"addLabelIds": {"STARRED"}}},
		{[]string{"gmail", "unstar", "m1"}, "POST /gmail/v1/users/me/messages/m1/modify", map[string][]any{"removeLabelIds": {"STARRED"}}},
		{[]string{"gmail", "label", "t1", "--thread", "--add", "customers", "--remove", "INBOX"}, "POST /gmail/v1/users/me/threads/t1/modify",
			map[string][]any{"addLabelIds": {"Label_7"}, "removeLabelIds": {"INBOX"}}},
		{[]string{"gmail", "untrash", "t1", "--thread"}, "POST /gmail/v1/users/me/threads/t1/untrash", nil},
		{[]string{"gmail", "trash", "m1", "--yes"}, "POST /gmail/v1/users/me/messages/m1/trash", nil},
	}
	for _, c := range cases {
		mux, rec := newGmailWriteMux(t)
		out, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), c.args...)
		if code != 0 {
			t.Fatalf("%v: code %d %s", c.args, code, stderr)
		}
		if out == "" {
			t.Errorf("%v: no output", c.args)
		}
		body, ok := rec.body[c.wantCall]
		if !ok {
			t.Fatalf("%v: calls %v", c.args, rec.calls)
		}
		for k, want := range c.wantBody {
			got, _ := body[k].([]any)
			if strings.Join(anyStrings(got), ",") != strings.Join(anyStrings(want), ",") {
				t.Errorf("%v: %s = %v, want %v", c.args, k, got, want)
			}
		}
	}
}

func anyStrings(l []any) []string {
	var out []string
	for _, v := range l {
		out = append(out, v.(string))
	}
	return out
}

func TestGmailTrashConfirmation(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	_, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), "gmail", "trash", "m1")
	if code == 0 || !strings.Contains(stderr, "--yes") || rec.count() != 0 {
		t.Fatalf("code %d stderr %q calls %v", code, stderr, rec.calls)
	}
	app, _, _ := newTestApp(t, testutil.NewFakeProvider(t, mux))
	app.In, app.IsTerminal = strings.NewReader("n\n"), func() bool { return true }
	if code := app.Run(context.Background(), []string{"gmail", "trash", "m1"}); code == 0 || rec.count() != 0 {
		t.Fatalf("declined trash ran: %v", rec.calls)
	}
	app, _, _ = newTestApp(t, testutil.NewFakeProvider(t, mux))
	app.In, app.IsTerminal = strings.NewReader("y\n"), func() bool { return true }
	if code := app.Run(context.Background(), []string{"gmail", "trash", "m1"}); code != 0 || rec.count() != 1 {
		t.Fatalf("confirmed trash: code %d calls %v", code, rec.calls)
	}
}

func TestGmailWriteMissingScope(t *testing.T) {
	mux, rec := newGmailWriteMux(t)
	p := testutil.NewFakeProvider(t, mux).GrantWrite()
	_, stderr, code := runCLI(t, p, "gmail", "archive", "m1")
	if code == 0 || !strings.Contains(stderr, "--write gmail") || rec.count() != 0 {
		t.Fatalf("code %d stderr %q calls %v", code, stderr, rec.calls)
	}
}

func TestGmailWriteAPIErrorHint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
	})
	_, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), "gmail", "archive", "nope")
	if code == 0 || !strings.Contains(stderr, "not found") || !strings.Contains(stderr, "nope") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}
