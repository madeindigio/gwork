package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/workspace/chat"
)

// chatMaxUnreadPerSpace caps max_per_space of chat_list_unread_messages.
const chatMaxUnreadPerSpace = 200

type chatListSectionsInput struct {
	// No fields: an empty struct still yields an object schema.
}

type chatListSectionsOutput struct {
	Sections []chat.Section `json:"sections" jsonschema:"sections of the user's Chat sidebar; display_name is only set for custom sections"`
}

type chatListUnreadMessagesInput struct {
	Spaces      []string `json:"spaces,omitempty" jsonschema:"only check these spaces (spaces/XXX or bare ids); default: all spaces of the user"`
	Type        string   `json:"type,omitempty" jsonschema:"only check this space type: space, group or dm (ignored when spaces is set)"`
	Section     string   `json:"section,omitempty" jsonschema:"only check the spaces of this sidebar section: display name, id or resource name (ignored when spaces is set)"`
	MaxPerSpace int      `json:"max_per_space,omitempty" jsonschema:"maximum number of unread messages per space (default 20, max 200)"`
	MaxChars    int      `json:"max_chars,omitempty" jsonschema:"maximum characters of each message text (default 20000)"`
}

type chatListUnreadMessagesOutput struct {
	chat.UnreadResult
	Truncated bool `json:"truncated" jsonschema:"true when at least one message text was cut to max_chars"`
}

// registerChatUnread registers the tools based on the user's sections and
// read state. It is called by registerChat.
func registerChatUnread(s *mcp.Server, deps Deps) {
	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_list_sections",
		Description: "List the sections of the user's Google Chat sidebar: the default ones and the custom sections " +
			"the user created to group conversations (e.g. favorites). Pass a section to chat_list_spaces or " +
			"chat_list_unread_messages to work with its spaces.",
	}, func(ctx context.Context, _ chatListSectionsInput) (chatListSectionsOutput, error) {
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatListSectionsOutput{}, err
		}
		sections, err := chat.ListSections(ctx, svc)
		if err != nil {
			return chatListSectionsOutput{}, err
		}
		return chatListSectionsOutput{Sections: sections}, nil
	})

	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_list_unread_messages",
		Description: "List the Google Chat messages the user has not read yet, grouped by space (most recently active " +
			"first; messages newest first, up to max_per_space each, more=true when a space has more). " +
			"LIMITATION: Chat has no unread flag, so this reads the user's read position in each space (all spaces, " +
			"or the given spaces, type or section) and lists the messages created after it; read positions of " +
			"individual threads are not considered. spaces_checked and failed_spaces report the coverage.",
	}, func(ctx context.Context, in chatListUnreadMessagesInput) (chatListUnreadMessagesOutput, error) {
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatListUnreadMessagesOutput{}, err
		}
		res, err := chat.ListUnread(ctx, svc, chat.UnreadOptions{
			Spaces: in.Spaces, Type: in.Type, Section: in.Section,
			MaxPerSpace: min(in.MaxPerSpace, chatMaxUnreadPerSpace),
		})
		if err != nil {
			return chatListUnreadMessagesOutput{}, err
		}
		resolver := chat.NewNameResolver(svc)
		var texts []*string
		for _, u := range res.Spaces {
			resolver.Resolve(ctx, u.Messages)
			for i := range u.Messages {
				texts = append(texts, &u.Messages[i].Text)
			}
		}
		return chatListUnreadMessagesOutput{UnreadResult: res, Truncated: truncateEach(in.MaxChars, texts...)}, nil
	})
}
