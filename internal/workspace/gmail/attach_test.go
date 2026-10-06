package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/testutil"
)

// parseUpload decodes a media upload request (uploadType=multipart): a
// multipart/related body with the JSON metadata and the message/rfc822
// message. It returns the metadata and the raw message. It runs in the
// server goroutine, so it reports failures with t.Errorf.
func parseUpload(t *testing.T, r *http.Request) (map[string]any, []byte) {
	t.Helper()
	if got := r.URL.Query().Get("uploadType"); got != "multipart" {
		t.Errorf("uploadType = %q", got)
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/related" {
		t.Errorf("content type %q: %v", r.Header.Get("Content-Type"), err)
		return nil, nil
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	meta, err := mr.NextPart()
	if err != nil {
		t.Errorf("metadata: %v", err)
		return nil, nil
	}
	var m map[string]any
	if err := json.NewDecoder(meta).Decode(&m); err != nil {
		t.Errorf("metadata: %v", err)
		return nil, nil
	}
	media, err := mr.NextPart()
	if err != nil {
		t.Errorf("media: %v", err)
		return nil, nil
	}
	if ct := media.Header.Get("Content-Type"); ct != "message/rfc822" {
		t.Errorf("media content type %q", ct)
	}
	raw, err := io.ReadAll(media)
	if err != nil {
		t.Errorf("media: %v", err)
		return nil, nil
	}
	return m, raw
}

type mimePart struct {
	header map[string][]string
	data   []byte // decoded
}

// readMixed parses a multipart/mixed message and decodes its parts.
func readMixed(t *testing.T, raw []byte) (*mail.Message, []mimePart) {
	t.Helper()
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type %q: %v", m.Header.Get("Content-Type"), err)
	}
	if m.Header.Get("Content-Transfer-Encoding") != "" {
		t.Errorf("top-level transfer encoding on multipart")
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	var parts []mimePart
	for {
		p, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		if p.Header.Get("Content-Transfer-Encoding") == "base64" {
			for _, l := range strings.Split(string(b), "\r\n") {
				if len(l) > 76 {
					t.Errorf("base64 line of %d chars", len(l))
				}
			}
			if b, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(string(b), "\r\n", "")); err != nil {
				t.Fatal(err)
			}
		}
		parts = append(parts, mimePart{header: p.Header, data: b})
	}
	return m, parts
}

