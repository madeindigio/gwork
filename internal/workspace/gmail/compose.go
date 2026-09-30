package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// ComposeInput describes an outgoing message (a draft or a message to send).
type ComposeInput struct {
	// To, Cc and Bcc are addresses ("a@b.com" or "Name <a@b.com>").
	To  []string `json:"to,omitempty"`
	Cc  []string `json:"cc,omitempty"`
	Bcc []string `json:"bcc,omitempty"`
	// Subject is the subject line. On a reply it defaults to "Re: <original>".
	Subject string `json:"subject,omitempty"`
	// Body is the plain-text UTF-8 body.
	Body string `json:"body,omitempty"`
	// ReplyToMessageID makes the message a reply to that message: it joins
	// its thread and gets In-Reply-To/References headers. When To is empty
	// the recipient defaults to the original Reply-To or From.
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	// ReplyAll also addresses the original To and Cc recipients (except the
	// account itself). Only valid with ReplyToMessageID.
	ReplyAll bool `json:"reply_all,omitempty"`
}

// DraftResult identifies a created draft.
type DraftResult struct {
	DraftID   string   `json:"draft_id"`
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id"`
	LabelIDs  []string `json:"label_ids"`
}

// SendResult identifies a sent message.
type SendResult struct {
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id"`
	LabelIDs  []string `json:"label_ids"`
}

// composed is a built message ready for the API.
type composed struct {
	raw      []byte
	threadID string
	nTo      int // number of recipients in To, Cc and Bcc
	rcpt     Recipients
}

// Recipients are the final addresses and subject of a message to be sent.
type Recipients struct {
	To      []string `json:"to"`
	Cc      []string `json:"cc"`
	Bcc     []string `json:"bcc"`
	Subject string   `json:"subject"`
}

// addrStrings renders addresses for display: "Name <addr>" or "addr".
func addrStrings(l []*mail.Address) []string {
	out := make([]string, len(l))
	for i, a := range l {
		if a.Name == "" {
			out[i] = a.Address
		} else {
			out[i] = a.Name + " <" + a.Address + ">"
		}
	}
	return out
}

// ResolveRecipients returns the recipients and subject that CreateDraft or
// SendMessage would use for in, sharing their logic. On a reply it reads the
// original message and the account profile; it never writes.
func ResolveRecipients(ctx context.Context, in ComposeInput, opts ...option.ClientOption) (*Recipients, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	c, err := compose(ctx, svc, in, time.Now())
	if err != nil {
		return nil, err
	}
	r := c.rcpt
	return &r, nil
}

// DraftRecipients returns the recipients and subject of an existing draft.
func DraftRecipients(ctx context.Context, draftID string, opts ...option.ClientOption) (*Recipients, error) {
	if strings.TrimSpace(draftID) == "" {
		return nil, errors.New("draft id is required")
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	d, err := svc.Users.Drafts.Get(userID, draftID).Format("metadata").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get draft %s: %w", draftID, err)
	}
	r := &Recipients{To: []string{}, Cc: []string{}, Bcc: []string{}}
	if d.Message != nil {
		h := headerMap(d.Message.Payload)
		r.To, r.Cc, r.Bcc = nonNil(addrStrings(parseList(h["to"]))), nonNil(addrStrings(parseList(h["cc"]))), nonNil(addrStrings(parseList(h["bcc"])))
		r.Subject = decodeHeader(oneLine(h["subject"]))
	}
	return r, nil
}

// CreateDraft builds the message described by in and saves it as a draft.
func CreateDraft(ctx context.Context, in ComposeInput, opts ...option.ClientOption) (*DraftResult, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	c, err := compose(ctx, svc, in, time.Now())
	if err != nil {
		return nil, err
	}
	d, err := svc.Users.Drafts.Create(userID, &gmailapi.Draft{Message: c.message()}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}
	res := &DraftResult{DraftID: d.Id, LabelIDs: []string{}}
	if d.Message != nil {
		res.MessageID, res.ThreadID = d.Message.Id, d.Message.ThreadId
		res.LabelIDs = nonNil(d.Message.LabelIds)
	}
	return res, nil
}

