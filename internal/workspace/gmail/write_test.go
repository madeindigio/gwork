package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/testutil"
)

var writeNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func parseRaw(t *testing.T, raw []byte) (*mail.Message, string) {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse message: %v\n%s", err, raw)
	}
	body, _ := io.ReadAll(m.Body)
	return m, string(body)
}

func decodeRaw(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("raw is not base64url: %v", err)
	}
	return b
}

func TestBuildMessageBasic(t *testing.T) {
	c, err := buildMessage(ComposeInput{
		To:      []string{"Ana Pérez <ana@example.com>", "bob@example.com"},
		Cc:      []string{"carol@example.com", "BOB@example.com"},
		Bcc:     []string{"dan@example.com"},
		Subject: "Reunión mañana",
		Body:    "Hola, qué tal?\nAdiós ñ",
	}, nil, "", writeNow)
	if err != nil {
		t.Fatal(err)
	}
	if c.nTo != 4 || c.threadID != "" {
		t.Fatalf("nTo=%d thread=%q", c.nTo, c.threadID)
	}
	m, body := parseRaw(t, c.raw)
	h := m.Header
	if got := h.Get("Content-Type"); got != "text/plain; charset=UTF-8" {
		t.Errorf("content-type %q", got)
	}
	if h.Get("Content-Transfer-Encoding") != "quoted-printable" || h.Get("MIME-Version") != "1.0" {
		t.Errorf("headers %v", h)
	}
	if _, err := h.Date(); err != nil {
		t.Errorf("date: %v", err)
	}
	subj := h.Get("Subject")
	if strings.ContainsAny(subj, "ñó") || !strings.HasPrefix(subj, "=?utf-8?q?") {
		t.Errorf("subject not encoded: %q", subj)
	}
	if dec, _ := new(mime.WordDecoder).DecodeHeader(subj); dec != "Reunión mañana" {
		t.Errorf("subject decodes to %q", dec)
	}
	to, err := h.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Name != "Ana Pérez" || to[0].Address != "ana@example.com" {
		t.Errorf("to = %v, %v", to, err)
	}
	if strings.ContainsAny(h.Get("To"), "é") {
		t.Errorf("display name not encoded: %q", h.Get("To"))
	}
	cc, _ := h.AddressList("Cc")
	if len(cc) != 1 || cc[0].Address != "carol@example.com" { // bob deduplicated against To
		t.Errorf("cc = %v", cc)
	}
	if h.Get("Bcc") != "<dan@example.com>" && h.Get("Bcc") != "dan@example.com" {
		t.Errorf("bcc = %q", h.Get("Bcc"))
	}
	if !strings.Contains(body, "=C3=B1") || !strings.Contains(body, "\r\n") {
		t.Errorf("body not quoted-printable CRLF: %q", body)
	}
}

