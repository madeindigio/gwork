package gmail

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

// b64 encodes s the way Gmail does (base64url, unpadded).
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// textPart returns a leaf part with an inline body and a Content-Type header.
func textPart(mimeType, contentType, body string) *gmailapi.MessagePart {
	return &gmailapi.MessagePart{
		MimeType: mimeType,
		Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Type", Value: contentType}},
		Body:     &gmailapi.MessagePartBody{Data: b64(body), Size: int64(len(body))},
	}
}

func multipart(mimeType string, parts ...*gmailapi.MessagePart) *gmailapi.MessagePart {
	return &gmailapi.MessagePart{MimeType: mimeType, Body: &gmailapi.MessagePartBody{}, Parts: parts}
}

// Fixtures modelled on real Gmail API format=full payloads.
var (
	fixtureAlternative = multipart("multipart/alternative",
		textPart("text/plain", `text/plain; charset="UTF-8"`, "Hello Bob,\r\n\r\nSee you tomorrow.\r\n"),
		textPart("text/html", `text/html; charset="UTF-8"`, "<div>Hello <b>Bob</b>,</div><div>See you tomorrow.</div>"),
	)

	fixtureMixed = multipart("multipart/mixed",
		multipart("multipart/related",
			multipart("multipart/alternative",
				textPart("text/plain", "text/plain; charset=utf-8", "Report attached."),
				textPart("text/html", "text/html; charset=utf-8", "<p>Report attached.</p>"),
			),
			&gmailapi.MessagePart{
				MimeType: "image/png",
				Filename: "logo.png",
				Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Disposition", Value: "inline; filename=logo.png"}},
				Body:     &gmailapi.MessagePartBody{AttachmentId: "att-logo", Size: 1200},
			},
		),
		&gmailapi.MessagePart{
			MimeType: "application/pdf",
			Filename: "report.pdf",
			Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Disposition", Value: `attachment; filename="report.pdf"`}},
			Body:     &gmailapi.MessagePartBody{AttachmentId: "att-pdf", Size: 34567},
		},
		&gmailapi.MessagePart{
			MimeType: "text/plain",
			Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Disposition", Value: "attachment"}},
			Body:     &gmailapi.MessagePartBody{AttachmentId: "att-txt", Size: 10},
		},
	)

	fixtureHTMLOnly = textPart("text/html", "text/html; charset=utf-8",
		`<html><head><style>p{color:red}</style><title>x</title></head><body>`+
			`<script>alert(1)</script><h1>Welcome</h1><p>Read the <a href="https://example.com/doc">docs</a> &amp; enjoy&nbsp;it.</p>`+
			`<ul><li>one</li><li>two</li></ul></body></html>`)

	fixtureLatin1 = &gmailapi.MessagePart{
		MimeType: "text/plain",
		Headers:  []*gmailapi.MessagePartHeader{{Name: "Content-Type", Value: "text/plain; charset=ISO-8859-1"}},
		Body:     &gmailapi.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte("Caf\xe9 con le\xf1a")), Size: 13},
	}

	fixtureNoBody = multipart("multipart/mixed",
		&gmailapi.MessagePart{MimeType: "text/plain", Body: &gmailapi.MessagePartBody{Size: 0}},
		&gmailapi.MessagePart{MimeType: "text/plain"},
	)
)

