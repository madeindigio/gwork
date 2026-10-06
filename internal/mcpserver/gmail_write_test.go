package mcpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

type gmailRecorder struct {
	mu    sync.Mutex
	calls []string
	body  map[string]map[string]any
}

func gmailWriteMux(t *testing.T) (*http.ServeMux, *gmailRecorder) {
	t.Helper()
	rec := &gmailRecorder{body: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		b := decodeGmailRequest(t, r)
		rec.mu.Lock()
		rec.calls = append(rec.calls, key)
		rec.body[key] = b
		rec.mu.Unlock()
		switch key {
		case "POST /upload/gmail/v1/users/me/drafts":
			testutil.WriteJSON(t, w, map[string]any{"id": "d1", "message": map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"DRAFT"}}})
		case "POST /gmail/v1/users/me/drafts/send":
			testutil.WriteJSON(t, w, map[string]any{"id": "sent2", "threadId": "t2", "labelIds": []string{"SENT"}})
		case "POST /upload/gmail/v1/users/me/messages/send":
			testutil.WriteJSON(t, w, map[string]any{"id": "sent1", "threadId": "t1", "labelIds": []string{"SENT"}})
		case "POST /gmail/v1/users/me/messages/m1/modify":
			testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"STARRED"}})
		case "POST /gmail/v1/users/me/threads/t1/modify", "POST /gmail/v1/users/me/threads/t1/trash", "POST /gmail/v1/users/me/threads/t1/untrash":
			testutil.WriteJSON(t, w, map[string]any{"id": "t1"})
		case "POST /gmail/v1/users/me/messages/m1/trash":
			testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"TRASH"}})
		case "POST /gmail/v1/users/me/messages/m1/untrash":
			testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX"}})
		case "GET /gmail/v1/users/me/messages/m9/attachments/A1":
			testutil.WriteJSON(t, w, map[string]any{"data": base64.URLEncoding.EncodeToString([]byte("ORIGINAL-PDF")), "size": 12})
		case "GET /gmail/v1/users/me/messages/m9":
			testutil.WriteJSON(t, w, map[string]any{"id": "m9", "payload": map[string]any{"mimeType": "multipart/mixed", "parts": []any{
				map[string]any{"mimeType": "application/pdf", "filename": "factura.pdf", "body": map[string]any{"attachmentId": "A1", "size": 12}},
			}}})
		case "GET /gmail/v1/users/me/labels":
			testutil.WriteJSON(t, w, map[string]any{"labels": []any{map[string]any{"id": "Label_7", "name": "Customers", "type": "user"}}})
		default:
			testutil.WriteGoogleError(w, 404, "notFound", "unexpected "+key)
		}
	})
	return mux, rec
}

// decodeGmailRequest decodes a JSON request body. A media upload
// (multipart/related: JSON metadata, then the message/rfc822 media) is
// returned as its metadata with the message added as base64url "raw", in
// "message" when the metadata has one (drafts), like a non-upload request.
func decodeGmailRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var b map[string]any
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/related" {
		_ = json.NewDecoder(r.Body).Decode(&b)
		return b
	}
	if r.URL.Query().Get("uploadType") != "multipart" {
		t.Errorf("uploadType = %q", r.URL.Query().Get("uploadType"))
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	meta, err := mr.NextPart()
	if err != nil {
		t.Errorf("upload metadata: %v", err)
		return nil
	}
	if err := json.NewDecoder(meta).Decode(&b); err != nil {
		t.Errorf("upload metadata: %v", err)
		return nil
	}
	media, err := mr.NextPart()
	if err != nil {
		t.Errorf("upload media: %v", err)
		return nil
	}
	if ct := media.Header.Get("Content-Type"); ct != "message/rfc822" {
		t.Errorf("media content type %q", ct)
	}
	raw, err := io.ReadAll(media)
	if err != nil {
		t.Errorf("upload media: %v", err)
		return nil
	}
	dst := b
	if m, ok := b["message"].(map[string]any); ok {
		dst = m
	}
	dst["raw"] = base64.RawURLEncoding.EncodeToString(raw)
	return b
}

func gmailWriteSession(t *testing.T, allowSend bool) (cs *mcp.ClientSession, rec *gmailRecorder) {
	t.Helper()
	mux, rec := gmailWriteMux(t)
	deps := testDeps(t, mux)
	deps.Write = WriteOptions{Services: []auth.Service{auth.Gmail}, AllowSend: allowSend}
	c, _ := newTestSession(t, deps, auth.Gmail)
	return c, rec
}