// SendDraft sends an existing draft.
func SendDraft(ctx context.Context, draftID string, opts ...option.ClientOption) (*SendResult, error) {
	if strings.TrimSpace(draftID) == "" {
		return nil, errors.New("draft id is required")
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	m, err := svc.Users.Drafts.Send(userID, &gmailapi.Draft{Id: draftID}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("send draft %s: %w", draftID, err)
	}
	return toSendResult(m), nil
}

// SendMessage builds the message described by in and sends it. It requires
// at least one recipient.
func SendMessage(ctx context.Context, in ComposeInput, opts ...option.ClientOption) (*SendResult, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	c, err := compose(ctx, svc, in, time.Now())
	if err != nil {
		return nil, err
	}
	if c.nTo == 0 {
		return nil, errors.New("no recipients: set to, cc or bcc")
	}
	m, err := svc.Users.Messages.Send(userID, c.message()).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	return toSendResult(m), nil
}

func toSendResult(m *gmailapi.Message) *SendResult {
	return &SendResult{MessageID: m.Id, ThreadID: m.ThreadId, LabelIDs: nonNil(m.LabelIds)}
}

func (c *composed) message() *gmailapi.Message {
	return &gmailapi.Message{
		Raw:      base64.RawURLEncoding.EncodeToString(c.raw),
		ThreadId: c.threadID,
	}
}

// replyContext is what a reply needs from the original message.
type replyContext struct {
	threadID   string
	messageID  string
	references string
	subject    string
	replyTo    []*mail.Address // Reply-To, or From when absent
	to, cc     []*mail.Address
}

// compose validates in, fetches the original message on a reply and builds
// the RFC 5322 message.
func compose(ctx context.Context, svc *gmailapi.Service, in ComposeInput, now time.Time) (*composed, error) {
	if in.ReplyAll && in.ReplyToMessageID == "" {
		return nil, errors.New("reply_all requires a message to reply to")
	}
	var (
		rc   *replyContext
		self string
	)
	if in.ReplyToMessageID != "" {
		var err error
		if rc, err = fetchReplyContext(ctx, svc, in.ReplyToMessageID); err != nil {
			return nil, err
		}
		p, err := svc.Users.GetProfile(userID).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("get profile: %w", err)
		}
		self = p.EmailAddress
	}
	return buildMessage(in, rc, self, now)
}

func fetchReplyContext(ctx context.Context, svc *gmailapi.Service, id string) (*replyContext, error) {
	m, err := svc.Users.Messages.Get(userID, id).Format("metadata").
		MetadataHeaders("Message-ID", "References", "Subject", "From", "Reply-To", "To", "Cc").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get message %s to reply to: %w", id, err)
	}
	h := headerMap(m.Payload)
	rc := &replyContext{
		threadID:   m.ThreadId,
		messageID:  oneLine(h["message-id"]),
		references: oneLine(h["references"]),
		subject:    decodeHeader(h["subject"]),
	}
	rc.replyTo = parseList(h["reply-to"])
	if len(rc.replyTo) == 0 {
		rc.replyTo = parseList(h["from"])
	}
	rc.to = parseList(h["to"])
	rc.cc = parseList(h["cc"])
	return rc, nil
}

// parseList parses an address list header leniently: unparsable input yields
// no addresses.
func parseList(s string) []*mail.Address {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	l, err := mail.ParseAddressList(s)
	if err != nil {
		return nil
	}
	return l
}

func decodeHeader(s string) string {
	d, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		return s
	}
	return d
}

// oneLine collapses all whitespace, including CR and LF, to single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// checkHeaderValue rejects values that could inject headers.
func checkHeaderValue(what, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("%s must not contain line breaks", what)
	}
	return nil
}