func TestBuildMessageValidation(t *testing.T) {
	cases := []struct {
		name string
		in   ComposeInput
		want string
	}{
		{"invalid to", ComposeInput{To: []string{"not an address"}}, "invalid to address"},
		{"invalid cc", ComposeInput{Cc: []string{"a@"}}, "invalid cc address"},
		{"crlf in to", ComposeInput{To: []string{"a@b.com\r\nBcc: evil@x.com"}}, "line breaks"},
		{"lf in bcc", ComposeInput{Bcc: []string{"a@b.com\nX: y"}}, "line breaks"},
		{"crlf in subject", ComposeInput{To: []string{"a@b.com"}, Subject: "hi\r\nBcc: evil@x.com"}, "subject must not contain"},
		{"lf in subject", ComposeInput{To: []string{"a@b.com"}, Subject: "hi\nthere"}, "subject must not contain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildMessage(c.in, nil, "", writeNow)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func addrs(t *testing.T, h mail.Header, k string) []string {
	t.Helper()
	l, err := h.AddressList(k)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range l {
		out = append(out, a.Address)
	}
	return out
}

func TestBuildMessageReply(t *testing.T) {
	orig := func() *replyContext {
		return &replyContext{
			threadID:   "thr1",
			messageID:  "<orig@example.com>",
			references: "<first@example.com>",
			subject:    "Plan",
			replyTo:    parseList("Ana <ana@example.com>"),
			to:         parseList("me@digio.es, bob@example.com"),
			cc:         parseList("Carol <carol@example.com>, ANA@example.com"),
		}
	}
	cases := []struct {
		name    string
		in      ComposeInput
		mut     func(*replyContext)
		subject string
		to, cc  []string
	}{
		{"defaults to reply-to", ComposeInput{ReplyToMessageID: "m1"}, nil, "Re: Plan", []string{"ana@example.com"}, nil},
		{"explicit subject and to", ComposeInput{ReplyToMessageID: "m1", To: []string{"z@example.com"}, Subject: "Other"}, nil, "Other", []string{"z@example.com"}, nil},
		{"reply all excludes self and dups", ComposeInput{ReplyToMessageID: "m1", ReplyAll: true}, nil, "Re: Plan",
			[]string{"ana@example.com"}, []string{"bob@example.com", "carol@example.com"}},
		{"already Re:", ComposeInput{ReplyToMessageID: "m1"}, func(r *replyContext) { r.subject = "RE: Plan" }, "RE: Plan", []string{"ana@example.com"}, nil},
		{"lowercase re:", ComposeInput{ReplyToMessageID: "m1"}, func(r *replyContext) { r.subject = "re: plan" }, "re: plan", []string{"ana@example.com"}, nil},
		{"reply to own message goes to original to", ComposeInput{ReplyToMessageID: "m1"}, func(r *replyContext) { r.replyTo = parseList("me@digio.es") },
			"Re: Plan", []string{"bob@example.com"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := orig()
			if c.mut != nil {
				c.mut(rc)
			}
			self := ""
			if c.in.ReplyAll || strings.Contains(c.name, "own") {
				self = "Me@digio.es"
			}
			built, err := buildMessage(c.in, rc, self, writeNow)
			if err != nil {
				t.Fatal(err)
			}
			if built.threadID != "thr1" {
				t.Errorf("thread = %q", built.threadID)
			}
			m, _ := parseRaw(t, built.raw)
			if got := m.Header.Get("Subject"); got != c.subject {
				t.Errorf("subject = %q, want %q", got, c.subject)
			}
			if got := m.Header.Get("In-Reply-To"); got != "<orig@example.com>" {
				t.Errorf("in-reply-to = %q", got)
			}
			if got := m.Header.Get("References"); got != "<first@example.com> <orig@example.com>" {
				t.Errorf("references = %q", got)
			}
			if got := addrs(t, m.Header, "To"); !reflect.DeepEqual(got, c.to) {
				t.Errorf("to = %v, want %v", got, c.to)
			}
			if got := addrs(t, m.Header, "Cc"); !reflect.DeepEqual(got, c.cc) {
				t.Errorf("cc = %v, want %v", got, c.cc)
			}
		})
	}
}

func TestBuildMessageReplyEncodedSubject(t *testing.T) {
	rc := &replyContext{threadID: "t", subject: decodeHeader("=?UTF-8?B?UmVzdW1lbiBtYcOxYW5h?="), replyTo: parseList("a@b.com")}
	c, err := buildMessage(ComposeInput{ReplyToMessageID: "m"}, rc, "", writeNow)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := parseRaw(t, c.raw)
	if dec, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject")); dec != "Re: Resumen mañana" {
		t.Errorf("subject = %q", dec)
	}
	if m.Header.Get("In-Reply-To") != "" { // original without Message-ID
		t.Errorf("unexpected In-Reply-To")
	}
}

func TestComposeReplyAllRequiresReply(t *testing.T) {
	_, err := compose(context.Background(), nil, ComposeInput{ReplyAll: true, To: []string{"a@b.com"}}, writeNow)
	if err == nil || !strings.Contains(err.Error(), "reply_all") {
		t.Fatalf("err = %v", err)
	}
}

// replyMux serves the original message and the profile.
func replyMux(t *testing.T, mux *http.ServeMux) {
	mux.HandleFunc("GET /gmail/v1/users/me/messages/orig", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") != "metadata" {
			t.Errorf("format = %q", q.Get("format"))
		}
		testutil.WriteJSON(t, w, map[string]any{
			"id": "orig", "threadId": "thr9",
			"payload": map[string]any{"headers": headers(
				"Message-ID", "<orig@example.com>", "Subject", "Plan", "From", "Ana <ana@example.com>",
				"To", "tester@digio.es, bob@example.com", "Cc", "carol@example.com")},
		})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"emailAddress": "Tester@digio.es"})
	})
}