func TestBuildMessageAttachments(t *testing.T) {
	big := bytes.Repeat([]byte{0, 1, 2, 3, 250}, 100)
	c, err := buildMessage(ComposeInput{
		To: []string{"a@b.com"}, Subject: "Docs", Body: "Adjunto ñ",
		Attachments: []OutgoingAttachment{
			{Filename: "/home/me/report.pdf", Data: []byte("%PDF-1.4 fake")},
			{Filename: `C:\Users\me\informe año.txt`, Data: []byte("hola")},
			{Filename: "noext", Data: []byte("<html><body>x</body></html>")},
			{Filename: "data.bin", ContentType: "application/x-custom", Data: big},
		},
	}, nil, "", writeNow)
	if err != nil {
		t.Fatal(err)
	}
	m, parts := readMixed(t, c.raw)
	if m.Header.Get("Subject") != "Docs" || m.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("headers %v", m.Header)
	}
	if len(parts) != 5 {
		t.Fatalf("parts = %d", len(parts))
	}
	body := parts[0]
	if body.header["Content-Type"][0] != "text/plain; charset=UTF-8" || body.header["Content-Transfer-Encoding"][0] != "quoted-printable" {
		t.Errorf("body headers %v", body.header)
	}
	if !strings.Contains(string(body.data), "=C3=B1") {
		t.Errorf("body not quoted-printable: %q", body.data)
	}
	cases := []struct {
		name, ctype string
		data        []byte
	}{
		{"report.pdf", "application/pdf", []byte("%PDF-1.4 fake")},
		{"informe año.txt", "text/plain", []byte("hola")},
		{"noext", "text/html", []byte("<html><body>x</body></html>")},
		{"data.bin", "application/x-custom", big},
	}
	for i, want := range cases {
		p := parts[i+1]
		get := func(k string) string {
			if v := p.header[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		disp, dp, err := mime.ParseMediaType(get("Content-Disposition"))
		if err != nil || disp != "attachment" || dp["filename"] != want.name {
			t.Errorf("part %d disposition %q -> %v %v", i, get("Content-Disposition"), dp, err)
		}
		ct, cp, err := mime.ParseMediaType(get("Content-Type"))
		if err != nil || ct != want.ctype || cp["name"] != want.name {
			t.Errorf("part %d content type %q", i, get("Content-Type"))
		}
		if get("Content-Transfer-Encoding") != "base64" || !bytes.Equal(p.data, want.data) {
			t.Errorf("part %d data/encoding mismatch", i)
		}
	}
	// Non-ASCII filenames use RFC 2231, not raw UTF-8, in the header.
	if d := parts[2].header["Content-Disposition"][0]; !strings.Contains(d, "filename*=utf-8''") || strings.Contains(d, "ñ") {
		t.Errorf("non-ascii filename not RFC 2231 encoded: %q", d)
	}
}

func TestBuildMessageNoAttachmentsUnchanged(t *testing.T) {
	in := ComposeInput{To: []string{"a@b.com"}, Subject: "s", Body: "b"}
	a, err := buildMessage(in, nil, "", writeNow)
	if err != nil {
		t.Fatal(err)
	}
	in.Attachments = []OutgoingAttachment{}
	b, err := buildMessage(in, nil, "", writeNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "MIME-Version: 1.0\r\nDate: Thu, 24 Sep 2026 12:00:00 +0000\r\nTo: <a@b.com>\r\nSubject: s\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nb"
	if string(a.raw) != want || !bytes.Equal(a.raw, b.raw) {
		t.Errorf("raw = %q", a.raw)
	}
}

func TestAttachmentValidation(t *testing.T) {
	cases := []struct {
		name string
		atts []OutgoingAttachment
		want string
	}{
		{"empty name", []OutgoingAttachment{{Filename: ""}}, "invalid attachment filename"},
		{"dir only", []OutgoingAttachment{{Filename: "dir/"}}, ""},
		{"dot dot", []OutgoingAttachment{{Filename: "a/.."}}, "invalid attachment filename"},
		{"crlf", []OutgoingAttachment{{Filename: "a\r\nX-Evil: 1.txt"}}, "line breaks"},
		{"nul", []OutgoingAttachment{{Filename: "a\x00.txt"}}, "line breaks"},
		{"bad type", []OutgoingAttachment{{Filename: "a.txt", ContentType: "not a type;;"}}, "invalid content type"},
		{"crlf type", []OutgoingAttachment{{Filename: "a.txt", ContentType: "text/plain\r\nX: y"}}, "line breaks"},
		{"too big", []OutgoingAttachment{
			{Filename: "a", Data: make([]byte, MaxAttachmentBytes/2)},
			{Filename: "b", Data: make([]byte, MaxAttachmentBytes/2+1)},
		}, "over the 25.0 MB limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateAttachments(c.atts)
			if c.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if _, err := buildMessage(ComposeInput{To: []string{"a@b.com"}, Attachments: c.atts}, nil, "", writeNow); err == nil {
				t.Error("buildMessage accepted invalid attachments")
			}
		})
	}
	if err := ValidateAttachments([]OutgoingAttachment{{Filename: "max", Data: make([]byte, MaxAttachmentBytes)}}); err != nil {
		t.Errorf("exactly the limit rejected: %v", err)
	}
}

func TestAttachmentName(t *testing.T) {
	for in, want := range map[string]string{
		"a.txt": "a.txt", "/x/y/z.pdf": "z.pdf", `C:\x\y.doc`: "y.doc", " spaced name.txt ": "spaced name.txt", "dir/": "dir",
	} {
		if got, err := AttachmentName(in); err != nil || got != want {
			t.Errorf("AttachmentName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestOutgoingAttachmentJSONOmitsData(t *testing.T) {
	b, err := json.Marshal(ComposeInput{Attachments: []OutgoingAttachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("secret")}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), base64.StdEncoding.EncodeToString([]byte("secret"))) ||
		!strings.Contains(string(b), `"attachments":[{"filename":"a.txt","content_type":"text/plain","size":6}]`) {
		t.Errorf("json = %s", b)
	}
}

func TestSendMessageWithAttachmentUsesUpload(t *testing.T) {
	var raw []byte
	var meta map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload/gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		meta, raw = parseUpload(t, r)
		testutil.WriteJSON(t, w, map[string]any{"id": "s1", "threadId": "t1"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected call %s %s", r.Method, r.URL) })
	data := bytes.Repeat([]byte("x"), 3<<20) // larger than a JSON-friendly raw payload
	res, err := SendMessage(context.Background(), ComposeInput{
		To: []string{"a@b.com"}, Body: "see attached",
		Attachments: []OutgoingAttachment{{Filename: "big.txt", Data: data}},
	}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if res.MessageID != "s1" || len(meta) != 0 {
		t.Errorf("res %+v meta %v", res, meta)
	}
	_, parts := readMixed(t, raw)
	if len(parts) != 2 || !bytes.Equal(parts[1].data, data) {
		t.Fatalf("parts = %d", len(parts))
	}
}

func TestComposeRejectsAttachmentsBeforeNetwork(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected call %s", r.URL) })
	opts := testutil.FakeGoogle(t, mux)
	in := ComposeInput{ReplyToMessageID: "orig", Attachments: []OutgoingAttachment{{Filename: "a\nb"}}}
	if _, err := CreateDraft(context.Background(), in, opts...); err == nil {
		t.Error("CreateDraft accepted a bad attachment")
	}
	if _, err := ResolveRecipients(context.Background(), in, opts...); err == nil {
		t.Error("ResolveRecipients accepted a bad attachment")
	}
}

func attachmentMux(t *testing.T, attID string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/m1/attachments/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"data": base64.URLEncoding.EncodeToString([]byte("PDFDATA")), "size": 7})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "m1", "payload": map[string]any{"mimeType": "multipart/mixed", "parts": []any{
			map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": "aGk", "size": 2}},
			map[string]any{"mimeType": "application/pdf", "filename": "Factura ñ.pdf", "body": map[string]any{"attachmentId": attID, "size": 7}},
			map[string]any{"mimeType": "application/octet-stream", "filename": "other.bin", "body": map[string]any{"attachmentId": "zzz", "size": 99}},
		}}})
	})
	return mux
}

