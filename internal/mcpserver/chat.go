package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	chatapi "google.golang.org/api/chat/v1"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/timeutil"
	"github.com/digio/gwork-cli/internal/workspace/chat"
)

// Caps on chat tool inputs, so one call cannot page through an unbounded
// number of messages.
const (
	// chatMaxResults caps max_results of the chat tools.
	chatMaxResults = 1000
	// chatMaxScan caps max_scan of chat_search_messages.
	chatMaxScan = 20000
)

type chatListSpacesInput struct {
	Type       string `json:"type,omitempty" jsonschema:"only this space type: space, group or dm (default: all)"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"maximum number of spaces (default 100, max 1000)"`
}

type chatListSpacesOutput struct {
	Spaces []chat.Space `json:"spaces" jsonschema:"spaces the user is a member of"`
}

type chatFindDMInput struct {
	Email string `json:"email" jsonschema:"email address of the other user"`
}

type chatFindDMOutput struct {
	Space chat.Space `json:"space" jsonschema:"the direct message space"`
}

type chatListMessagesInput struct {
	Space      string `json:"space" jsonschema:"space resource name (spaces/XXX) or bare id"`
	Since      string `json:"since,omitempty" jsonschema:"only messages created at or after this time: RFC 3339, YYYY-MM-DD, today, yesterday or relative like 7d, 24h"`
	Until      string `json:"until,omitempty" jsonschema:"only messages created before this time (same syntax as since)"`
	Thread     string `json:"thread,omitempty" jsonschema:"only messages of this thread (spaces/S/threads/T or bare T)"`
	Order      string `json:"order,omitempty" jsonschema:"asc (oldest first) or desc (newest first, default)"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"maximum number of messages (default 50, max 1000)"`
	MaxChars   int    `json:"max_chars,omitempty" jsonschema:"maximum characters of each message text (default 20000)"`
}

type chatMessagesOutput struct {
	Messages  []chat.Message `json:"messages" jsonschema:"messages; sender.display_name is only set when Google exposes it"`
	Truncated bool           `json:"truncated" jsonschema:"true when at least one message text was cut to max_chars"`
}

type chatGetMessageInput struct {
	MessageName string `json:"message_name" jsonschema:"message resource name: spaces/{space}/messages/{message}"`
	MaxChars    int    `json:"max_chars,omitempty" jsonschema:"maximum characters of the message text (default 20000)"`
}

type chatGetMessageOutput struct {
	Message   chat.Message `json:"message" jsonschema:"the message"`
	Truncated bool         `json:"truncated" jsonschema:"true when the text was cut to max_chars"`
}

type chatSearchMessagesInput struct {
	Text       string   `json:"text" jsonschema:"words to find; case-insensitive, every word must appear"`
	Spaces     []string `json:"spaces,omitempty" jsonschema:"limit the search to these spaces (spaces/XXX or bare ids); default: all spaces of the user"`
	Since      string   `json:"since,omitempty" jsonschema:"only messages created at or after this time (default 7d)"`
	MaxResults int      `json:"max_results,omitempty" jsonschema:"maximum number of matches returned (default 50, max 1000)"`
	MaxScan    int      `json:"max_scan,omitempty" jsonschema:"maximum number of messages examined (default 2000, max 20000)"`
	MaxChars   int      `json:"max_chars,omitempty" jsonschema:"maximum characters of each message text (default 20000)"`
}

type chatSearchMessagesOutput struct {
	chat.SearchResult
	Truncated bool `json:"truncated" jsonschema:"true when at least one message text was cut to max_chars"`
}

