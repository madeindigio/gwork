package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

type gmailComposeInput struct {
	To               []string `json:"to,omitempty" jsonschema:"recipient addresses, e.g. 'ana@example.com' or 'Ana Perez <ana@example.com>'; optional on replies (defaults to the original Reply-To or From)"`
	Cc               []string `json:"cc,omitempty" jsonschema:"Cc addresses"`
	Bcc              []string `json:"bcc,omitempty" jsonschema:"Bcc addresses"`
	Subject          string   `json:"subject,omitempty" jsonschema:"subject line (single line); on replies it defaults to 'Re: <original subject>'"`
	Body             string   `json:"body,omitempty" jsonschema:"plain-text UTF-8 message body"`
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty" jsonschema:"id of the message to reply to: keeps the conversation thread and sets the reply headers"`
	ReplyAll         bool     `json:"reply_all,omitempty" jsonschema:"with reply_to_message_id, also address the original To and Cc recipients (except the user's own address)"`
}

func (in gmailComposeInput) compose() gmail.ComposeInput {
	return gmail.ComposeInput{
		To: in.To, Cc: in.Cc, Bcc: in.Bcc,
		Subject: in.Subject, Body: in.Body,
		ReplyToMessageID: in.ReplyToMessageID, ReplyAll: in.ReplyAll,
	}
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
			"Set reply_to_message_id to reply; recipients and subject then default from the original message.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &no},
	}, func(ctx context.Context, in gmailComposeInput) (gmail.DraftResult, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.DraftResult{}, err
		}
		res, err := gmail.CreateDraft(ctx, in.compose(), opts...)
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
			"IMPORTANT: ask the user for explicit confirmation of recipients, subject and body before calling this tool, and prefer gmail_create_draft.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: false, OpenWorldHint: &yes},
	}, func(ctx context.Context, in gmailComposeInput) (gmail.SendResult, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmail.SendResult{}, err
		}
		res, err := gmail.SendMessage(ctx, in.compose(), opts...)
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