func TestGmailWriteToolRegistration(t *testing.T) {
	all := []string{"gmail_create_draft", "gmail_modify_labels", "gmail_trash", "gmail_untrash", "gmail_send_draft", "gmail_send_message"}
	sendTools := []string{"gmail_send_draft", "gmail_send_message"}

	cs, _ := gmailWriteSession(t, false)
	tools := listTools(t, cs)
	for _, n := range all {
		if _, ok := tools[n]; ok != !slices.Contains(sendTools, n) {
			t.Errorf("without allow-send: tool %s present = %v", n, ok)
		}
	}

	cs, _ = gmailWriteSession(t, true)
	tools = listTools(t, cs)
	for _, n := range all {
		if tools[n] == nil {
			t.Fatalf("with allow-send: %s missing", n)
		}
		if tools[n].Annotations.ReadOnlyHint {
			t.Errorf("%s is read-only", n)
		}
	}

	// No --allow-write gmail: no write tool at all, even with allow-send.
	mux, _ := gmailWriteMux(t)
	deps := testDeps(t, mux)
	deps.Write = WriteOptions{AllowSend: true}
	cs2, _ := newTestSession(t, deps, auth.Gmail)
	for n := range listTools(t, cs2) {
		if slices.Contains(all, n) {
			t.Errorf("tool %s registered without allow-write", n)
		}
	}
}

func TestGmailWriteToolAnnotations(t *testing.T) {
	cs, _ := gmailWriteSession(t, true)
	tools := listTools(t, cs)
	cases := []struct {
		name                             string
		destructive, idempotent, openWld bool
	}{
		{"gmail_create_draft", false, false, false},
		{"gmail_modify_labels", false, true, false},
		{"gmail_trash", true, true, false},
		{"gmail_untrash", false, true, false},
		{"gmail_send_draft", true, false, true},
		{"gmail_send_message", true, false, true},
	}
	for _, c := range cases {
		a := tools[c.name].Annotations
		if a.DestructiveHint == nil || *a.DestructiveHint != c.destructive || a.IdempotentHint != c.idempotent ||
			a.OpenWorldHint == nil || *a.OpenWorldHint != c.openWld {
			t.Errorf("%s annotations = %+v", c.name, a)
		}
	}
	for _, n := range []string{"gmail_send_draft", "gmail_send_message", "gmail_trash"} {
		if !strings.Contains(strings.ToLower(tools[n].Description), "confirmation") {
			t.Errorf("%s description does not ask for confirmation", n)
		}
	}
	if !strings.Contains(tools["gmail_send_message"].Description, "gmail_create_draft") {
		t.Errorf("send_message should point to drafts")
	}
}

