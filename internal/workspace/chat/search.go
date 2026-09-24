package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	chatapi "google.golang.org/api/chat/v1"

	"github.com/digio/gwork-cli/internal/timeutil"
)

// Search defaults.
const (
	// DefaultSearchMax is the default number of matches returned.
	DefaultSearchMax = 50
	// DefaultMaxScan is the default number of messages scanned.
	DefaultMaxScan = 2000
	// DefaultSearchConcurrency is the number of spaces scanned in parallel.
	DefaultSearchConcurrency = 4
	// searchPageSize is the page size used while scanning, small enough to
	// share the scan budget among spaces.
	searchPageSize = 500
)

// SearchOptions configures SearchMessages.
type SearchOptions struct {
	// Text is matched case-insensitively; with several words, all must
	// appear in the message (in any order).
	Text string
	// Spaces restricts the search ("spaces/X" or bare "X"). Empty searches
	// every space the user is a member of.
	Spaces []string
	// Window limits messages by creation time.
	Window timeutil.Window
	// Max caps the matches returned; <= 0 means DefaultSearchMax.
	Max int
	// MaxScan caps the messages scanned overall; <= 0 means DefaultMaxScan.
	MaxScan int
	// Concurrency is the number of spaces scanned in parallel; <= 0 means
	// DefaultSearchConcurrency.
	Concurrency int
}

// SpaceError reports a space that could not be scanned.
type SpaceError struct {
	// Space is the space resource name.
	Space string `json:"space"`
	// Error is the failure message.
	Error string `json:"error"`
}

// SearchResult is the outcome of SearchMessages.
type SearchResult struct {
	// Matches are the matching messages, newest first (ties by name),
	// capped at Max.
	Matches []Message `json:"matches"`
	// TotalMatches counts every match found, including those beyond Max.
	TotalMatches int `json:"total_matches"`
	// Scanned is the number of messages examined.
	Scanned int `json:"scanned"`
	// SpacesScanned is the number of spaces read successfully (fully or
	// partially).
	SpacesScanned int `json:"spaces_scanned"`
	// SpacesTotal is the number of spaces selected for the search.
	SpacesTotal int `json:"spaces_total"`
	// CapReached is true when MaxScan stopped the scan before every
	// selected message was examined; results may be incomplete.
	CapReached bool `json:"cap_reached"`
	// FailedSpaces lists spaces that could not be read.
	FailedSpaces []SpaceError `json:"failed_spaces"`
}