// registerChat registers the chat_* tools. It is called by New only when the
// chat service is requested and granted.
func registerChat(s *mcp.Server, deps Deps) {
	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_list_spaces",
		Description: "List the Google Chat spaces, group chats and direct messages the user is a member of " +
			"(name, display_name, type, last_active_time, member_count). DMs and group chats usually have no " +
			"display name; use chat_find_dm to locate the DM with a person.",
	}, func(ctx context.Context, in chatListSpacesInput) (chatListSpacesOutput, error) {
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatListSpacesOutput{}, err
		}
		spaces, err := chat.ListSpaces(ctx, svc, chat.ListSpacesOptions{Type: in.Type, Max: min(in.MaxResults, chatMaxResults)})
		if err != nil {
			return chatListSpacesOutput{}, err
		}
		return chatListSpacesOutput{Spaces: spaces}, nil
	})

	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name:        "chat_find_dm",
		Description: "Find the direct message space between the user and another person by email. Fails when no DM exists yet.",
	}, func(ctx context.Context, in chatFindDMInput) (chatFindDMOutput, error) {
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatFindDMOutput{}, err
		}
		space, err := chat.FindDirectMessage(ctx, svc, in.Email)
		if err != nil {
			return chatFindDMOutput{}, err
		}
		return chatFindDMOutput{Space: space}, nil
	})

	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_list_messages",
		Description: "List messages of a Google Chat space, newest first by default, optionally within a time window " +
			"and/or one thread (max_results up to 1000). Senders carry name (users/{id}) and display_name; missing display names are filled in " +
			"from space memberships when possible. since/until: " + timeExpressions,
	}, func(ctx context.Context, in chatListMessagesInput) (chatMessagesOutput, error) {
		win, err := timeutil.ParseWindow(in.Since, in.Until, deps.CurrentTime(), "", "")
		if err != nil {
			return chatMessagesOutput{}, err
		}
		order := in.Order
		if order == "" {
			order = chat.OrderDesc
		}
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatMessagesOutput{}, err
		}
		msgs, err := chat.ListMessages(ctx, svc, in.Space, chat.ListMessagesOptions{
			Window: win, Thread: in.Thread, Max: min(in.MaxResults, chatMaxResults), Order: order,
		})
		if err != nil {
			return chatMessagesOutput{}, err
		}
		chat.NewNameResolver(svc).Resolve(ctx, msgs)
		truncated := truncateChatTexts(msgs, in.MaxChars)
		return chatMessagesOutput{Messages: msgs, Truncated: truncated}, nil
	})

	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name:        "chat_get_message",
		Description: "Get one Google Chat message by resource name (spaces/{space}/messages/{message}).",
	}, func(ctx context.Context, in chatGetMessageInput) (chatGetMessageOutput, error) {
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatGetMessageOutput{}, err
		}
		m, err := chat.GetMessage(ctx, svc, in.MessageName)
		if err != nil {
			return chatGetMessageOutput{}, err
		}
		one := []chat.Message{m}
		chat.NewNameResolver(svc).Resolve(ctx, one)
		truncated := truncateChatTexts(one, in.MaxChars)
		return chatGetMessageOutput{Message: one[0], Truncated: truncated}, nil
	})

	addReadOnlyTool(s, deps, auth.Chat, &mcp.Tool{
		Name: "chat_search_messages",
		Description: "Search Google Chat messages containing text (case-insensitive; all words must appear). " +
			"LIMITATION: Chat has no server-side text search, so this lists the messages of each space " +
			"(all spaces, or the given ones) created since `since` (default 7d) and filters them locally, " +
			"stopping after max_scan messages (default 2000, max 20000). When cap_reached is true results may be incomplete: narrow " +
			"since or spaces. Matches are newest first; scanned and spaces_scanned report the coverage. " +
			"since: " + timeExpressions,
	}, func(ctx context.Context, in chatSearchMessagesInput) (chatSearchMessagesOutput, error) {
		win, err := timeutil.ParseWindow(in.Since, "", deps.CurrentTime(), "7d", "")
		if err != nil {
			return chatSearchMessagesOutput{}, err
		}
		svc, err := chatService(ctx, deps)
		if err != nil {
			return chatSearchMessagesOutput{}, err
		}
		res, err := chat.SearchMessages(ctx, svc, chat.SearchOptions{
			Text: in.Text, Spaces: in.Spaces, Window: win,
			Max: min(in.MaxResults, chatMaxResults), MaxScan: min(in.MaxScan, chatMaxScan),
		})
		if err != nil {
			return chatSearchMessagesOutput{}, err
		}
		chat.NewNameResolver(svc).Resolve(ctx, res.Matches)
		truncated := truncateChatTexts(res.Matches, in.MaxChars)
		return chatSearchMessagesOutput{SearchResult: res, Truncated: truncated}, nil
	})
}

// chatService builds a Chat API client for the current account.
func chatService(ctx context.Context, deps Deps) (*chatapi.Service, error) {
	opts, err := deps.ClientOptions(ctx, auth.Chat)
	if err != nil {
		return nil, err
	}
	return chat.New(ctx, opts...)
}

// truncateChatTexts cuts each message text to maxChars (default
// DefaultMaxChars) and reports whether any was cut.
func truncateChatTexts(msgs []chat.Message, maxChars int) bool {
	texts := make([]*string, len(msgs))
	for i := range msgs {
		texts[i] = &msgs[i].Text
	}
	return truncateEach(maxChars, texts...)
}