func TestGetAttachmentFile(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ name, metaID string }{{"same id", "A1"}, {"id changed, matched by size", "A1-other"}} {
		t.Run(c.name, func(t *testing.T) {
			a, err := GetAttachmentFile(ctx, "m1", "A1", testutil.FakeGoogle(t, attachmentMux(t, c.metaID))...)
			if err != nil {
				t.Fatal(err)
			}
			if a.Filename != "Factura ñ.pdf" || a.ContentType != "application/pdf" || string(a.Data) != "PDFDATA" {
				t.Errorf("attachment = %+v", a)
			}
		})
	}
	mux := attachmentMux(t, "A1")
	mux.HandleFunc("GET /gmail/v1/users/me/messages/m1/attachments/zzz", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"data": base64.URLEncoding.EncodeToString([]byte("bin")), "size": 3})
	})
	a, err := GetAttachmentFile(ctx, "m1", "zzz", testutil.FakeGoogle(t, mux)...)
	if err != nil || a.Filename != "other.bin" || a.ContentType != "" { // octet-stream left to detection
		t.Errorf("octet-stream: %+v %v", a, err)
	}
	if _, err := GetAttachmentFile(ctx, "m1", " "); err == nil {
		t.Error("empty attachment id accepted")
	}
}

func TestGetAttachmentFileUnknownName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/m1/attachments/A1", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"data": base64.URLEncoding.EncodeToString([]byte("x"))})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"id": "m1", "payload": map[string]any{"mimeType": "text/plain"}})
	})
	a, err := GetAttachmentFile(context.Background(), "m1", "A1", testutil.FakeGoogle(t, mux)...)
	if err != nil || a.Filename != "" || string(a.Data) != "x" {
		t.Errorf("%+v %v", a, err)
	}
}
