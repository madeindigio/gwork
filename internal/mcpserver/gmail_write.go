package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/option"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/fsutil"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

type gmailComposeInput struct {
	To               []string               `json:"to,omitempty" jsonschema:"recipient addresses, e.g. 'ana@example.com' or 'Ana Perez <ana@example.com>'; optional on replies (defaults to the original Reply-To or From)"`
	Cc               []string               `json:"cc,omitempty" jsonschema:"Cc addresses"`
	Bcc              []string               `json:"bcc,omitempty" jsonschema:"Bcc addresses"`
	Subject          string                 `json:"subject,omitempty" jsonschema:"subject line (single line); on replies it defaults to 'Re: <original subject>'"`
	Body             string                 `json:"body,omitempty" jsonschema:"plain-text UTF-8 message body"`
	ReplyToMessageID string                 `json:"reply_to_message_id,omitempty" jsonschema:"id of the message to reply to: keeps the conversation thread and sets the reply headers"`
	ReplyAll         bool                   `json:"reply_all,omitempty" jsonschema:"with reply_to_message_id, also address the original To and Cc recipients (except the user's own address)"`
	Attachments      []gmailAttachmentInput `json:"attachments,omitempty" jsonschema:"files to attach (at most 25 MB in total); each item sets exactly one source: path, content_base64 (with filename) or message_id + attachment_id"`
}

// gmailAttachmentInput is one attachment of gmailComposeInput: a local file,
// inline content or an attachment of an existing message.
type gmailAttachmentInput struct {
	Path          string `json:"path,omitempty" jsonschema:"absolute path of a local file to attach; it is read from the filesystem of the machine running gwork (not the client's), any readable file is allowed"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"inline file content, base64 encoded (standard or URL alphabet); requires filename"`
	MessageID     string `json:"message_id,omitempty" jsonschema:"id of an existing Gmail message whose attachment to re-attach (with attachment_id, as listed by gmail_get_message)"`
	AttachmentID  string `json:"attachment_id,omitempty" jsonschema:"attachment_id of that message's attachment (with message_id)"`
	Filename      string `json:"filename,omitempty" jsonschema:"attachment name shown to recipients; required with content_base64, otherwise it overrides the file or original attachment name"`
	ContentType   string `json:"content_type,omitempty" jsonschema:"MIME type, e.g. application/pdf; guessed from the filename or content when omitted"`
}

// source validates that exactly one source is set and names it.
func (a gmailAttachmentInput) source() (string, error) {
	var set []string
	if a.Path != "" {
		set = append(set, "path")
	}
	if a.ContentBase64 != "" {
		set = append(set, "content_base64")
	}
	if a.MessageID != "" || a.AttachmentID != "" {
		set = append(set, "message_id/attachment_id")
	}
	switch {
	case len(set) == 0:
		return "", errors.New("set exactly one of path, content_base64 or message_id + attachment_id")
	case len(set) > 1:
		return "", fmt.Errorf("set exactly one of path, content_base64 or message_id + attachment_id, got %s", strings.Join(set, " and "))
	}
	switch set[0] {
	case "content_base64":
		if strings.TrimSpace(a.Filename) == "" {
			return "", errors.New("content_base64 requires filename")
		}
	case "message_id/attachment_id":
		if a.MessageID == "" || a.AttachmentID == "" {
			return "", errors.New("message_id and attachment_id must be set together")
		}
	}
	return set[0], nil
}

// compose converts the input, loading the attachments. opts are used to
// fetch attachments of existing messages. Errors never include file content.
func (in gmailComposeInput) compose(ctx context.Context, opts []option.ClientOption) (gmail.ComposeInput, error) {
	out := gmail.ComposeInput{
		To: in.To, Cc: in.Cc, Bcc: in.Bcc,
		Subject: in.Subject, Body: in.Body,
		ReplyToMessageID: in.ReplyToMessageID, ReplyAll: in.ReplyAll,
	}
	if len(in.Attachments) == 0 {
		return out, nil
	}
	sources := make([]string, len(in.Attachments))
	for i, a := range in.Attachments { // validate every item before any I/O
		src, err := a.source()
		if err != nil {
			return out, fmt.Errorf("attachments[%d]: %w", i, err)
		}
		sources[i] = src
	}
	remaining := int64(gmail.MaxAttachmentBytes)
	for i, a := range in.Attachments {
		att := gmail.OutgoingAttachment{Filename: a.Filename, ContentType: a.ContentType}
		var err error
		switch sources[i] {
		case "path":
			att.Data, err = fsutil.ReadFile(a.Path, remaining)
			if att.Filename == "" {
				att.Filename = filepath.Base(a.Path)
			}
		case "content_base64":
			att.Data, err = decodeBase64Content(a.ContentBase64, remaining)
		default:
			var got *gmail.OutgoingAttachment
			if got, err = gmail.GetAttachmentFile(ctx, a.MessageID, a.AttachmentID, opts...); err == nil {
				att.Data = got.Data
				if att.Filename == "" {
					att.Filename = got.Filename
				}
				if att.ContentType == "" {
					att.ContentType = got.ContentType
				}
				if att.Filename == "" {
					err = fmt.Errorf("cannot determine the name of attachment %s of message %s: set filename", a.AttachmentID, a.MessageID)
				} else if att.Size() > remaining {
					err = fmt.Errorf("attachments total over the %d MB limit", gmail.MaxAttachmentBytes>>20)
				}
			}
		}
		if err != nil {
			return out, fmt.Errorf("attachments[%d]: %w", i, err)
		}
		remaining -= att.Size()
		out.Attachments = append(out.Attachments, att)
	}
	if err := gmail.ValidateAttachments(out.Attachments); err != nil {
		return out, err
	}
	return out, nil
}

