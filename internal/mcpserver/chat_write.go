package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/workspace/chat"
)

type chatSendMessageInput struct {
	Space     string `json:"space,omitempty" jsonschema:"target space (spaces/XXX or bare id); exactly one of space and user_email"`
	UserEmail string `json:"user_email,omitempty" jsonschema:"email of a user whose existing direct message to post in; fails if no DM exists (spaces are never created); exactly one of space and user_email"`
	Text      string `json:"text" jsonschema:"message text, at most 4096 characters"`
	Thread    string `json:"thread,omitempty" jsonschema:"reply in this thread of the target space (spaces/S/threads/T); fails if the thread does not exist"`
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
			"this server. Before calling, show the user the exact text and the target (space or user) and get their " +
			"explicit confirmation; never send on your own initiative. Set exactly one of space and user_email.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, in chatSendMessageInput) (chatSendMessageOutput, error) {
		si := chat.SendInput{Space: in.Space, UserEmail: in.UserEmail, Text: in.Text, Thread: in.Thread}
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
