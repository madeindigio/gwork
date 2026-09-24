package gmail

import (
	"encoding/base64"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
	gmailapi "google.golang.org/api/gmail/v1"
)

// Body sources reported in Message.BodySource.
const (
	sourcePlain = "text/plain"
	sourceHTML  = "text/html"
)

// fetchFunc downloads the data of a body stored out of line (a part with an
// attachment id and no inline data). It returns base64url data.
type fetchFunc func(attachmentID string) (string, error)

// extracted is the result of walking a MIME tree.
type extracted struct {
	Plain       []*gmailapi.MessagePart
	HTML        []*gmailapi.MessagePart
	Attachments []Attachment
}

// walkParts classifies the leaves of the MIME tree rooted at p into inline
// text/plain parts, inline text/html parts and attachments, in document
// order.
func walkParts(p *gmailapi.MessagePart) extracted {
	ex := extracted{Attachments: []Attachment{}}
	var walk func(p *gmailapi.MessagePart)
	walk = func(p *gmailapi.MessagePart) {
		if p == nil {
			return
		}
		mt := strings.ToLower(p.MimeType)
		switch {
		case isAttachment(p):
			a := Attachment{Filename: p.Filename, MimeType: p.MimeType}
			if p.Body != nil {
				a.AttachmentID = p.Body.AttachmentId
				a.Size = p.Body.Size
			}
			ex.Attachments = append(ex.Attachments, a)
		case len(p.Parts) > 0:
			for _, c := range p.Parts {
				walk(c)
			}
		case mt == "text/plain":
			ex.Plain = append(ex.Plain, p)
		case mt == "text/html":
			ex.HTML = append(ex.HTML, p)
		}
	}
	walk(p)
	return ex
}

// isAttachment reports whether p is a file rather than inline body text.
func isAttachment(p *gmailapi.MessagePart) bool {
	if p.Filename != "" {
		return true
	}
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, "Content-Disposition") {
			d, _, err := mime.ParseMediaType(h.Value)
			if err == nil && d == "attachment" {
				return true
			}
		}
	}
	return false
}

// extractBody returns the text of the message rooted at payload, where it
// came from (sourcePlain, sourceHTML or ""), the raw HTML (when wantHTML)
// and the attachments. fetch may be nil; it is used for out-of-line bodies.
func extractBody(payload *gmailapi.MessagePart, wantHTML bool, fetch fetchFunc) (body, source, rawHTML string, atts []Attachment, err error) {
	ex := walkParts(payload)
	plain, err := joinParts(ex.Plain, fetch)
	if err != nil {
		return "", "", "", nil, err
	}
	var html string
	if len(ex.HTML) > 0 && (wantHTML || strings.TrimSpace(plain) == "") {
		html, err = joinParts(ex.HTML, fetch)
		if err != nil {
			return "", "", "", nil, err
		}
	}
	switch {
	case strings.TrimSpace(plain) != "":
		body, source = normalizeText(plain), sourcePlain
	case strings.TrimSpace(html) != "":
		body, source = HTMLToText(html), sourceHTML
	}
	if wantHTML {
		rawHTML = html
	}
	return body, source, rawHTML, ex.Attachments, nil
}

// joinParts decodes parts and joins them with blank lines.
func joinParts(parts []*gmailapi.MessagePart, fetch fetchFunc) (string, error) {
	var texts []string
	for _, p := range parts {
		s, err := partText(p, fetch)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(s) != "" {
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, "\n\n"), nil
}

// partText decodes the body of a text part to a UTF-8 string, honouring
// the charset parameter of its Content-Type header.
func partText(p *gmailapi.MessagePart, fetch fetchFunc) (string, error) {
	if p.Body == nil {
		return "", nil
	}
	data := p.Body.Data
	if data == "" && p.Body.AttachmentId != "" && fetch != nil {
		var err error
		if data, err = fetch(p.Body.AttachmentId); err != nil {
			return "", err
		}
	}
	if data == "" {
		return "", nil
	}
	raw, err := decodeBase64URL(data)
	if err != nil {
		return "", fmt.Errorf("decode %s part %s: %w", p.MimeType, p.PartId, err)
	}
	return toUTF8(raw, partCharset(p)), nil
}

// partCharset returns the charset parameter of p's Content-Type header.
func partCharset(p *gmailapi.MessagePart) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			_, params, err := mime.ParseMediaType(h.Value)
			if err == nil {
				return params["charset"]
			}
		}
	}
	return ""
}

// toUTF8 converts b from charset to UTF-8. Unknown charsets and invalid
// conversions fall back to the bytes as they are, with invalid UTF-8
// sequences replaced.
func toUTF8(b []byte, charset string) string {
	cs := strings.ToLower(strings.TrimSpace(charset))
	if cs != "" && cs != "utf-8" && cs != "utf8" && cs != "us-ascii" {
		if enc, err := htmlindex.Get(cs); err == nil {
			if out, err := enc.NewDecoder().Bytes(b); err == nil {
				return string(out)
			}
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// decodeBase64URL decodes Gmail's base64url data, padded or not. Standard
// base64 is accepted as a fallback.
func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		return b, nil
	}
	if b2, err2 := base64.RawStdEncoding.DecodeString(s); err2 == nil {
		return b2, nil
	}
	return nil, err
}

// normalizeText converts CRLF line endings to LF and trims surrounding
// blank space.
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}
