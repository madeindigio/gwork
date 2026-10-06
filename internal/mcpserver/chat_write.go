package mcpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/workspace/chat"
)

type chatSendMessageInput struct {
	Space     string `json:"space,omitempty" jsonschema:"target space (spaces/XXX or bare id); exactly one of space and user_email"`
	UserEmail string `json:"user_email,omitempty" jsonschema:"email of a user whose existing direct message to post in; fails if no DM exists (spaces are never created); exactly one of space and user_email"`
	Text      string `json:"text,omitempty" jsonschema:"message text, at most 4096 characters; required unless attachments are given"`
	Thread    string `json:"thread,omitempty" jsonschema:"reply in this thread of the target space (spaces/S/threads/T); fails if the thread does not exist"`
	// Attachments are files uploaded to the target space and attached to the message.
	Attachments []uploadInput `json:"attachments,omitempty" jsonschema:"files to attach (at most 200 MB each); each item sets exactly one of path or content_base64"`
}

// uploadInput is one file to upload, given either as a local path or as
// inline base64 content. Exactly one of Path and ContentBase64 is set.
type uploadInput struct {
	Path          string `json:"path,omitempty" jsonschema:"path of a local file to attach; the file is read from the machine running gwork (prefer absolute paths, relative ones resolve against the gwork process directory); the attachment is named after its base name"`
	Filename      string `json:"filename,omitempty" jsonschema:"attachment file name including the extension; required with content_base64, not allowed with path"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"file content encoded in standard base64; requires filename"`
	ContentType   string `json:"content_type,omitempty" jsonschema:"optional MIME type; guessed from the file name extension or the content when omitted"`
}

// loadUpload validates u and returns the attachment name, content type
// ("" when not given) and content, reading Path or decoding ContentBase64.
// Content is limited to maxBytes. Errors never include the content.
func (u uploadInput) loadUpload(maxBytes int64) (name, contentType string, data []byte, err error) {
	hasPath := strings.TrimSpace(u.Path) != ""
	hasContent := u.ContentBase64 != ""
	switch {
	case hasPath == hasContent:
		return "", "", nil, errors.New("set exactly one of path or content_base64")
	case hasPath && u.Filename != "":
		return "", "", nil, errors.New("filename is only used with content_base64; a path attachment is named after its base name")
	case hasContent && strings.TrimSpace(u.Filename) == "":
		return "", "", nil, errors.New("filename is required with content_base64")
	}
	if hasPath {
		data, err = readLocalFile(u.Path, maxBytes)
		return filepath.Base(u.Path), u.ContentType, data, err
	}
	enc := strings.TrimSpace(u.ContentBase64)
	if int64(base64.StdEncoding.DecodedLen(len(enc))) > maxBytes+2 {
		return "", "", nil, fmt.Errorf("content_base64 decodes to more than %d bytes", maxBytes)
	}
	if data, err = base64.StdEncoding.DecodeString(enc); err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(enc); err != nil {
			return "", "", nil, fmt.Errorf("content_base64 is not valid base64: %w", err)
		}
	}
	if int64(len(data)) > maxBytes {
		return "", "", nil, fmt.Errorf("content_base64 decodes to more than %d bytes", maxBytes)
	}
	return u.Filename, u.ContentType, data, nil
}

// readLocalFile reads the regular file at path, refusing files larger than
// maxBytes before reading them.
func readLocalFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > maxBytes {
		return nil, fmt.Errorf("%s is %d bytes; the limit is %d bytes", path, fi.Size(), maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxBytes)
	}
	return data, nil
}

type chatSendMessageOutput struct {
	Message chat.Message `json:"message" jsonschema:"the created message"`
}

// registerChatWrite registers the Chat write tools with addWriteTool. It is
// called only when the operator enabled chat writes (--allow-write) and the
// account granted the chat write scopes.
func registerChatWrite(s *mcp.Server, deps Deps) {
	destructive := false
	openWorld := true
	addWriteTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_send_message",
		Description: "Send a Google Chat message as the user to a space, to the existing direct message with a user, " +
			"or as a reply in a thread. The message is delivered immediately to other people and cannot be undone by " +
			"this server. Before calling, show the user the exact text, the target (space or user) and the names of any " +
			"attachments and get their explicit confirmation; never send on your own initiative. Set exactly one of " +
			"space and user_email. Attachments are uploaded to the space first; the text may be omitted when files are attached.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, in chatSendMessageInput) (chatSendMessageOutput, error) {
		si := chat.SendInput{Space: in.Space, UserEmail: in.UserEmail, Text: in.Text, Thread: in.Thread}
		for i, a := range in.Attachments {
			name, ct, data, err := a.loadUpload(chat.MaxAttachmentSize)
			if err != nil {
				return chatSendMessageOutput{}, fmt.Errorf("attachments[%d]: %w", i, err)
			}
			si.Attachments = append(si.Attachments, chat.Upload{Filename: name, ContentType: ct, Data: data})
		}
		if _, _, err := si.Validate(); err != nil {
			return chatSendMessageOutput{}, err
		}
		opts, err := deps.WriteClientOptions(ctx, auth.Chat)
		if err != nil {
			return chatSendMessageOutput{}, err
		}
		svc, err := chat.New(ctx, opts...)
		if err != nil {
			return chatSendMessageOutput{}, err
		}
		msg, err := chat.SendMessage(ctx, svc, si)
		if err != nil {
			return chatSendMessageOutput{}, err
		}
		return chatSendMessageOutput{Message: msg}, nil
	})
}
