package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	chatapi "google.golang.org/api/chat/v1"
	"google.golang.org/api/googleapi"
)

// MaxTextLength is the maximum length, in characters, of a message text
// accepted by the Chat API.
const MaxTextLength = 4096

// MaxAttachmentSize is the maximum size, in bytes, of one file uploaded as
// a message attachment (the media.upload limit of the Chat API, 200 MB).
const MaxAttachmentSize = 200 << 20

// Upload is a file to attach to a message. The caller supplies the content;
// this package never reads files from paths.
type Upload struct {
	// Filename is the attachment name shown in Chat, a base name including
	// the extension (no directory, no control line breaks).
	Filename string
	// ContentType is the MIME type; when empty SendMessage uses
	// DetectContentType.
	ContentType string
	// Data is the file content, at most MaxAttachmentSize bytes.
	Data []byte
}

// Validate checks the filename and the size of the upload.
func (u Upload) Validate() error {
	name := u.Filename
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("attachment filename is required")
	case strings.ContainsAny(name, "\r\n\x00"):
		return fmt.Errorf("attachment filename %q contains a line break or NUL", name)
	case strings.ContainsAny(name, "/\\") || name == "." || name == "..":
		return fmt.Errorf("attachment filename %q must be a base name, without directories", name)
	case strings.ContainsAny(u.ContentType, "\r\n\x00"):
		return fmt.Errorf("attachment %s: content type contains a line break or NUL", name)
	case len(u.Data) > MaxAttachmentSize:
		return fmt.Errorf("attachment %s is %d bytes; the Chat API limit is %d bytes (200 MB)", name, len(u.Data), MaxAttachmentSize)
	}
	return nil
}

// SendInput describes a message to post as the authenticated user.
type SendInput struct {
	// Space is the target space ("spaces/X" or bare "X"). Exactly one of
	// Space and UserEmail must be set.
	Space string
	// UserEmail targets the existing direct message with this user; it is
	// resolved with spaces.findDirectMessage. No space is ever created.
	UserEmail string
	// Text is the message body, at most MaxTextLength characters. It is
	// required unless Attachments is non-empty.
	Text string
	// Thread optionally replies in this thread ("spaces/X/threads/Y" or a
	// bare thread id) of the target space.
	Thread string
	// Attachments are uploaded to the target space with media.upload and
	// attached to the message.
	Attachments []Upload
}

// Validate checks the input without touching the network and returns the
// normalized space name ("" when UserEmail is used) and thread name.
func (in SendInput) Validate() (space, thread string, err error) {
	hasSpace := strings.TrimSpace(in.Space) != ""
	hasUser := strings.TrimSpace(in.UserEmail) != ""
	switch {
	case hasSpace == hasUser:
		return "", "", errors.New("specify exactly one target: a space or a user email")
	case strings.TrimSpace(in.Text) == "" && len(in.Attachments) == 0:
		return "", "", errors.New("message text is required (or attach a file)")
	case utf8.RuneCountInString(in.Text) > MaxTextLength:
		return "", "", fmt.Errorf("message text is %d characters long; the Chat API limit is %d",
			utf8.RuneCountInString(in.Text), MaxTextLength)
	}
	for _, u := range in.Attachments {
		if err := u.Validate(); err != nil {
			return "", "", err
		}
	}
	if hasSpace {
		if space, err = NormalizeSpace(in.Space); err != nil {
			return "", "", err
		}
		thread, err = normalizeThread(space, in.Thread)
		return space, thread, err
	}
	if t := strings.TrimSpace(in.Thread); t != "" && !strings.HasPrefix(t, "spaces/") {
		return "", "", errors.New("with a user target the thread must be a full name (spaces/{space}/threads/{id})")
	}
	return "", strings.TrimSpace(in.Thread), nil
}

// SendMessage posts a message as the authenticated user and returns it.
// With UserEmail the target is the existing DM with that user; if there is
// none the error wraps ErrDirectMessageNotFound and nothing is created.
// With Thread the message is a reply (REPLY_MESSAGE_OR_FAIL). Attachments
// are uploaded to the resolved space first, then referenced by the message;
// if an upload fails no message is posted.
func SendMessage(ctx context.Context, svc *chatapi.Service, in SendInput) (Message, error) {
	space, thread, err := in.Validate()
	if err != nil {
		return Message{}, err
	}
	if space == "" {
		dm, err := FindDirectMessage(ctx, svc, in.UserEmail)
		if err != nil {
			if errors.Is(err, ErrDirectMessageNotFound) {
				return Message{}, fmt.Errorf("no direct message with %s yet: start the conversation from Google Chat first, gwork never creates spaces: %w",
					strings.TrimSpace(in.UserEmail), err)
			}
			return Message{}, err
		}
		space = dm.Name
		if thread != "" {
			if thread, err = normalizeThread(space, thread); err != nil {
				return Message{}, err
			}
		}
	}
	msg := &chatapi.Message{Text: in.Text}
	if strings.TrimSpace(in.Text) == "" {
		msg.Text = "" // attachment-only message
	}
	for _, u := range in.Attachments {
		ref, err := uploadAttachment(ctx, svc, space, u)
		if err != nil {
			return Message{}, err
		}
		msg.Attachment = append(msg.Attachment, &chatapi.Attachment{AttachmentDataRef: ref})
	}
	call := svc.Spaces.Messages.Create(space, msg)
	if thread != "" {
		msg.Thread = &chatapi.Thread{Name: thread}
		call = call.MessageReplyOption("REPLY_MESSAGE_OR_FAIL")
	}
	created, err := call.Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("send message to %s: %w", space, err)
	}
	return convertMessage(created), nil
}

// uploadAttachment uploads u to space and returns the reference to put in
// the message.
func uploadAttachment(ctx context.Context, svc *chatapi.Service, space string, u Upload) (*chatapi.AttachmentDataRef, error) {
	ct := u.ContentType
	if ct == "" {
		ct = DetectContentType(u.Filename, u.Data)
	}
	resp, err := svc.Media.Upload(space, &chatapi.UploadAttachmentRequest{Filename: u.Filename}).
		Media(bytes.NewReader(u.Data), googleapi.ContentType(ct)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("upload attachment %s to %s: %w", u.Filename, space, err)
	}
	if resp.AttachmentDataRef == nil {
		return nil, fmt.Errorf("upload attachment %s to %s: response has no attachment reference", u.Filename, space)
	}
	return resp.AttachmentDataRef, nil
}

// DetectContentType guesses the MIME type of a file from the extension of
// filename, then from its content (http.DetectContentType).
func DetectContentType(filename string, data []byte) string {
	if ct := mime.TypeByExtension(filepath.Ext(filename)); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}
