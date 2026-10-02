package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	chatapi "google.golang.org/api/chat/v1"

	"github.com/madeindigio/gwork/internal/timeutil"
)

// DefaultMaxUnreadPerSpace is the default number of unread messages
// returned per space by ListUnread.
const DefaultMaxUnreadPerSpace = 20

// UnreadOptions configures ListUnread.
type UnreadOptions struct {
	// Spaces restricts the check ("spaces/X" or bare "X"). Empty checks
	// every space the user is a member of (see Type and Section).
	Spaces []string
	// Type limits the checked spaces to one type ("space", "group", "dm");
	// ignored when Spaces is set.
	Type string
	// Section limits the checked spaces to one sidebar section (see
	// FindSection); ignored when Spaces is set.
	Section string
	// MaxPerSpace caps the unread messages returned per space; <= 0 means
	// DefaultMaxUnreadPerSpace.
	MaxPerSpace int
	// Concurrency is the number of spaces checked in parallel; <= 0 means
	// DefaultSearchConcurrency.
	Concurrency int
}

// UnreadSpace is a space with messages newer than the user's read position.
type UnreadSpace struct {
	// Space is the space holding the unread messages.
	Space Space `json:"space"`
	// LastReadTime is the user's read position; zero when the user never
	// read the space.
	LastReadTime time.Time `json:"last_read_time,omitzero"`
	// Messages are the unread messages, newest first, capped at MaxPerSpace.
	Messages []Message `json:"messages"`
	// More is true when the space has more unread messages than returned.
	More bool `json:"more"`
}

// UnreadResult is the outcome of ListUnread.
type UnreadResult struct {
	// Spaces are the spaces with unread messages, most recent message first.
	Spaces []UnreadSpace `json:"spaces"`
	// SpacesChecked is the number of spaces whose read state was read.
	SpacesChecked int `json:"spaces_checked"`
	// SpacesTotal is the number of spaces selected for the check.
	SpacesTotal int `json:"spaces_total"`
	// FailedSpaces lists spaces that could not be checked.
	FailedSpaces []SpaceError `json:"failed_spaces"`
}

// GetSpaceReadState returns the time up to which the user has read space
// ("spaces/X" or bare "X"); zero when the user never read it. It needs the
// chat.users.readstate.readonly scope.
func GetSpaceReadState(ctx context.Context, svc *chatapi.Service, space string) (time.Time, error) {
	name, err := NormalizeSpace(space)
	if err != nil {
		return time.Time{}, err
	}
	rs, err := svc.Users.Spaces.GetSpaceReadState("users/me/" + name + "/spaceReadState").Context(ctx).Do()
	if err != nil {
		return time.Time{}, fmt.Errorf("get read state of %s: %w", name, err)
	}
	return parseTime(rs.LastReadTime), nil
}

// ListUnread returns the messages created after the user's read position in
// each selected space.
//
// The Chat API has no unread flag or counter: this reads the read state of
// every selected space (Concurrency spaces in parallel) and lists the
// messages created after it. Spaces whose last activity is not newer than
// the read position are skipped without listing messages. Read positions of
// individual threads are not considered.
func ListUnread(ctx context.Context, svc *chatapi.Service, o UnreadOptions) (UnreadResult, error) {
	perSpace := o.MaxPerSpace
	if perSpace <= 0 {
		perSpace = DefaultMaxUnreadPerSpace
	}
	workers := o.Concurrency
	if workers <= 0 {
		workers = DefaultSearchConcurrency
	}
	spaces, err := unreadSpaces(ctx, svc, o)
	if err != nil {
		return UnreadResult{}, err
	}
	res := UnreadResult{Spaces: make([]UnreadSpace, 0), SpacesTotal: len(spaces), FailedSpaces: make([]SpaceError, 0)}
	if len(spaces) == 0 {
		return res, nil
	}

	c := &unreadCheck{perSpace: perSpace}
	jobs := make(chan Space)
	var wg sync.WaitGroup
	for range min(workers, len(spaces)) {
		wg.Go(func() {
			for sp := range jobs {
				c.space(ctx, svc, sp)
			}
		})
	}
	for _, sp := range spaces {
		if ctx.Err() != nil {
			break
		}
		jobs <- sp
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return UnreadResult{}, fmt.Errorf("list unread chat messages: %w", err)
	}
	if c.checked == 0 && len(c.failed) > 0 {
		return UnreadResult{}, fmt.Errorf("list unread chat messages: %w", c.firstErr)
	}

	slices.SortFunc(c.unread, func(a, b UnreadSpace) int {
		if d := b.Messages[0].CreateTime.Compare(a.Messages[0].CreateTime); d != 0 {
			return d
		}
		return strings.Compare(a.Space.Name, b.Space.Name)
	})
	slices.SortFunc(c.failed, func(a, b SpaceError) int { return strings.Compare(a.Space, b.Space) })
	res.Spaces = append(res.Spaces, c.unread...)
	res.SpacesChecked = c.checked
	res.FailedSpaces = append(res.FailedSpaces, c.failed...)
	return res, nil
}