func decodeMsg(t *testing.T, b map[string]any) *mail.Message {
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

func TestGmailCreateDraftTool(t *testing.T) {
	cs, rec := gmailWriteSession(t, false)
	out, res := callTool[gmail.DraftResult](t, cs, "gmail_create_draft", map[string]any{
		"to": []string{"a@b.com"}, "subject": "Hola", "body": "text",
	})
	if res.IsError || out.DraftID != "d1" || out.ThreadID != "t1" {
		t.Fatalf("res %s out %+v", resultText(res), out)
	}
	m := decodeMsg(t, rec.body["POST /upload/gmail/v1/users/me/drafts"])
	if m.Header.Get("Subject") != "Hola" || m.Header.Get("To") == "" {
		t.Errorf("headers %v", m.Header)
	}
	// Header injection is rejected as a tool error, without an API call.
	before := len(rec.calls)
	_, res = callTool[gmail.DraftResult](t, cs, "gmail_create_draft", map[string]any{"to": []string{"a@b.com"}, "subject": "x\r\nBcc: e@e.com"})
	if !res.IsError || len(rec.calls) != before {
		t.Errorf("injection accepted: %v %v", res.IsError, rec.calls)
	}
}

func TestGmailSendTools(t *testing.T) {
	cs, rec := gmailWriteSession(t, true)
	out, res := callTool[gmail.SendResult](t, cs, "gmail_send_message", map[string]any{"to": []string{"a@b.com"}, "subject": "Hi", "body": "x"})
	if res.IsError || out.MessageID != "sent1" || out.ThreadID != "t1" {
		t.Fatalf("send_message: %s %+v", resultText(res), out)
	}
	if rec.body["POST /upload/gmail/v1/users/me/messages/send"]["raw"] == nil {
		t.Errorf("no raw sent")
	}
	out, res = callTool[gmail.SendResult](t, cs, "gmail_send_draft", map[string]any{"draft_id": "d1"})
	if res.IsError || out.MessageID != "sent2" || rec.body["POST /gmail/v1/users/me/drafts/send"]["id"] != "d1" {
		t.Fatalf("send_draft: %s %+v", resultText(res), out)
	}
	_, res = callTool[gmail.SendResult](t, cs, "gmail_send_message", map[string]any{"subject": "no recipients"})
	if !res.IsError || !strings.Contains(resultText(res), "no recipients") {
		t.Errorf("missing recipients: %s", resultText(res))
	}
}

func TestGmailModifyLabelsTool(t *testing.T) {
	cs, rec := gmailWriteSession(t, false)
	out, res := callTool[gmail.ModifyResult](t, cs, "gmail_modify_labels", map[string]any{
		"message_id": "m1", "add_labels": []string{"customers", "STARRED"}, "remove_labels": []string{"INBOX"},
	})
	if res.IsError || out.Kind != "message" || out.ID != "m1" || len(out.LabelIDs) != 1 {
		t.Fatalf("%s %+v", resultText(res), out)
	}
	b := rec.body["POST /gmail/v1/users/me/messages/m1/modify"]
	if got := b["addLabelIds"].([]any); got[0] != "Label_7" || got[1] != "STARRED" {
		t.Errorf("add = %v", got)
	}
	out, res = callTool[gmail.ModifyResult](t, cs, "gmail_modify_labels", map[string]any{"thread_id": "t1", "remove_labels": []string{"UNREAD"}})
	if res.IsError || out.Kind != "thread" {
		t.Fatalf("%s %+v", resultText(res), out)
	}

	for _, args := range []map[string]any{
		{"add_labels": []string{"STARRED"}},
		{"message_id": "m1", "thread_id": "t1", "add_labels": []string{"STARRED"}},
	} {
		_, res = callTool[gmail.ModifyResult](t, cs, "gmail_modify_labels", args)
		if !res.IsError || !strings.Contains(resultText(res), "exactly one") {
			t.Errorf("%v: %s", args, resultText(res))
		}
	}
	_, res = callTool[gmail.ModifyResult](t, cs, "gmail_modify_labels", map[string]any{"message_id": "m1", "add_labels": []string{"Nope"}})
	if !res.IsError || !strings.Contains(resultText(res), "unknown label") {
		t.Errorf("unknown label: %s", resultText(res))
	}
}

func TestGmailTrashTools(t *testing.T) {
	cs, rec := gmailWriteSession(t, false)
	out, res := callTool[gmail.ModifyResult](t, cs, "gmail_trash", map[string]any{"message_id": "m1"})
	if res.IsError || out.LabelIDs[0] != "TRASH" {
		t.Fatalf("%s %+v", resultText(res), out)
	}
	out, res = callTool[gmail.ModifyResult](t, cs, "gmail_untrash", map[string]any{"message_id": "m1"})
	if res.IsError || out.LabelIDs[0] != "INBOX" {
		t.Fatalf("%s %+v", resultText(res), out)
	}
	if _, res = callTool[gmail.ModifyResult](t, cs, "gmail_trash", map[string]any{"thread_id": "t1"}); res.IsError {
		t.Fatal(resultText(res))
	}
	if _, res = callTool[gmail.ModifyResult](t, cs, "gmail_untrash", map[string]any{"thread_id": "t1"}); res.IsError {
		t.Fatal(resultText(res))
	}
	before := len(rec.calls)
	_, res = callTool[gmail.ModifyResult](t, cs, "gmail_trash", map[string]any{})
	if !res.IsError || len(rec.calls) != before {
		t.Errorf("empty target accepted")
	}
}

// mixedAttachments returns the attachments of a multipart/mixed message:
// filename -> [content type, data].
func mixedAttachments(t *testing.T, m *mail.Message) map[string][2]string {
	t.Helper()
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type %q: %v", m.Header.Get("Content-Type"), err)
	}
	out := map[string][2]string{}
	mr := multipart.NewReader(m.Body, params["boundary"])
	for {
		p, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		_, dp, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		if dp["filename"] == "" {
			continue
		}
		enc, _ := io.ReadAll(p)
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(enc), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		out[dp["filename"]] = [2]string{ct, string(data)}
	}
	return out
}

