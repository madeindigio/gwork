package chat

import (
	"errors"
	"fmt"
	"strings"
	"time"

	chatapi "google.golang.org/api/chat/v1"
)

// Space types as returned by the Chat API (Space.spaceType).
const (
	// TypeSpace is a named space with one or more members.
	TypeSpace = "SPACE"
	// TypeGroupChat is an unnamed group conversation.
	TypeGroupChat = "GROUP_CHAT"
	// TypeDirectMessage is a 1:1 conversation.
	TypeDirectMessage = "DIRECT_MESSAGE"
)

// ErrDirectMessageNotFound is returned by FindDirectMessage when there is
// no direct message space with the requested user.
var ErrDirectMessageNotFound = errors.New("direct message not found")

// Space is a Chat space (named space, group chat or direct message).
type Space struct {
	// Name is the resource name, e.g. "spaces/AAAA1234".
	Name string `json:"name"`
	// DisplayName is empty for direct messages and most group chats.
	DisplayName string `json:"display_name,omitempty"`
	// Type is SPACE, GROUP_CHAT or DIRECT_MESSAGE.
	Type string `json:"type"`
	// URI opens the space in the Chat web UI.
	URI string `json:"uri,omitempty"`
	// LastActiveTime is the time of the last message in the space.
	LastActiveTime time.Time `json:"last_active_time,omitzero"`
	// MemberCount is the number of joined human members, when reported.
	MemberCount int64 `json:"member_count,omitempty"`
}

// User is a message sender or space member.
type User struct {
	// Name is the resource name, e.g. "users/123456789".
	Name string `json:"name"`
	// DisplayName is often empty with user authentication; see NameResolver.
	DisplayName string `json:"display_name,omitempty"`
	// Type is HUMAN or BOT.
	Type string `json:"type,omitempty"`
}

// Attachment describes a file attached to a message.
type Attachment struct {
	// Name is the attachment resource name.
	Name string `json:"name"`
	// ContentName is the original file name.
	ContentName string `json:"content_name,omitempty"`
	// ContentType is the MIME type.
	ContentType string `json:"content_type,omitempty"`
	// Source is UPLOADED_CONTENT or DRIVE_FILE.
	Source string `json:"source,omitempty"`
	// DriveFileID is set for Drive attachments.
	DriveFileID string `json:"drive_file_id,omitempty"`
}

// Message is a Chat message.
type Message struct {
	// Name is the resource name, e.g. "spaces/AAAA/messages/BBBB".
	Name string `json:"name"`
	// Space is the space resource name.
	Space string `json:"space"`
	// Thread is the thread resource name.
	Thread string `json:"thread,omitempty"`
	// ThreadReply reports whether the message is a reply inside a thread.
	ThreadReply bool `json:"thread_reply,omitempty"`
	// CreateTime is when the message was created.
	CreateTime time.Time `json:"create_time,omitzero"`
	// LastUpdateTime is when the message was last edited.
	LastUpdateTime time.Time `json:"last_update_time,omitzero"`
	// Sender is the author.
	Sender User `json:"sender"`
	// Text is the plain text body (falls back to formatted or argument text).
	Text string `json:"text"`
	// Attachments lists the attached files.
	Attachments []Attachment `json:"attachments"`
}

// NormalizeSpace turns "AAAA" or "spaces/AAAA" into "spaces/AAAA".
func NormalizeSpace(s string) (string, error) {
	s = strings.TrimSpace(s)
	id := strings.TrimPrefix(s, "spaces/")
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("invalid space %q: want spaces/{id} or a bare space id", s)
	}
	return "spaces/" + id, nil
}

// normalizeThread turns a bare thread id into "{space}/threads/{id}".
func normalizeThread(space, thread string) (string, error) {
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return "", nil
	}
	if strings.HasPrefix(thread, "spaces/") {
		if !strings.HasPrefix(thread, space+"/threads/") {
			return "", fmt.Errorf("thread %q does not belong to %s", thread, space)
		}
		return thread, nil
	}
	if strings.Contains(thread, "/") {
		return "", fmt.Errorf("invalid thread %q: want spaces/{space}/threads/{id} or a bare thread id", thread)
	}
	return space + "/threads/" + thread, nil
}

// SpaceTypeFilter maps a user-facing type ("space", "group", "dm" or an API
// enum value) to the API enum. An empty input returns "".
func SpaceTypeFilter(t string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "":
		return "", nil
	case "space":
		return TypeSpace, nil
	case "group", "group_chat":
		return TypeGroupChat, nil
	case "dm", "direct_message":
		return TypeDirectMessage, nil
	}
	return "", fmt.Errorf("invalid space type %q: want space, group or dm", t)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func convertSpace(s *chatapi.Space) Space {
	if s == nil {
		return Space{}
	}
	out := Space{
		Name:           s.Name,
		DisplayName:    s.DisplayName,
		Type:           s.SpaceType,
		URI:            s.SpaceUri,
		LastActiveTime: parseTime(s.LastActiveTime),
	}
	if out.Type == "" {
		// Legacy field, kept by older spaces.
		switch s.Type {
		case "ROOM":
			out.Type = TypeSpace
		case "DM":
			out.Type = TypeDirectMessage
		}
	}
	if s.MembershipCount != nil {
		out.MemberCount = s.MembershipCount.JoinedDirectHumanUserCount
	}
	return out
}

func convertUser(u *chatapi.User) User {
	if u == nil {
		return User{}
	}
	return User{Name: u.Name, DisplayName: u.DisplayName, Type: u.Type}
}

func convertMessage(m *chatapi.Message) Message {
	out := Message{
		Name:           m.Name,
		CreateTime:     parseTime(m.CreateTime),
		LastUpdateTime: parseTime(m.LastUpdateTime),
		Sender:         convertUser(m.Sender),
		ThreadReply:    m.ThreadReply,
		Text:           messageText(m),
		Attachments:    make([]Attachment, 0, len(m.Attachment)),
	}
	if m.Space != nil {
		out.Space = m.Space.Name
	}
	if out.Space == "" {
		if i := strings.Index(m.Name, "/messages/"); i > 0 {
			out.Space = m.Name[:i]
		}
	}
	if m.Thread != nil {
		out.Thread = m.Thread.Name
	}
	for _, a := range m.Attachment {
		if a == nil {
			continue
		}
		att := Attachment{Name: a.Name, ContentName: a.ContentName, ContentType: a.ContentType, Source: a.Source}
		if a.DriveDataRef != nil {
			att.DriveFileID = a.DriveDataRef.DriveFileId
		}
		out.Attachments = append(out.Attachments, att)
	}
	return out
}

// messageText returns the best plain-text representation of m.
func messageText(m *chatapi.Message) string {
	for _, s := range []string{m.Text, m.FormattedText, m.ArgumentText, m.FallbackText} {
		if s != "" {
			return s
		}
	}
	return ""
}
