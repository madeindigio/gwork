package gmail

import "time"

// MessageSummary is a search result: the message headers most useful to
// identify it, without its body.
type MessageSummary struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"thread_id"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Subject  string    `json:"subject"`
	Date     time.Time `json:"date"`
	Snippet  string    `json:"snippet"`
	Labels   []string  `json:"labels"`
}

// Message is a full message with its body rendered as text.
type Message struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"thread_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Cc        string    `json:"cc,omitempty"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
	MessageID string    `json:"message_id,omitempty"`
	Labels    []string  `json:"labels"`
	Snippet   string    `json:"snippet"`
	// Body is the message text: the text/plain parts when present,
	// otherwise the HTML parts converted to text.
	Body string `json:"body"`
	// BodySource says where Body came from: "text/plain", "text/html" or
	// "" when the message has no textual body.
	BodySource string `json:"body_source"`
	// HTML is the raw HTML body; only set when requested with
	// GetOptions.IncludeHTML.
	HTML        string       `json:"html,omitempty"`
	Attachments []Attachment `json:"attachments"`
}

// Attachment describes a file attached to a message. Download it with
// GetAttachment(ctx, messageID, AttachmentID).
type Attachment struct {
	AttachmentID string `json:"attachment_id"`
	Filename     string `json:"filename"`
	MimeType     string `json:"mime_type"`
	Size         int64  `json:"size"`
}

// Thread is a conversation: its messages in chronological order.
type Thread struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// Label is a Gmail label (system or user defined).
type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is "system" or "user".
	Type string `json:"type"`
}