func gmailAttachSession(t *testing.T) (*mcp.ClientSession, *gmailRecorder, *bytes.Buffer) {
	t.Helper()
	mux, rec := gmailWriteMux(t)
	var logs bytes.Buffer
	deps := testDeps(t, mux)
	deps.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	deps.Write = WriteOptions{Services: []auth.Service{auth.Gmail}, AllowSend: true}
	cs, _ := newTestSession(t, deps, auth.Gmail)
	return cs, rec, &logs
}

func TestGmailComposeAttachmentsTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOCAL-notes.txt")
	if err := os.WriteFile(path, []byte("LOCAL-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	inline := base64.RawURLEncoding.EncodeToString([]byte("INLINE-CONTENT\xff\xfe")) // URL alphabet, unpadded
	for _, c := range []struct{ tool, key string }{
		{"gmail_create_draft", "POST /upload/gmail/v1/users/me/drafts"},
		{"gmail_send_message", "POST /upload/gmail/v1/users/me/messages/send"},
	} {
		t.Run(c.tool, func(t *testing.T) {
			cs, rec, logs := gmailAttachSession(t)
			_, res := callTool[map[string]any](t, cs, c.tool, map[string]any{
				"to": []string{"a@b.com"}, "subject": "Docs", "body": "see attached",
				"attachments": []any{
					map[string]any{"path": path},
					map[string]any{"content_base64": inline, "filename": "datos año.bin", "content_type": "application/x-test"},
					map[string]any{"message_id": "m9", "attachment_id": "A1"},
					map[string]any{"message_id": "m9", "attachment_id": "A1", "filename": "renamed.pdf"},
				},
			})
			if res.IsError {
				t.Fatalf("%s", resultText(res))
			}
			got := mixedAttachments(t, decodeMsg(t, rec.body[c.key]))
			want := map[string][2]string{
				"LOCAL-notes.txt": {"text/plain", "LOCAL-CONTENT"},
				"datos año.bin":   {"application/x-test", "INLINE-CONTENT\xff\xfe"},
				"factura.pdf":     {"application/pdf", "ORIGINAL-PDF"},
				"renamed.pdf":     {"application/pdf", "ORIGINAL-PDF"},
			}
			if len(got) != len(want) {
				t.Fatalf("attachments = %q", got)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
			for _, secret := range []string{"LOCAL-CONTENT", "INLINE-CONTENT", inline, "ORIGINAL-PDF"} {
				if strings.Contains(logs.String(), secret) {
					t.Errorf("log leaked attachment content: %s", logs.String())
				}
			}
		})
	}
}

func TestGmailComposeAttachmentsValidation(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(gmail.MaxAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	cases := []struct {
		att  map[string]any
		want string
	}{
		{map[string]any{"filename": "x.txt"}, "exactly one of"},
		{map[string]any{"path": big, "content_base64": "eA", "filename": "x"}, "got path and content_base64"},
		{map[string]any{"path": big, "message_id": "m9", "attachment_id": "A1"}, "exactly one of"},
		{map[string]any{"content_base64": "SECRETPAYLOAD"}, "requires filename"},
		{map[string]any{"message_id": "m9"}, "set together"},
		{map[string]any{"content_base64": "SECRET!!PAYLOAD", "filename": "x"}, "not valid base64"},
		{map[string]any{"path": filepath.Join(dir, "missing.pdf")}, "no such file"},
		{map[string]any{"path": dir}, "not a regular file"},
		{map[string]any{"path": big}, "over the 25 MB limit"},
		{map[string]any{"content_base64": "eA", "filename": "a\nb"}, "line breaks"},
	}
	cs, rec, logs := gmailAttachSession(t)
	for _, c := range cases {
		_, res := callTool[map[string]any](t, cs, "gmail_send_message", map[string]any{
			"to": []string{"a@b.com"}, "attachments": []any{c.att},
		})
		if !res.IsError || !strings.Contains(resultText(res), c.want) {
			t.Errorf("%v: %s, want %q", c.att, resultText(res), c.want)
		}
	}
	for _, k := range rec.calls {
		if strings.HasPrefix(k, "POST") {
			t.Errorf("unexpected write %s", k)
		}
	}
	if strings.Contains(logs.String(), "SECRET") {
		t.Errorf("log leaked content: %s", logs.String())
	}
}
