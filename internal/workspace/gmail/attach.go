package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/textproto"
	"path"
	"strings"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// MaxAttachmentBytes is the maximum total size of the attachments of one
// composed message (Gmail's own limit for attachments).
const MaxAttachmentBytes = 25 << 20

// OutgoingAttachment is a file attached to a composed message (a draft or a
// message to send). Its data is never serialized to JSON: the JSON form has
// the filename, content type and size only.
type OutgoingAttachment struct {
	// Filename is the attachment name shown to recipients. Directory
	// components are dropped; it must not be empty or contain line breaks.
	Filename string
	// ContentType is the MIME type. When empty it is guessed from the
	// filename extension, then from the data.
	ContentType string
	// Data is the file content.
	Data []byte
}

// Size returns the size of the data in bytes.
func (a OutgoingAttachment) Size() int64 { return int64(len(a.Data)) }

// MarshalJSON renders the attachment without its data:
// {"filename", "content_type", "size"}.
func (a OutgoingAttachment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type,omitempty"`
		Size        int64  `json:"size"`
	}{a.Filename, a.ContentType, a.Size()})
}

// AttachmentName returns the base name of name (both / and \ separate
// directories) and validates it: it must not be empty or contain CR, LF or
// NUL.
func AttachmentName(name string) (string, error) {
	if strings.ContainsAny(name, "\r\n\x00") {
		return "", errors.New("attachment filename must not contain line breaks or NUL")
	}
	base := path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	base = strings.TrimSpace(base)
	switch base {
	case "", ".", "..", "/":
		return "", fmt.Errorf("invalid attachment filename %q", name)
	}
	return base, nil
}

// ValidateAttachments checks the names, content types and total size of
// atts without building a message. CreateDraft, SendMessage and
// ResolveRecipients run it before any API call.
func ValidateAttachments(atts []OutgoingAttachment) error {
	var total int64
	for _, a := range atts {
		if _, err := AttachmentName(a.Filename); err != nil {
			return err
		}
		if a.ContentType != "" {
			if err := checkHeaderValue("attachment content type", a.ContentType); err != nil {
				return err
			}
			if _, _, err := mime.ParseMediaType(a.ContentType); err != nil {
				return fmt.Errorf("invalid content type %q of attachment %q: %w", a.ContentType, a.Filename, err)
			}
		}
		total += a.Size()
	}
	if total > MaxAttachmentBytes {
		return fmt.Errorf("attachments total %s, over the %s limit per message", formatMB(total), formatMB(MaxAttachmentBytes))
	}
	return nil
}

func formatMB(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// attachmentContentType returns the explicit content type, else the one of
// the filename extension, else one sniffed from the data.
func attachmentContentType(a OutgoingAttachment, name string) string {
	if a.ContentType != "" {
		return a.ContentType
	}
	if ext := path.Ext(name); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return http.DetectContentType(a.Data)
}

// writeMultipartBody writes the multipart/mixed body (the text part, then
// one part per attachment) to mw.
func writeMultipartBody(mw *multipart.Writer, body string, atts []OutgoingAttachment) error {
	pw, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qw := quotedprintable.NewWriter(pw)
	if _, err := qw.Write([]byte(body)); err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	if err := qw.Close(); err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	for _, a := range atts {
		name, err := AttachmentName(a.Filename)
		if err != nil {
			return err
		}
		mt, params, err := mime.ParseMediaType(attachmentContentType(a, name))
		if err != nil {
			return fmt.Errorf("content type of attachment %q: %w", name, err)
		}
		params["name"] = name // for clients that ignore Content-Disposition
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(mt, params)},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
		})
		if err != nil {
			return err
		}
		if err := writeBase64Lines(pw, a.Data); err != nil {
			return fmt.Errorf("encode attachment %q: %w", name, err)
		}
	}
	return mw.Close()
}

// writeBase64Lines writes data in standard base64, in CRLF-terminated lines
// of at most 76 characters (RFC 2045).
func writeBase64Lines(w io.Writer, data []byte) error {
	const line = 76
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := min(line, len(enc))
		if _, err := io.WriteString(w, enc[:n]+"\r\n"); err != nil {
			return err
		}
		enc = enc[n:]
	}
	return nil
}

// GetAttachmentFile downloads attachment attachmentID of message messageID
// with its filename and content type, ready to be attached to a new message.
// Filename is "" when the message metadata does not identify the attachment
// (Gmail may issue a different attachment id on each read); set it before
// attaching.
func GetAttachmentFile(ctx context.Context, messageID, attachmentID string, opts ...option.ClientOption) (*OutgoingAttachment, error) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(attachmentID) == "" {
		return nil, errors.New("message id and attachment id are required")
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	data, err := getAttachmentData(ctx, svc, messageID, attachmentID)
	if err != nil {
		return nil, err
	}
	m, err := svc.Users.Messages.Get(userID, messageID).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", messageID, err)
	}
	out := &OutgoingAttachment{Data: data}
	if a, ok := matchAttachment(walkParts(m.Payload).Attachments, attachmentID, int64(len(data))); ok {
		out.Filename = a.Filename
		if !strings.EqualFold(a.MimeType, "application/octet-stream") {
			out.ContentType = a.MimeType
		}
	}
	return out, nil
}

// matchAttachment finds the attachment with id, or else the only one whose
// size is size.
func matchAttachment(atts []Attachment, id string, size int64) (Attachment, bool) {
	for _, a := range atts {
		if a.AttachmentID == id {
			return a, true
		}
	}
	var found []Attachment
	for _, a := range atts {
		if a.Size == size {
			found = append(found, a)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Attachment{}, false
}

// getAttachmentData downloads and decodes an attachment body.
func getAttachmentData(ctx context.Context, svc *gmailapi.Service, messageID, attachmentID string) ([]byte, error) {
	b, err := svc.Users.Messages.Attachments.Get(userID, messageID, attachmentID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get attachment of message %s: %w", messageID, err)
	}
	data, err := decodeBase64URL(b.Data)
	if err != nil {
		return nil, fmt.Errorf("decode attachment of message %s: %w", messageID, err)
	}
	return data, nil
}