// SearchMessages finds messages containing Text.
//
// Google Chat has no server-side full-text search for users: this lists the
// messages of each selected space inside the time window (newest first,
// scanning up to Concurrency spaces in parallel) and filters them
// client-side, stopping once MaxScan messages have been examined. Narrow
// the window or the spaces for complete results in busy accounts.
func SearchMessages(ctx context.Context, svc *chatapi.Service, o SearchOptions) (SearchResult, error) {
	terms := strings.Fields(strings.ToLower(o.Text))
	if len(terms) == 0 {
		return SearchResult{}, errors.New("search text is empty")
	}
	limit := o.Max
	if limit <= 0 {
		limit = DefaultSearchMax
	}
	maxScan := o.MaxScan
	if maxScan <= 0 {
		maxScan = DefaultMaxScan
	}
	workers := o.Concurrency
	if workers <= 0 {
		workers = DefaultSearchConcurrency
	}

	spaces, err := searchSpaces(ctx, svc, o.Spaces)
	if err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{Matches: make([]Message, 0), SpacesTotal: len(spaces), FailedSpaces: make([]SpaceError, 0)}
	if len(spaces) == 0 {
		return res, nil
	}

	s := &scan{budget: newBudget(maxScan), terms: terms, filter: MessageFilter(o.Window, "")}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(workers, len(spaces)) {
		wg.Go(func() {
			for space := range jobs {
				s.space(ctx, svc, space)
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
		return SearchResult{}, fmt.Errorf("search chat messages: %w", err)
	}
	if s.okSpaces == 0 && len(s.failed) > 0 {
		return SearchResult{}, fmt.Errorf("search chat messages: %w", s.firstErr)
	}

	slices.SortFunc(s.matches, func(a, b Message) int {
		if c := b.CreateTime.Compare(a.CreateTime); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(s.failed, func(a, b SpaceError) int { return strings.Compare(a.Space, b.Space) })
	res.TotalMatches = len(s.matches)
	res.Matches = append(res.Matches, s.matches[:min(limit, len(s.matches))]...)
	res.Scanned = maxScan - s.budget.remaining
	res.SpacesScanned = s.okSpaces
	res.CapReached = s.budget.exhausted
	res.FailedSpaces = append(res.FailedSpaces, s.failed...)
	return res, nil
}

// searchSpaces returns the normalized, de-duplicated spaces to scan.
func searchSpaces(ctx context.Context, svc *chatapi.Service, given []string) ([]string, error) {
	if len(given) == 0 {
		all, err := listSpaces(ctx, svc, "", 0)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(all))
		for _, sp := range all {
			out = append(out, sp.Name)
		}
		return out, nil
	}
	out := make([]string, 0, len(given))
	for _, g := range given {
		name, err := NormalizeSpace(g)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// scan is the shared state of one search.
type scan struct {
	budget *budget
	terms  []string
	filter string

	mu       sync.Mutex
	matches  []Message
	okSpaces int
	failed   []SpaceError
	firstErr error
}

// space scans one space until it is exhausted or the budget runs out.
func (s *scan) space(ctx context.Context, svc *chatapi.Service, space string) {
	p := messagePager{svc: svc, space: space, filter: s.filter, orderBy: "createTime desc"}
	ok := false
	for {
		n := s.budget.reserve(searchPageSize)
		if n == 0 {
			// Budget spent while this space still had messages to read.
			s.budget.markExhausted()
			break
		}
		msgs, more, err := p.next(ctx, n)
		if len(msgs) > n {
			// The server ignored pageSize: never scan beyond the budget.
			msgs, more = msgs[:n], true
		}
		s.budget.release(n - len(msgs))
		if err != nil {
			s.fail(space, err)
			return
		}
		ok = true
		s.collect(msgs)
		if !more {
			break
		}
	}
	if ok {
		s.mu.Lock()
		s.okSpaces++
		s.mu.Unlock()
	}
}

func (s *scan) collect(msgs []Message) {
	var hits []Message
	for _, m := range msgs {
		if matches(m, s.terms) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		return
	}
	s.mu.Lock()
	s.matches = append(s.matches, hits...)
	s.mu.Unlock()
}

func (s *scan) fail(space string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstErr == nil {
		s.firstErr = err
	}
	s.failed = append(s.failed, SpaceError{Space: space, Error: err.Error()})
}

// matches reports whether every term appears in the message text or in an
// attachment name (case-insensitive).
func matches(m Message, terms []string) bool {
	var b strings.Builder
	b.WriteString(m.Text)
	for _, a := range m.Attachments {
		b.WriteByte('\n')
		b.WriteString(a.ContentName)
	}
	hay := strings.ToLower(b.String())
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// budget is the shared message-scan allowance. Pages reserve up to their
// size before the request and release what they did not use, so the total
// scanned never exceeds the cap even with concurrent workers.
type budget struct {
	mu        sync.Mutex
	cond      *sync.Cond
	remaining int
	inflight  int
	exhausted bool
}

func newBudget(n int) *budget {
	b := &budget{remaining: n}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// reserve takes up to n units. While the allowance is zero but other
// requests are in flight it waits, since they may give units back. It
// returns 0 when the budget is definitely spent.
func (b *budget) reserve(n int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.remaining == 0 && b.inflight > 0 {
		b.cond.Wait()
	}
	got := min(n, b.remaining)
	if got > 0 {
		b.remaining -= got
		b.inflight++
	}
	return got
}

// release returns unused units of a reservation and ends it.
func (b *budget) release(unused int) {
	b.mu.Lock()
	b.remaining += unused
	b.inflight--
	b.mu.Unlock()
	b.cond.Broadcast()
}

func (b *budget) markExhausted() {
	b.mu.Lock()
	b.exhausted = true
	b.mu.Unlock()
}