func TestSendMessage(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	replyMux(t, mux)
	mux.HandleFunc("POST /gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		testutil.WriteJSON(t, w, map[string]any{"id": "sent1", "threadId": "thr9", "labelIds": []string{"SENT"}})
	})
	res, err := SendMessage(context.Background(), ComposeInput{ReplyToMessageID: "orig", ReplyAll: true, Body: "ok"}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID != "sent1" || res.ThreadID != "thr9" || !reflect.DeepEqual(res.LabelIDs, []string{"SENT"}) {
		t.Errorf("result = %+v", res)
	}
	if got["threadId"] != "thr9" {
		t.Errorf("threadId = %v", got["threadId"])
	}
	m, body := parseRaw(t, decodeRaw(t, got["raw"].(string)))
	if s := m.Header.Get("Subject"); s != "Re: Plan" {
		t.Errorf("subject %q", s)
	}
	if to := addrs(t, m.Header, "To"); !reflect.DeepEqual(to, []string{"ana@example.com"}) {
		t.Errorf("to %v", to)
	}
	if cc := addrs(t, m.Header, "Cc"); !reflect.DeepEqual(cc, []string{"bob@example.com", "carol@example.com"}) { // self excluded case-insensitively
		t.Errorf("cc %v", cc)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Errorf("body %q", body)
	}
}

func TestSendMessageNoRecipients(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected call %s", r.URL) })
	_, err := SendMessage(context.Background(), ComposeInput{Subject: "x"}, testutil.FakeGoogle(t, mux)...)
	if err == nil || !strings.Contains(err.Error(), "no recipients") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateDraft(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gmail/v1/users/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		testutil.WriteJSON(t, w, map[string]any{"id": "d1", "message": map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"DRAFT"}}})
	})
	res, err := CreateDraft(context.Background(), ComposeInput{To: []string{"a@b.com"}, Subject: "Hi", Body: "text"}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if res.DraftID != "d1" || res.MessageID != "m1" || res.ThreadID != "t1" || len(res.LabelIDs) != 1 {
		t.Errorf("result = %+v", res)
	}
	msg := got["message"].(map[string]any)
	m, _ := parseRaw(t, decodeRaw(t, msg["raw"].(string)))
	if m.Header.Get("Subject") != "Hi" {
		t.Errorf("subject %q", m.Header.Get("Subject"))
	}
	if _, ok := msg["threadId"]; ok {
		t.Errorf("threadId must be absent on new drafts")
	}
}

func TestSendDraft(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gmail/v1/users/me/drafts/send", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		testutil.WriteJSON(t, w, map[string]any{"id": "m2", "threadId": "t2", "labelIds": []string{"SENT"}})
	})
	res, err := SendDraft(context.Background(), "d1", testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] != "d1" || res.MessageID != "m2" || res.ThreadID != "t2" {
		t.Errorf("got %v res %+v", got, res)
	}
	if _, err := SendDraft(context.Background(), " "); err == nil {
		t.Error("empty draft id accepted")
	}
}

func labelsMux(t *testing.T, mux *http.ServeMux, calls *int) {
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		*calls++
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{
			map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"},
			map[string]any{"id": "Label_7", "name": "Customers/Digio", "type": "user"},
		}})
	})
}

