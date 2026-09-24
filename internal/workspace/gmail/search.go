package gmail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	// DefaultMaxResults is the default number of messages returned by Search.
	DefaultMaxResults = 20
	// maxPageSize is the largest page size accepted by messages.list.
	maxPageSize = 500
	// metadataConcurrency bounds the parallel metadata requests of Search.
	metadataConcurrency = 8
	// userID is the special Gmail user id for the authenticated account.
	userID = "me"
)

// summaryHeaders are the headers requested for search results.
var summaryHeaders = []string{"From", "To", "Subject", "Date"}

// SearchOptions tunes Search.
type SearchOptions struct {
	// MaxResults caps the number of messages; <= 0 means DefaultMaxResults.
	MaxResults int
	// IncludeSpamTrash also searches the SPAM and TRASH folders.
	IncludeSpamTrash bool
}

// Search lists the messages matching query (Gmail search syntax, e.g.
// "from:alice subject:report after:2026/09/01 has:attachment is:unread")
// and fetches their metadata. Results keep the order returned by Gmail
// (newest first). Messages deleted between the listing and the metadata
// request are skipped; rate-limit and server errors on a metadata request
// are retried a few times before Search fails.
func Search(ctx context.Context, query string, so SearchOptions, opts ...option.ClientOption) ([]MessageSummary, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return search(ctx, svc, query, so)
}

func search(ctx context.Context, svc *gmailapi.Service, query string, so SearchOptions) ([]MessageSummary, error) {
	limit := so.MaxResults
	if limit <= 0 {
		limit = DefaultMaxResults
	}

	var refs []*gmailapi.Message
	pageToken := ""
	for len(refs) < limit {
		call := svc.Users.Messages.List(userID).
			Q(query).
			IncludeSpamTrash(so.IncludeSpamTrash).
			MaxResults(int64(min(limit-len(refs), maxPageSize))).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("search messages %q: %w", query, err)
		}
		refs = append(refs, resp.Messages...)
		pageToken = resp.NextPageToken
		if pageToken == "" || len(resp.Messages) == 0 {
			break
		}
	}
	if len(refs) > limit {
		refs = refs[:limit]
	}

	found := make([]*MessageSummary, len(refs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(metadataConcurrency)
	for i, ref := range refs {
		g.Go(func() error {
			m, err := getMetadata(gctx, svc, ref.Id)
			if isStatus(err, http.StatusNotFound) {
				// The message was deleted between list and get: skip it.
				return nil
			}
			if err != nil {
				return fmt.Errorf("get message %s metadata: %w", ref.Id, err)
			}
			s := toSummary(m)
			found[i] = &s
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	out := make([]MessageSummary, 0, len(found))
	for _, s := range found {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

// metadataAttempts is how many times a metadata request is tried when
// Google answers 429 or 5xx.
const metadataAttempts = 3

// retryBaseDelay is the first backoff delay; it doubles on each retry.
// Tests lower it.
var retryBaseDelay = 250 * time.Millisecond

// getMetadata fetches the summary headers of message id, retrying
// transient failures (429 and 5xx) with exponential backoff.
func getMetadata(ctx context.Context, svc *gmailapi.Service, id string) (*gmailapi.Message, error) {
	delay := retryBaseDelay
	for attempt := 1; ; attempt++ {
		m, err := svc.Users.Messages.Get(userID, id).
			Format("metadata").
			MetadataHeaders(summaryHeaders...).
			Context(ctx).
			Do()
		if err == nil || attempt == metadataAttempts || !isTransient(err) {
			return m, err
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, err
		case <-t.C:
		}
		delay *= 2
	}
}

// isStatus reports whether err is a Google API error with HTTP status code.
func isStatus(err error, code int) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == code
}

// isTransient reports whether err is a rate limit or server error worth
// retrying.
func isTransient(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && (ge.Code == http.StatusTooManyRequests || ge.Code >= 500)
}

// toSummary converts an API message fetched with format=metadata or full.
func toSummary(m *gmailapi.Message) MessageSummary {
	h := headerMap(m.Payload)
	return MessageSummary{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		From:     h["from"],
		To:       h["to"],
		Subject:  h["subject"],
		Date:     messageDate(h["date"], m.InternalDate),
		Snippet:  m.Snippet,
		Labels:   nonNil(m.LabelIds),
	}
}

// headerMap returns the top-level headers of p keyed by lower-case name.
// The first occurrence of a header wins.
func headerMap(p *gmailapi.MessagePart) map[string]string {
	h := map[string]string{}
	if p == nil {
		return h
	}
	for _, hd := range p.Headers {
		k := strings.ToLower(hd.Name)
		if _, ok := h[k]; !ok {
			h[k] = hd.Value
		}
	}
	return h
}

// messageDate parses the Date header, falling back to Gmail's internal
// date (milliseconds since the epoch). The result is in UTC.
func messageDate(header string, internalMillis int64) time.Time {
	if header != "" {
		if t, err := mail.ParseDate(header); err == nil {
			return t.UTC()
		}
	}
	if internalMillis > 0 {
		return time.UnixMilli(internalMillis).UTC()
	}
	return time.Time{}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
