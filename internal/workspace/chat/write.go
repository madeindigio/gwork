package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	chatapi "google.golang.org/api/chat/v1"
)

// MaxTextLength is the maximum length, in characters, of a message text
// accepted by the Chat API.
const MaxTextLength = 4096

// SendInput describes a message to post as the authenticated user.
type SendInput struct {
	// Space is the target space ("spaces/X" or bare "X"). Exactly one of
	// Space and UserEmail must be set.
	Space string
	// UserEmail targets the existing direct message with this user; it is
	// resolved with spaces.findDirectMessage. No space is ever created.
	UserEmail string
	// Text is the message body (required, at most MaxTextLength characters).
	Text string
	// Thread optionally replies in this thread ("spaces/X/threads/Y" or a
	// bare thread id) of the target space.
	Thread string
}

// Validate checks the input without touching the network and returns the
// normalized space name ("" when UserEmail is used) and thread name.
func (in SendInput) Validate() (space, thread string, err error) {
	hasSpace := strings.TrimSpace(in.Space) != ""
	hasUser := strings.TrimSpace(in.UserEmail) != ""
	switch {
	case hasSpace == hasUser:
		return "", "", errors.New("specify exactly one target: a space or a user email")
	case strings.TrimSpace(in.Text) == "":
		return "", "", errors.New("message text is required")
	case utf8.RuneCountInString(in.Text) > MaxTextLength:
		return "", "", fmt.Errorf("message text is %d characters long; the Chat API limit is %d",
			utf8.RuneCountInString(in.Text), MaxTextLength)
	}
	if hasSpace {
		if space, err = NormalizeSpace(in.Space); err != nil {
			return "", "", err
		}
		thread, err = normalizeThread(space, in.Thread)
		return space, thread, err
	}
	if t := strings.TrimSpace(in.Thread); t != "" && !strings.HasPrefix(t, "spaces/") {
		return "", "", errors.New("with a user target the thread must be a full name (spaces/{space}/threads/{id})")
	}
	return "", strings.TrimSpace(in.Thread), nil
}

// SendMessage posts a message as the authenticated user and returns it.
// With UserEmail the target is the existing DM with that user; if there is
// none the error wraps ErrDirectMessageNotFound and nothing is created.
// With Thread the message is a reply (REPLY_MESSAGE_OR_FAIL).
func SendMessage(ctx context.Context, svc *chatapi.Service, in SendInput) (Message, error) {
	space, thread, err := in.Validate()
	if err != nil {
		return Message{}, err
	}
	if space == "" {
		dm, err := FindDirectMessage(ctx, svc, in.UserEmail)
		if err != nil {
			if errors.Is(err, ErrDirectMessageNotFound) {
				return Message{}, fmt.Errorf("no direct message with %s yet: start the conversation from Google Chat first, gwork never creates spaces: %w",
					strings.TrimSpace(in.UserEmail), err)
			}
			return Message{}, err
		}
		space = dm.Name
		if thread != "" {
			if thread, err = normalizeThread(space, thread); err != nil {
				return Message{}, err
			}
		}
	}
	msg := &chatapi.Message{Text: in.Text}
	call := svc.Spaces.Messages.Create(space, msg)
	if thread != "" {
		msg.Thread = &chatapi.Thread{Name: thread}
		call = call.MessageReplyOption("REPLY_MESSAGE_OR_FAIL")
	}
	created, err := call.Context(ctx).Do()
	if err != nil {
		return Message{}, fmt.Errorf("send message to %s: %w", space, err)
	}
	return convertMessage(created), nil
}