func TestModifyLabels(t *testing.T) {
	var msgReq, thrReq map[string]any
	labelCalls := 0
	mux := http.NewServeMux()
	labelsMux(t, mux, &labelCalls)
	mux.HandleFunc("POST /gmail/v1/users/me/messages/m1/modify", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&msgReq)
		testutil.WriteJSON(t, w, map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"Label_7"}})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/threads/t1/modify", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&thrReq)
		testutil.WriteJSON(t, w, map[string]any{"id": "t1"})
	})
	opts := testutil.FakeGoogle(t, mux)
	ctx := context.Background()

	res, err := ModifyLabels(ctx, Target{MessageID: "m1"}, []string{"customers/digio", "STARRED"}, []string{"INBOX", "Label_7x"[:7]}, opts...)
	if err == nil {
		t.Fatalf("label added and removed must fail, got %+v", res)
	}

	res, err = ModifyLabels(ctx, Target{MessageID: "m1"}, []string{"customers/digio", "STARRED"}, []string{"INBOX", "UNREAD"}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(msgReq["addLabelIds"], []any{"Label_7", "STARRED"}) || !reflect.DeepEqual(msgReq["removeLabelIds"], []any{"INBOX", "UNREAD"}) {
		t.Errorf("request = %v", msgReq)
	}
	if res.Kind != "message" || res.ID != "m1" || res.ThreadID != "t1" || !reflect.DeepEqual(res.LabelIDs, []string{"Label_7"}) {
		t.Errorf("result = %+v", res)
	}
	if labelCalls != 2 { // one lookup per call, shared by add and remove
		t.Errorf("labels.list calls = %d", labelCalls)
	}

	before := labelCalls
	res, err = ModifyLabels(ctx, Target{ThreadID: "t1"}, nil, []string{"INBOX"}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if labelCalls != before {
		t.Errorf("system label triggered a lookup")
	}
	if !reflect.DeepEqual(thrReq["removeLabelIds"], []any{"INBOX"}) || thrReq["addLabelIds"] != nil {
		t.Errorf("thread request = %v", thrReq)
	}
	if res.Kind != "thread" || res.ID != "t1" {
		t.Errorf("result = %+v", res)
	}
}

func TestModifyLabelsErrors(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	labelsMux(t, mux, &calls)
	opts := testutil.FakeGoogle(t, mux)
	ctx := context.Background()
	cases := []struct {
		name string
		t    Target
		add  []string
		want string
	}{
		{"unknown", Target{MessageID: "m"}, []string{"Nope"}, `unknown label "Nope"`},
		{"no target", Target{}, []string{"INBOX"}, "exactly one"},
		{"both targets", Target{MessageID: "m", ThreadID: "t"}, []string{"INBOX"}, "exactly one"},
		{"nothing", Target{MessageID: "m"}, nil, "no labels"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ModifyLabels(ctx, c.t, c.add, nil, opts...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestTrashUntrash(t *testing.T) {
	var paths []string
	mux := http.NewServeMux()
	handle := func(pattern string, resp map[string]any) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.Method+" "+r.URL.Path)
			testutil.WriteJSON(t, w, resp)
		})
	}
	handle("POST /gmail/v1/users/me/messages/m1/trash", map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"TRASH"}})
	handle("POST /gmail/v1/users/me/messages/m1/untrash", map[string]any{"id": "m1", "threadId": "t1", "labelIds": []string{"INBOX"}})
	handle("POST /gmail/v1/users/me/threads/t1/trash", map[string]any{"id": "t1"})
	handle("POST /gmail/v1/users/me/threads/t1/untrash", map[string]any{"id": "t1"})
	opts := testutil.FakeGoogle(t, mux)
	ctx := context.Background()

	r, err := Trash(ctx, Target{MessageID: "m1"}, opts...)
	if err != nil || r.Kind != "message" || r.ThreadID != "t1" || !reflect.DeepEqual(r.LabelIDs, []string{"TRASH"}) {
		t.Fatalf("trash message: %+v %v", r, err)
	}
	r, err = Untrash(ctx, Target{MessageID: "m1"}, opts...)
	if err != nil || !reflect.DeepEqual(r.LabelIDs, []string{"INBOX"}) {
		t.Fatalf("untrash message: %+v %v", r, err)
	}
	if r, err = Trash(ctx, Target{ThreadID: "t1"}, opts...); err != nil || r.Kind != "thread" || r.ID != "t1" {
		t.Fatalf("trash thread: %+v %v", r, err)
	}
	if _, err = Untrash(ctx, Target{ThreadID: "t1"}, opts...); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Errorf("calls = %v", paths)
	}
	if _, err := Trash(ctx, Target{}, opts...); err == nil {
		t.Error("empty target accepted")
	}
}

func TestWriteErrorsKeepContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gmail/v1/users/me/messages/m1/trash", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
	})
	_, err := Trash(context.Background(), Target{MessageID: "m1"}, testutil.FakeGoogle(t, mux)...)
	if err == nil || !strings.Contains(err.Error(), "trash message m1") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveLabelAmbiguity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{
			map[string]any{"id": "Label_1", "name": "Work"},
			map[string]any{"id": "Label_2", "name": "work"},
			map[string]any{"id": "Label_3", "name": "Home"},
			map[string]any{"id": "Label_4", "name": "HOME"},
			map[string]any{"id": "Label_5", "name": "Solo"},
		}})
	})
	mux.HandleFunc("POST /gmail/v1/users/me/messages/m1/modify", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "m1"})
	})
	opts := testutil.FakeGoogle(t, mux)
	ctx := context.Background()
	for _, c := range []struct{ ref, want string }{{"work", "Label_2"}, {"Work", "Label_1"}, {"SOLO", "Label_5"}, {"Label_3", "Label_3"}} {
		res, err := ModifyLabels(ctx, Target{MessageID: "m1"}, []string{c.ref}, nil, opts...)
		if err != nil {
			t.Fatalf("%s: %v", c.ref, err)
		}
		if !reflect.DeepEqual(res.Added, []string{c.want}) {
			t.Errorf("%s resolved to %v, want %s", c.ref, res.Added, c.want)
		}
	}
	_, err := ModifyLabels(ctx, Target{MessageID: "m1"}, []string{"hOmE"}, nil, opts...)
	if err == nil || !strings.Contains(err.Error(), "ambiguous label") || !strings.Contains(err.Error(), "Label_3, Label_4") {
		t.Fatalf("err = %v", err)
	}
}

func TestModifyResultJSONEmptySlices(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gmail/v1/users/me/threads/t1/trash", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "t1"})
	})
	res, err := Trash(context.Background(), Target{ThreadID: "t1"}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	for _, k := range []string{`"label_ids":[]`, `"added_label_ids":[]`, `"removed_label_ids":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("%s lacks %s", b, k)
		}
	}
}

// TestReplyRecipientsUseProfile goes through the public path: the account
// address comes from users.getProfile, also for a plain reply.
func TestReplyRecipientsUseProfile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"emailAddress": "Me@digio.es"})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/theirs", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "theirs", "threadId": "t1", "payload": map[string]any{"headers": headers(
			"Subject", "Plan", "From", "Ana <ana@example.com>", "To", "me@digio.es, bob@example.com")}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/mine", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "mine", "threadId": "t2", "payload": map[string]any{"headers": headers(
			"Subject", "Plan", "From", "me@digio.es", "To", "bob@example.com")}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/note", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "note", "threadId": "t3", "payload": map[string]any{"headers": headers(
			"Subject", "Plan", "From", "Me <me@digio.es>", "To", "<me@digio.es>")}})
	})
	opts := testutil.FakeGoogle(t, mux)
	cases := []struct {
		name string
		in   ComposeInput
		to   []string
		cc   []string
	}{
		{"reply to someone else", ComposeInput{ReplyToMessageID: "theirs"}, []string{"Ana <ana@example.com>"}, []string{}},
		{"reply to own message", ComposeInput{ReplyToMessageID: "mine"}, []string{"bob@example.com"}, []string{}},
		{"reply all to someone else", ComposeInput{ReplyToMessageID: "theirs", ReplyAll: true}, []string{"Ana <ana@example.com>"}, []string{"bob@example.com"}},
		{"reply to a note to self goes to self", ComposeInput{ReplyToMessageID: "note"}, []string{"Me <me@digio.es>"}, []string{}},
		{"reply all to a note to self", ComposeInput{ReplyToMessageID: "note", ReplyAll: true}, []string{"Me <me@digio.es>"}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := ResolveRecipients(context.Background(), c.in, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.To, c.to) || !reflect.DeepEqual(r.Cc, c.cc) || r.Subject != "Re: Plan" {
				t.Errorf("recipients = %+v", r)
			}
		})
	}
}