func TestExtractBody(t *testing.T) {
	tests := []struct {
		name        string
		payload     *gmailapi.MessagePart
		wantBody    string
		wantSource  string
		wantAttIDs  []string
		wantContain []string
	}{
		{
			name:       "multipart/alternative prefers text/plain",
			payload:    fixtureAlternative,
			wantBody:   "Hello Bob,\n\nSee you tomorrow.",
			wantSource: "text/plain",
			wantAttIDs: []string{},
		},
		{
			name:       "nested multipart/mixed with attachments",
			payload:    fixtureMixed,
			wantBody:   "Report attached.",
			wantSource: "text/plain",
			wantAttIDs: []string{"att-logo", "att-pdf", "att-txt"},
		},
		{
			name:       "html only falls back to converted text",
			payload:    fixtureHTMLOnly,
			wantBody:   "Welcome\n\nRead the docs (https://example.com/doc) & enjoy it.\n\n- one\n- two",
			wantSource: "text/html",
			wantAttIDs: []string{},
		},
		{
			name:       "ISO-8859-1 charset is converted to UTF-8",
			payload:    fixtureLatin1,
			wantBody:   "Café con leña",
			wantSource: "text/plain",
			wantAttIDs: []string{},
		},
		{
			name:       "missing body",
			payload:    fixtureNoBody,
			wantBody:   "",
			wantSource: "",
			wantAttIDs: []string{},
		},
		{
			name:       "nil payload",
			payload:    nil,
			wantAttIDs: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, source, raw, atts, err := extractBody(tt.payload, false, nil)
			if err != nil {
				t.Fatalf("extractBody: %v", err)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
			if raw != "" {
				t.Errorf("raw html = %q, want empty when not requested", raw)
			}
			if atts == nil {
				t.Fatal("attachments is nil, want empty slice")
			}
			var ids []string
			for _, a := range atts {
				ids = append(ids, a.AttachmentID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.wantAttIDs, ",") {
				t.Errorf("attachment ids = %v, want %v", ids, tt.wantAttIDs)
			}
		})
	}
}

func TestExtractBodyAttachmentDetails(t *testing.T) {
	_, _, _, atts, err := extractBody(fixtureMixed, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Attachment{AttachmentID: "att-pdf", Filename: "report.pdf", MimeType: "application/pdf", Size: 34567}
	if atts[1] != want {
		t.Errorf("attachment = %+v, want %+v", atts[1], want)
	}
}

func TestExtractBodyIncludeHTML(t *testing.T) {
	body, source, raw, _, err := extractBody(fixtureAlternative, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if source != "text/plain" || !strings.HasPrefix(body, "Hello Bob") {
		t.Errorf("body/source = %q/%q", body, source)
	}
	if !strings.Contains(raw, "<b>Bob</b>") {
		t.Errorf("raw html = %q, want the html part", raw)
	}
}

func TestExtractBodyOutOfLine(t *testing.T) {
	payload := &gmailapi.MessagePart{
		MimeType: "text/plain",
		Body:     &gmailapi.MessagePartBody{AttachmentId: "big-body", Size: 5},
	}
	var asked string
	fetch := func(id string) (string, error) {
		asked = id
		return b64("large body"), nil
	}
	body, _, _, atts, err := extractBody(payload, false, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "big-body" || body != "large body" || len(atts) != 0 {
		t.Errorf("asked=%q body=%q atts=%v", asked, body, atts)
	}

	_, _, _, _, err = extractBody(payload, false, func(string) (string, error) { return "", errors.New("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want fetch error", err)
	}
}

func TestDecodeBase64URL(t *testing.T) {
	const want = "hello?>world~"
	for _, in := range []string{
		base64.RawURLEncoding.EncodeToString([]byte(want)),
		base64.URLEncoding.EncodeToString([]byte(want)),
		base64.StdEncoding.EncodeToString([]byte(want)),
	} {
		got, err := decodeBase64URL(in)
		if err != nil || string(got) != want {
			t.Errorf("decode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := decodeBase64URL("!!!"); err == nil {
		t.Error("want error for invalid input")
	}
}

func TestToUTF8(t *testing.T) {
	tests := []struct {
		in, charset, want string
	}{
		{"plain", "", "plain"},
		{"café", "UTF-8", "café"},
		{"caf\xe9", "iso-8859-1", "café"},
		{"\x80 euro", "windows-1252", "€ euro"},
		{"caf\xe9", "x-unknown", "caf�"},
		{"caf\xe9", "", "caf�"},
	}
	for _, tt := range tests {
		if got := toUTF8([]byte(tt.in), tt.charset); got != tt.want {
			t.Errorf("toUTF8(%q, %q) = %q, want %q", tt.in, tt.charset, got, tt.want)
		}
	}
}