// unreadSpaces returns the spaces to check. Spaces given explicitly are
// fetched one by one so they carry their display name and last activity.
func unreadSpaces(ctx context.Context, svc *chatapi.Service, o UnreadOptions) ([]Space, error) {
	if len(o.Spaces) == 0 {
		if strings.TrimSpace(o.Section) != "" {
			return listSectionSpaces(ctx, svc, o.Section, o.Type)
		}
		return listSpaces(ctx, svc, o.Type, 0)
	}
	names, err := searchSpaces(ctx, svc, o.Spaces)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0, len(names))
	for _, name := range names {
		sp, err := GetSpace(ctx, svc, name)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, nil
}

// unreadCheck is the shared state of one ListUnread call.
type unreadCheck struct {
	perSpace int

	mu       sync.Mutex
	unread   []UnreadSpace
	checked  int
	failed   []SpaceError
	firstErr error
}

// space checks one space and records its unread messages, if any.
func (c *unreadCheck) space(ctx context.Context, svc *chatapi.Service, sp Space) {
	u, err := unreadIn(ctx, svc, sp, c.perSpace)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.firstErr == nil {
			c.firstErr = err
		}
		c.failed = append(c.failed, SpaceError{Space: sp.Name, Error: err.Error()})
		return
	}
	c.checked++
	if len(u.Messages) > 0 {
		c.unread = append(c.unread, u)
	}
}

// unreadIn returns the messages of sp created after the user's read
// position, newest first and capped at limit.
func unreadIn(ctx context.Context, svc *chatapi.Service, sp Space, limit int) (UnreadSpace, error) {
	lastRead, err := GetSpaceReadState(ctx, svc, sp.Name)
	if err != nil {
		return UnreadSpace{}, err
	}
	out := UnreadSpace{Space: sp, LastReadTime: lastRead, Messages: make([]Message, 0)}
	if !sp.LastActiveTime.IsZero() && !sp.LastActiveTime.After(lastRead) {
		return out, nil
	}
	// The filter has second precision, so it may also match messages read
	// within the read position's second; those are dropped below.
	p := messagePager{svc: svc, space: sp.Name, filter: unreadFilter(lastRead), orderBy: "createTime desc"}
	for {
		msgs, more, err := p.next(ctx, min(maxPageSize, limit+1))
		if err != nil {
			return UnreadSpace{}, err
		}
		for _, m := range msgs {
			if !m.CreateTime.After(lastRead) {
				// Newest first: everything from here on is already read.
				return out, nil
			}
			if len(out.Messages) == limit {
				out.More = true
				return out, nil
			}
			out.Messages = append(out.Messages, m)
		}
		if !more {
			return out, nil
		}
	}
}

// unreadFilter builds the spaces.messages.list filter for messages created
// after lastRead; empty when the user never read the space.
func unreadFilter(lastRead time.Time) string {
	if lastRead.IsZero() {
		return ""
	}
	return fmt.Sprintf("createTime > %q", timeutil.FormatRFC3339(lastRead.Truncate(time.Second).UTC()))
}
