package gmail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Target selects the message or thread an operation applies to. Exactly one
// of MessageID and ThreadID must be set.
type Target struct {
	MessageID string `json:"message_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
}

// Validate checks that exactly one of MessageID and ThreadID is set.
func (t Target) Validate() error {
	if (t.MessageID == "") == (t.ThreadID == "") {
		return errors.New("exactly one of message_id and thread_id is required")
	}
	return nil
}

// Kind returns "thread" or "message".
func (t Target) Kind() string {
	if t.ThreadID != "" {
		return "thread"
	}
	return "message"
}

// ID returns the message or thread id.
func (t Target) ID() string {
	if t.ThreadID != "" {
		return t.ThreadID
	}
	return t.MessageID
}

// ModifyResult reports the outcome of a label, trash or untrash operation.
type ModifyResult struct {
	// Kind is "message" or "thread".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// ThreadID and LabelIDs (the labels after the change) are only known for
	// messages.
	ThreadID string   `json:"thread_id,omitempty"`
	LabelIDs []string `json:"label_ids"`
	// Added and Removed are the label ids applied by ModifyLabels.
	Added   []string `json:"added_label_ids"`
	Removed []string `json:"removed_label_ids"`
}

// systemLabelIDs are the label ids that need no lookup.
var systemLabelIDs = map[string]bool{
	"INBOX": true, "UNREAD": true, "STARRED": true, "IMPORTANT": true, "SPAM": true, "TRASH": true,
	"SENT": true, "DRAFT": true, "CHAT": true,
	"CATEGORY_PERSONAL": true, "CATEGORY_SOCIAL": true, "CATEGORY_PROMOTIONS": true,
	"CATEGORY_UPDATES": true, "CATEGORY_FORUMS": true,
}

// labelCache loads labels.list at most once.
type labelCache struct {
	labels []*gmailapi.Label
	loaded bool
}

func (c *labelCache) get(ctx context.Context, svc *gmailapi.Service) ([]*gmailapi.Label, error) {
	if !c.loaded {
		resp, err := svc.Users.Labels.List(userID).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("list labels: %w", err)
		}
		c.labels, c.loaded = resp.Labels, true
	}
	return c.labels, nil
}

// resolveLabels turns label ids or names into ids. System label ids are used
// as they are; anything else is looked up (id first, then name, ignoring
// case) with labels.list, which is only called when needed.
func resolveLabels(ctx context.Context, svc *gmailapi.Service, l *labelCache, refs []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		id := ref
		if !systemLabelIDs[ref] {
			labels, err := l.get(ctx, svc)
			if err != nil {
				return nil, err
			}
			var err2 error
			if id, err2 = matchLabel(labels, ref); err2 != nil {
				return nil, err2
			}
			if id == "" {
				return nil, fmt.Errorf("unknown label %q: use a label id or the exact name of an existing label", ref)
			}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// matchLabel finds ref among labels: an id wins, then an exact-case name,
// then a unique case-insensitive name. Several case-insensitive matches are
// ambiguous. It returns "" when nothing matches.
func matchLabel(labels []*gmailapi.Label, ref string) (string, error) {
	for _, l := range labels {
		if l.Id == ref {
			return l.Id, nil
		}
	}
	for _, l := range labels {
		if l.Name == ref {
			return l.Id, nil
		}
	}
	var ids []string
	for _, l := range labels {
		if strings.EqualFold(l.Name, ref) {
			ids = append(ids, l.Id)
		}
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous label %q matches several labels (ids: %s): use the label id or the exact name", ref, strings.Join(ids, ", "))
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", nil
}

// ModifyLabels adds and removes labels (ids or names) on a message or on
// every message of a thread.
func ModifyLabels(ctx context.Context, t Target, add, remove []string, opts ...option.ClientOption) (*ModifyResult, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	cache := &labelCache{}
	addIDs, err := resolveLabels(ctx, svc, cache, add)
	if err != nil {
		return nil, err
	}
	removeIDs, err := resolveLabels(ctx, svc, cache, remove)
	if err != nil {
		return nil, err
	}
	if len(addIDs) == 0 && len(removeIDs) == 0 {
		return nil, errors.New("no labels to add or remove")
	}
	for _, a := range addIDs {
		for _, r := range removeIDs {
			if a == r {
				return nil, fmt.Errorf("label %s is both added and removed", a)
			}
		}
	}
	res := &ModifyResult{Kind: t.Kind(), ID: t.ID(), Added: nonNil(addIDs), Removed: nonNil(removeIDs), LabelIDs: []string{}}
	if t.ThreadID != "" {
		req := &gmailapi.ModifyThreadRequest{AddLabelIds: addIDs, RemoveLabelIds: removeIDs}
		if _, err := svc.Users.Threads.Modify(userID, t.ThreadID, req).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("modify labels of thread %s: %w", t.ThreadID, err)
		}
		return res, nil
	}
	req := &gmailapi.ModifyMessageRequest{AddLabelIds: addIDs, RemoveLabelIds: removeIDs}
	m, err := svc.Users.Messages.Modify(userID, t.MessageID, req).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("modify labels of message %s: %w", t.MessageID, err)
	}
	res.ThreadID, res.LabelIDs = m.ThreadId, nonNil(m.LabelIds)
	return res, nil
}

// Trash moves a message or thread to the Trash.
func Trash(ctx context.Context, t Target, opts ...option.ClientOption) (*ModifyResult, error) {
	return trash(ctx, t, true, opts)
}

// Untrash restores a message or thread from the Trash.
func Untrash(ctx context.Context, t Target, opts ...option.ClientOption) (*ModifyResult, error) {
	return trash(ctx, t, false, opts)
}

func trash(ctx context.Context, t Target, on bool, opts []option.ClientOption) (*ModifyResult, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	verb := "untrash"
	if on {
		verb = "trash"
	}
	res := &ModifyResult{Kind: t.Kind(), ID: t.ID(), LabelIDs: []string{}, Added: []string{}, Removed: []string{}}
	if t.ThreadID != "" {
		if on {
			_, err = svc.Users.Threads.Trash(userID, t.ThreadID).Context(ctx).Do()
		} else {
			_, err = svc.Users.Threads.Untrash(userID, t.ThreadID).Context(ctx).Do()
		}
		if err != nil {
			return nil, fmt.Errorf("%s thread %s: %w", verb, t.ThreadID, err)
		}
		return res, nil
	}
	var m *gmailapi.Message
	if on {
		m, err = svc.Users.Messages.Trash(userID, t.MessageID).Context(ctx).Do()
	} else {
		m, err = svc.Users.Messages.Untrash(userID, t.MessageID).Context(ctx).Do()
	}
	if err != nil {
		return nil, fmt.Errorf("%s message %s: %w", verb, t.MessageID, err)
	}
	res.ThreadID, res.LabelIDs = m.ThreadId, nonNil(m.LabelIds)
	return res, nil
}
