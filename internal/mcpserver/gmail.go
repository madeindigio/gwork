package mcpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/workspace/gmail"
)

// gmailMaxSearchResults caps max_results of gmail_search.
const gmailMaxSearchResults = 100

type gmailSearchInput struct {
	Query      string `json:"query" jsonschema:"Gmail search query, same syntax as the Gmail search box, e.g. 'from:alice@digio.es subject:invoice after:2026/09/01 has:attachment is:unread'"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"maximum number of messages to return (default 20, max 100)"`
}

type gmailSearchOutput struct {
	Messages []gmail.MessageSummary `json:"messages" jsonschema:"matching messages, newest first"`
	Count    int                    `json:"count" jsonschema:"number of messages returned"`
}

type gmailGetMessageInput struct {
	MessageID   string `json:"message_id" jsonschema:"Gmail message id (the id field of gmail_search results)"`
	MaxChars    int    `json:"max_chars,omitempty" jsonschema:"maximum characters of body text to return (default 20000); longer bodies are cut and truncated is set"`
	IncludeHTML bool   `json:"include_html,omitempty" jsonschema:"also return the raw HTML body in message.html (usually not needed: body already has the HTML converted to text)"`
}

type gmailGetMessageOutput struct {
	Message   gmail.Message `json:"message" jsonschema:"the message with headers, text body and attachment list"`
	Truncated bool          `json:"truncated" jsonschema:"true when the body (or html) was cut to max_chars"`
}

type gmailGetThreadInput struct {
	ThreadID string `json:"thread_id" jsonschema:"Gmail thread id (the thread_id field of gmail_search results)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"maximum characters of body text for the whole thread (default 20000), spent on messages in chronological order; later bodies are cut or emptied and truncated is set"`
}

type gmailGetThreadOutput struct {
	Thread    gmail.Thread `json:"thread" jsonschema:"the thread with its messages in chronological order"`
	Truncated bool         `json:"truncated" jsonschema:"true when any message body was cut to fit max_chars"`
}

type gmailListLabelsInput struct{}

type gmailListLabelsOutput struct {
	Labels []gmail.Label `json:"labels" jsonschema:"the account's labels (system first, then user labels)"`
}

// registerGmail registers the gmail_* tools. It is called by New only when the
// gmail service is requested and granted.
func registerGmail(s *mcp.Server, deps Deps) {
	addReadOnlyTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_search",
		Description: "Search the user's Gmail messages using Gmail search syntax and return summaries " +
			"(id, thread_id, from, to, subject, date, snippet, labels), newest first. Operators can be combined: " +
			"from:alice@digio.es, to:me, subject:\"weekly report\", after:2026/09/01, before:2026/09/30, newer_than:7d, " +
			"has:attachment, filename:pdf, is:unread, is:starred, label:projects, in:sent, \"exact phrase\", -exclude, OR. " +
			"Use gmail_get_message or gmail_get_thread to read a result's body.",
	}, func(ctx context.Context, in gmailSearchInput) (gmailSearchOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return gmailSearchOutput{}, errors.New("query is required")
		}
		limit := in.MaxResults
		if limit <= 0 {
			limit = gmail.DefaultMaxResults
		}
		limit = min(limit, gmailMaxSearchResults)
		opts, err := deps.ClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmailSearchOutput{}, err
		}
		msgs, err := gmail.Search(ctx, in.Query, gmail.SearchOptions{MaxResults: limit}, opts...)
		if err != nil {
			return gmailSearchOutput{}, err
		}
		return gmailSearchOutput{Messages: msgs, Count: len(msgs)}, nil
	})

	addReadOnlyTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_get_message",
		Description: "Read one Gmail message: headers (from, to, cc, subject, date, message_id), labels, the body as " +
			"plain text (HTML-only emails are converted to text, links kept as 'text (url)') and the attachment list " +
			"(attachment_id, filename, mime_type, size). Long bodies are cut to max_chars.",
	}, func(ctx context.Context, in gmailGetMessageInput) (gmailGetMessageOutput, error) {
		if strings.TrimSpace(in.MessageID) == "" {
			return gmailGetMessageOutput{}, errors.New("message_id is required")
		}
		opts, err := deps.ClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmailGetMessageOutput{}, err
		}
		msg, err := gmail.GetMessage(ctx, in.MessageID, gmail.GetOptions{IncludeHTML: in.IncludeHTML}, opts...)
		if err != nil {
			return gmailGetMessageOutput{}, err
		}
		truncated := truncateEach(in.MaxChars, &msg.Body, &msg.HTML)
		return gmailGetMessageOutput{Message: *msg, Truncated: truncated}, nil
	})

	addReadOnlyTool(s, deps, auth.Gmail, &mcp.Tool{
		Name: "gmail_get_thread",
		Description: "Read a whole Gmail conversation: every message in chronological order with headers, plain-text " +
			"body and attachment list. max_chars bounds the total body text of the thread.",
	}, func(ctx context.Context, in gmailGetThreadInput) (gmailGetThreadOutput, error) {
		if strings.TrimSpace(in.ThreadID) == "" {
			return gmailGetThreadOutput{}, errors.New("thread_id is required")
		}
		opts, err := deps.ClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmailGetThreadOutput{}, err
		}
		th, err := gmail.GetThread(ctx, in.ThreadID, gmail.GetOptions{}, opts...)
		if err != nil {
			return gmailGetThreadOutput{}, err
		}
		truncated := truncateThreadBodies(th, in.MaxChars)
		return gmailGetThreadOutput{Thread: *th, Truncated: truncated}, nil
	})

	addReadOnlyTool(s, deps, auth.Gmail, &mcp.Tool{
		Name:        "gmail_list_labels",
		Description: "List the Gmail labels of the account (id, name, type system|user). Label names can be used in gmail_search as label:<name>.",
	}, func(ctx context.Context, _ gmailListLabelsInput) (gmailListLabelsOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Gmail)
		if err != nil {
			return gmailListLabelsOutput{}, err
		}
		labels, err := gmail.ListLabels(ctx, opts...)
		if err != nil {
			return gmailListLabelsOutput{}, err
		}
		return gmailListLabelsOutput{Labels: labels}, nil
	})
}

// truncateThreadBodies spends a budget of maxChars characters (default
// DefaultMaxChars) on the thread's bodies in order, cutting the one that
// exceeds it and emptying the rest. It reports whether anything was cut.
func truncateThreadBodies(th *gmail.Thread, maxChars int) bool {
	bodies := make([]*string, len(th.Messages))
	for i := range th.Messages {
		bodies[i] = &th.Messages[i].Body
	}
	return truncateShared(maxChars, bodies...)
}
