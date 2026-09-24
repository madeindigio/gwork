package gmail

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// GetOptions tunes GetMessage and GetThread.
type GetOptions struct {
	// IncludeHTML also returns the raw HTML body in Message.HTML.
	IncludeHTML bool
}

// GetMessage fetches a message (format=full) and renders its body as text.
func GetMessage(ctx context.Context, id string, g GetOptions, opts ...option.ClientOption) (*Message, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	m, err := svc.Users.Messages.Get(userID, id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", id, err)
	}
	msg, err := toMessage(m, g, attachmentFetcher(ctx, svc, m.Id))
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// GetThread fetches a thread (format=full) with every message rendered as
// text, in chronological order.
func GetThread(ctx context.Context, id string, g GetOptions, opts ...option.ClientOption) (*Thread, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	t, err := svc.Users.Threads.Get(userID, id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get thread %s: %w", id, err)
	}
	out := &Thread{ID: t.Id, Messages: make([]Message, 0, len(t.Messages))}
	for _, m := range t.Messages {
		msg, err := toMessage(m, g, attachmentFetcher(ctx, svc, m.Id))
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, msg)
	}
	return out, nil
}

// toMessage converts an API message fetched with format=full.
func toMessage(m *gmailapi.Message, g GetOptions, fetch fetchFunc) (Message, error) {
	s := toSummary(m)
	h := headerMap(m.Payload)
	body, source, rawHTML, atts, err := extractBody(m.Payload, g.IncludeHTML, fetch)
	if err != nil {
		return Message{}, fmt.Errorf("message %s: %w", m.Id, err)
	}
	return Message{
		ID:          s.ID,
		ThreadID:    s.ThreadID,
		From:        s.From,
		To:          s.To,
		Cc:          h["cc"],
		Subject:     s.Subject,
		Date:        s.Date,
		MessageID:   h["message-id"],
		Labels:      s.Labels,
		Snippet:     s.Snippet,
		Body:        body,
		BodySource:  source,
		HTML:        rawHTML,
		Attachments: atts,
	}, nil
}

// attachmentFetcher downloads out-of-line body parts of message msgID.
func attachmentFetcher(ctx context.Context, svc *gmailapi.Service, msgID string) fetchFunc {
	return func(attID string) (string, error) {
		b, err := svc.Users.Messages.Attachments.Get(userID, msgID, attID).Context(ctx).Do()
		if err != nil {
			return "", fmt.Errorf("get body of message %s: %w", msgID, err)
		}
		return b.Data, nil
	}
}

// GetAttachment downloads attachment attachmentID of message messageID and
// returns its decoded bytes.
func GetAttachment(ctx context.Context, messageID, attachmentID string, opts ...option.ClientOption) ([]byte, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
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

// ListLabels returns the account's labels: system labels first, then user
// labels, each group sorted by name.
func ListLabels(ctx context.Context, opts ...option.ClientOption) ([]Label, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	resp, err := svc.Users.Labels.List(userID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	out := make([]Label, 0, len(resp.Labels))
	for _, l := range resp.Labels {
		out = append(out, Label{ID: l.Id, Name: l.Name, Type: l.Type})
	}
	slices.SortStableFunc(out, func(a, b Label) int {
		if a.Type != b.Type {
			if a.Type == "system" {
				return -1
			}
			if b.Type == "system" {
				return 1
			}
			return strings.Compare(a.Type, b.Type)
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}
