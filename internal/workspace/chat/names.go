package chat

import (
	"context"
	"fmt"
	"sync"

	chatapi "google.golang.org/api/chat/v1"
)

// maxMembersPerSpace bounds how many memberships the resolver reads per
// space.
const maxMembersPerSpace = 5000

// NameResolver fills in missing sender display names from space
// memberships (spaces.members.list, chat.memberships.readonly).
//
// With user authentication the Chat API usually returns only the sender's
// resource name ("users/123") and type, not the display name. Membership
// listings sometimes carry display names; when they do not, or when the
// listing fails (e.g. 403), the resolver degrades silently and senders keep
// their resource names. Results are cached per space for the resolver's
// lifetime (one CLI invocation or tool call). It is safe for concurrent use.
type NameResolver struct {
	svc *chatapi.Service

	mu     sync.Mutex
	spaces map[string]*spaceNames
}

// spaceNames is the lazily loaded member directory of one space.
type spaceNames struct {
	once  sync.Once
	names map[string]string
	err   error
}

// NewNameResolver returns a resolver using svc.
func NewNameResolver(svc *chatapi.Service) *NameResolver {
	return &NameResolver{svc: svc, spaces: map[string]*spaceNames{}}
}

// Resolve sets Sender.DisplayName on every message that lacks one and whose
// sender is a known member of the message's space. It never fails; spaces
// whose memberships cannot be read are left unchanged.
func (r *NameResolver) Resolve(ctx context.Context, msgs []Message) {
	for i := range msgs {
		m := &msgs[i]
		if m.Sender.DisplayName != "" || m.Sender.Name == "" || m.Space == "" {
			continue
		}
		if name := r.lookup(ctx, m.Space, m.Sender.Name); name != "" {
			m.Sender.DisplayName = name
		}
	}
}

// Err returns the error met while loading space's memberships, if any.
// It is nil for spaces never looked up.
func (r *NameResolver) Err(space string) error {
	r.mu.Lock()
	sn := r.spaces[space]
	r.mu.Unlock()
	if sn == nil {
		return nil
	}
	return sn.err
}

func (r *NameResolver) lookup(ctx context.Context, space, user string) string {
	r.mu.Lock()
	sn, ok := r.spaces[space]
	if !ok {
		sn = &spaceNames{}
		r.spaces[space] = sn
	}
	r.mu.Unlock()
	sn.once.Do(func() {
		sn.names, sn.err = r.load(ctx, space)
	})
	return sn.names[user]
}

// load reads the display names of the members of space.
func (r *NameResolver) load(ctx context.Context, space string) (map[string]string, error) {
	names := map[string]string{}
	token := ""
	seen := 0
	for seen < maxMembersPerSpace {
		call := r.svc.Spaces.Members.List(space).PageSize(maxPageSize).Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Do()
		if err != nil {
			return names, fmt.Errorf("list members of %s: %w", space, err)
		}
		for _, m := range resp.Memberships {
			seen++
			if m == nil || m.Member == nil || m.Member.Name == "" || m.Member.DisplayName == "" {
				continue
			}
			names[m.Member.Name] = m.Member.DisplayName
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return names, nil
}
