package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	chatapi "google.golang.org/api/chat/v1"

	"github.com/madeindigio/gwork/internal/timeutil"
)

// DefaultMaxMessages is the default number of messages returned by
// ListMessages.
const DefaultMaxMessages = 50

// Message orders accepted by ListMessagesOptions.Order.
const (
	// OrderAsc lists oldest messages first (API default).
	OrderAsc = "asc"
	// OrderDesc lists newest messages first.
	OrderDesc = "desc"
)

// ListMessagesOptions configures ListMessages.
type ListMessagesOptions struct {
	// Window limits messages by creation time; zero bounds are open.
	Window timeutil.Window
	// Thread limits messages to one thread ("spaces/S/threads/T" or bare
	// "T").
	Thread string
	// Max caps the number of messages; <= 0 means DefaultMaxMessages.
	Max int
	// Order is "asc" (oldest first, default) or "desc" (newest first).
	Order string
}

// MessageFilter builds the spaces.messages.list filter for a creation-time
// window and an optional full thread name, e.g.
//
//	createTime > "2026-09-17T12:00:00Z" AND createTime < "2026-09-24T12:00:00Z" AND thread.name = spaces/S/threads/T
//
// The API only supports strict comparisons, so the lower bound is moved
// back by one second to keep [From, To) semantics at second precision.
func MessageFilter(w timeutil.Window, thread string) string {
	var parts []string
	if !w.From.IsZero() {
		parts = append(parts, fmt.Sprintf("createTime > %q", timeutil.FormatRFC3339(w.From.Add(-time.Second))))
	}
	if !w.To.IsZero() {
		parts = append(parts, fmt.Sprintf("createTime < %q", timeutil.FormatRFC3339(w.To)))
	}
	if thread != "" {
		parts = append(parts, "thread.name = "+thread)
	}
	return strings.Join(parts, " AND ")
}

// orderBy maps "asc"/"desc" to the API orderBy value.
func orderBy(order string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "", OrderAsc:
		return "createTime asc", nil
	case OrderDesc:
		return "createTime desc", nil
	}
	return "", fmt.Errorf("invalid order %q: want asc or desc", order)
}

// ListMessages lists messages of space ("spaces/X" or bare "X") within the
// options' window, optionally limited to one thread.
func ListMessages(ctx context.Context, svc *chatapi.Service, space string, o ListMessagesOptions) ([]Message, error) {
	name, err := NormalizeSpace(space)
	if err != nil {
		return nil, err
	}
	thread, err := normalizeThread(name, o.Thread)
	if err != nil {
		return nil, err
	}
	order, err := orderBy(o.Order)
	if err != nil {
		return nil, err
	}
	limit := o.Max
	if limit <= 0 {
		limit = DefaultMaxMessages
	}
	p := messagePager{svc: svc, space: name, filter: MessageFilter(o.Window, thread), orderBy: order}
	out := make([]Message, 0)
	for len(out) < limit {
		msgs, more, err := p.next(ctx, min(maxPageSize, limit-len(out)))
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
		if !more {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// messagePager pages through spaces.messages.list.
type messagePager struct {
	svc     *chatapi.Service
	space   string
	filter  string
	orderBy string
	token   string
}

// next fetches the next page of at most size messages and reports whether
// more pages remain.
func (p *messagePager) next(ctx context.Context, size int) ([]Message, bool, error) {
	call := p.svc.Spaces.Messages.List(p.space).PageSize(int64(size)).OrderBy(p.orderBy).Context(ctx)
	if p.filter != "" {
		call = call.Filter(p.filter)
	}
	if p.token != "" {
		call = call.PageToken(p.token)
	}
	resp, err := call.Do()
	if err != nil {
		return nil, false, fmt.Errorf("list messages of %s: %w", p.space, err)
	}
	out := make([]Message, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		if m != nil {
			out = append(out, convertMessage(m))
		}
	}
	p.token = resp.NextPageToken
	return out, p.token != "", nil
}

// GetMessage returns one message by resource name
// ("spaces/{space}/messages/{message}").
func GetMessage(ctx context.Context, svc *chatapi.Service, name string) (Message, error) {
	name = strings.TrimSpace(name)
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "spaces" || parts[1] == "" || parts[2] != "messages" || parts[3] == "" {
		return Message{}, fmt.Errorf("invalid message name %q: want spaces/{space}/messages/{message}", name)
	}
	m, err := svc.Spaces.Messages.Get(name).Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("get chat message %s: %w", name, err)
	}
	return convertMessage(m), nil
}