// parseAddresses validates every entry with net/mail.
func parseAddresses(what string, in []string) ([]*mail.Address, error) {
	var out []*mail.Address
	for _, s := range in {
		if err := checkHeaderValue(what+" address", s); err != nil {
			return nil, err
		}
		a, err := mail.ParseAddress(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("invalid %s address %q: %w", what, s, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func formatList(l []*mail.Address) string {
	parts := make([]string, len(l))
	for i, a := range l {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}

// hasRePrefix reports whether s already starts with "Re:" (case-insensitive).
func hasRePrefix(s string) bool {
	return len(s) >= 3 && strings.EqualFold(s[:3], "re:")
}

// buildMessage produces the RFC 5322 message. rc is the original message on a
// reply; self is the account address, excluded from replies.
func buildMessage(in ComposeInput, rc *replyContext, self string, now time.Time) (*composed, error) {
	to, err := parseAddresses("to", in.To)
	if err != nil {
		return nil, err
	}
	cc, err := parseAddresses("cc", in.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := parseAddresses("bcc", in.Bcc)
	if err != nil {
		return nil, err
	}
	if err := checkHeaderValue("subject", in.Subject); err != nil {
		return nil, err
	}
	isSelf := func(a *mail.Address) bool { return self != "" && strings.EqualFold(a.Address, self) }

	subject := in.Subject
	if rc != nil {
		if len(to) == 0 {
			for _, a := range rc.replyTo {
				if !isSelf(a) {
					to = append(to, a)
				}
			}
			if len(to) == 0 { // replying to a message sent by the account
				for _, a := range rc.to {
					if !isSelf(a) {
						to = append(to, a)
					}
				}
			}
			if len(to) == 0 && len(rc.replyTo) > 0 { // a note to self: reply to self, as Gmail does
				to = append(to, rc.replyTo[0])
			}
		}
		if in.ReplyAll {
			orig := append(append([]*mail.Address{}, rc.to...), rc.cc...)
			for _, a := range orig {
				if !isSelf(a) {
					cc = append(cc, a)
				}
			}
		}
		if strings.TrimSpace(subject) == "" {
			subject = oneLine(rc.subject)
			if !hasRePrefix(subject) {
				subject = "Re: " + subject
			}
		}
	}

	// Deduplicate across the three lists: To first, then Cc, then Bcc.
	seen := map[string]bool{}
	var lists [3][]*mail.Address
	for i, l := range [][]*mail.Address{to, cc, bcc} {
		for _, a := range l {
			k := strings.ToLower(a.Address)
			if !seen[k] {
				seen[k] = true
				lists[i] = append(lists[i], a)
			}
		}
	}

	var buf bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	hdr("MIME-Version", "1.0")
	hdr("Date", now.Format(time.RFC1123Z))
	if len(lists[0]) > 0 {
		hdr("To", formatList(lists[0]))
	}
	if len(lists[1]) > 0 {
		hdr("Cc", formatList(lists[1]))
	}
	if len(lists[2]) > 0 {
		hdr("Bcc", formatList(lists[2]))
	}
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	threadID := ""
	if rc != nil {
		threadID = rc.threadID
		if rc.messageID != "" {
			hdr("In-Reply-To", rc.messageID)
			hdr("References", strings.TrimSpace(rc.references+" "+rc.messageID))
		}
	}
	hdr("Content-Type", "text/plain; charset=UTF-8")
	hdr("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")
	qw := quotedprintable.NewWriter(&buf)
	if _, err := qw.Write([]byte(in.Body)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qw.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return &composed{
		raw: buf.Bytes(), threadID: threadID, nTo: len(lists[0]) + len(lists[1]) + len(lists[2]),
		rcpt: Recipients{To: addrStrings(lists[0]), Cc: addrStrings(lists[1]), Bcc: addrStrings(lists[2]), Subject: subject},
	}, nil
}