type gmailSendDraftInput struct {
	DraftID string `json:"draft_id" jsonschema:"id of the draft to send (the draft_id returned by gmail_create_draft)"`
}

type gmailTargetInput struct {
	MessageID string `json:"message_id,omitempty" jsonschema:"id of a single message (give exactly one of message_id and thread_id)"`
	ThreadID  string `json:"thread_id,omitempty" jsonschema:"id of a whole conversation (give exactly one of message_id and thread_id)"`
}

func (in gmailTargetInput) target() gmail.Target {
	return gmail.Target{MessageID: in.MessageID, ThreadID: in.ThreadID}
}

type gmailModifyLabelsInput struct {
	gmailTargetInput
	AddLabels    []string `json:"add_labels,omitempty" jsonschema:"labels to add: label ids or names (names are case-insensitive), e.g. STARRED, UNREAD, IMPORTANT or a user label name"`
	RemoveLabels []string `json:"remove_labels,omitempty" jsonschema:"labels to remove: ids or names. Archive = remove INBOX; mark read = remove UNREAD; mark unread = add UNREAD; star = add STARRED"`
}

// registerGmailWrite registers the Gmail write tools with addWriteTool. It is
// called only when the operator enabled gmail writes (--allow-write) and the
// account granted the gmail write scopes. The tools that send email are
// registered only with --allow-send.
func registerGmailWrite(s *mcp.Server, deps Deps) {
	no, yes := false, true

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_create_draft",
		Description: "Create a Gmail draft (optionally a reply in an existing thread). Nothing is sent: the user reviews and sends it from Gmail. " +
			"Prefer this over sending; use it whenever the user asks you to write or answer an email. " +
			"Set reply_to_message_id to reply; recipients and subject then default from the original message. " +
			"attachments adds files: a local path, inline base64 content or an attachment of an existing message (to forward it).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &no},
	}, func(ctx context.Context, in gmailComposeInput) (gmail.DraftResult, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.DraftResult{}, err
		}
		msg, err := in.compose(ctx, opts)
		if err != nil {
			return gmail.DraftResult{}, err
		}
		res, err := gmail.CreateDraft(ctx, msg, opts...)
		if err != nil {
			return gmail.DraftResult{}, err
		}
		return *res, nil
	})

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_modify_labels",
		Description: "Add or remove labels on a message or on a whole thread (exactly one of message_id and thread_id). " +
			"Also covers archiving (remove INBOX), marking read (remove UNREAD) or unread (add UNREAD) and starring (add STARRED). " +
			"Label names are resolved case-insensitively; unknown names are an error. Reversible.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no},
	}, func(ctx context.Context, in gmailModifyLabelsInput) (gmail.ModifyResult, error) {
		if err := in.target().Validate(); err != nil {
			return gmail.ModifyResult{}, err
		}
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.ModifyResult{}, err
		}
		res, err := gmail.ModifyLabels(ctx, in.target(), in.AddLabels, in.RemoveLabels, opts...)
		if err != nil {
			return gmail.ModifyResult{}, err
		}
		return *res, nil
	})

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_trash",
		Description: "Move a message or a whole thread (exactly one of message_id and thread_id) to the Trash. " +
			"IMPORTANT: ask the user for explicit confirmation before calling this tool. Gmail deletes trashed items permanently after 30 days; gmail_untrash restores them.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: true, OpenWorldHint: &no},
	}, func(ctx context.Context, in gmailTargetInput) (gmail.ModifyResult, error) {
		return gmailTrash(ctx, deps, in.target(), true)
	})

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name:        "gmail_untrash",
		Description: "Restore a message or a whole thread (exactly one of message_id and thread_id) from the Trash.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no},
	}, func(ctx context.Context, in gmailTargetInput) (gmail.ModifyResult, error) {
		return gmailTrash(ctx, deps, in.target(), false)
	})

	if !deps.Write.AllowSend {
		return
	}

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_send_draft",
		Description: "Send an existing Gmail draft to its recipients. Sending cannot be undone. " +
			"IMPORTANT: ask the user for explicit confirmation (recipients and content) before calling this tool.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: false, OpenWorldHint: &yes},
	}, func(ctx context.Context, in gmailSendDraftInput) (gmail.SendResult, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.SendResult{}, err
		}
		res, err := gmail.SendDraft(ctx, in.DraftID, opts...)
		if err != nil {
			return gmail.SendResult{}, err
		}
		return *res, nil
	})

	addWriteTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_send_message",
		Description: "Send an email immediately (optionally a reply in an existing thread). Sending cannot be undone and reaches other people. " +
			"IMPORTANT: ask the user for explicit confirmation of recipients, subject, body and attachments before calling this tool, and prefer gmail_create_draft. " +
			"attachments works as in gmail_create_draft.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: false, OpenWorldHint: &yes},
	}, func(ctx context.Context, in gmailComposeInput) (gmail.SendResult, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.SendResult{}, err
		}
		msg, err := in.compose(ctx, opts)
		if err != nil {
			return gmail.SendResult{}, err
		}
		res, err := gmail.SendMessage(ctx, msg, opts...)
		if err != nil {
			return gmail.SendResult{}, err
		}
		return *res, nil
	})
}

func gmailTrash(ctx context.Context, deps Deps, t gmail.Target, on bool) (gmail.ModifyResult, error) {
	if err := t.Validate(); err != nil {
		return gmail.ModifyResult{}, err
	}
	opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
	if err != nil {
		return gmail.ModifyResult{}, err
	}
	fn := gmail.Untrash
	if on {
		fn = gmail.Trash
	}
	res, err := fn(ctx, t, opts...)
	if err != nil {
		return gmail.ModifyResult{}, err
	}
	return *res, nil
}
