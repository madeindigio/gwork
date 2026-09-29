package calendar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	calendarapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/madeindigio/gwork/internal/timeutil"
)

// SendUpdates says who is notified by email about a change.
type SendUpdates string

// Notification modes accepted by the write operations.
const (
	// SendAll notifies every guest (the default).
	SendAll SendUpdates = "all"
	// SendExternalOnly notifies only guests that are not Google Calendar users.
	SendExternalOnly SendUpdates = "external_only"
	// SendNone notifies nobody.
	SendNone SendUpdates = "none"
)

// ParseSendUpdates validates s (all, external_only or none); empty means all.
func ParseSendUpdates(s string) (SendUpdates, error) {
	switch v := SendUpdates(strings.ToLower(strings.TrimSpace(s))); v {
	case "":
		return SendAll, nil
	case SendAll, SendExternalOnly, SendNone:
		return v, nil
	}
	return "", fmt.Errorf("invalid send-updates %q: use all, external_only or none", s)
}

// apiValue returns the value of the Calendar API sendUpdates parameter.
func (s SendUpdates) apiValue() string {
	switch s {
	case SendExternalOnly:
		return "externalOnly"
	case SendNone:
		return "none"
	}
	return "all"
}

// Responses accepted by RespondEvent.
const (
	ResponseAccepted  = "accepted"
	ResponseDeclined  = "declined"
	ResponseTentative = "tentative"
)

// EventInput describes an event to create.
//
// Timed events use Start and End (End must be after Start). All-day events
// (AllDay true) use the calendar dates of Start and End in their own
// locations; End is exclusive and defaults to the day after Start.
type EventInput struct {
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end,omitzero"`
	AllDay      bool      `json:"all_day,omitempty"`
	// TimeZone is an optional IANA zone (Europe/Madrid) attached to the
	// event; the times are rendered in it.
	TimeZone string `json:"time_zone,omitempty"`
	// Attendees are the email addresses to invite.
	Attendees []string `json:"attendees,omitempty"`
	// Meet adds a Google Meet link.
	Meet bool `json:"meet,omitempty"`
	// Visibility is default, public, private or confidential.
	Visibility string `json:"visibility,omitempty"`
	// Transparency is opaque (busy) or transparent (free).
	Transparency string `json:"transparency,omitempty"`
}

// EventPatch describes changes to an existing event. Nil pointers and empty
// lists leave the field untouched; a pointer to "" clears a text field.
type EventPatch struct {
	Summary     *string `json:"summary,omitempty"`
	Description *string `json:"description,omitempty"`
	Location    *string `json:"location,omitempty"`
	// Start and End move the event. When only Start is given the duration is
	// kept; when only End is given the start is kept.
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
	// AllDay converts the event to (or from) an all-day event; nil keeps the
	// current kind.
	AllDay   *bool  `json:"all_day,omitempty"`
	TimeZone string `json:"time_zone,omitempty"`
	// AddAttendees and RemoveAttendees change the guest list; the other
	// guests keep their response status.
	AddAttendees    []string `json:"add_attendees,omitempty"`
	RemoveAttendees []string `json:"remove_attendees,omitempty"`
	// AddMeet adds a Google Meet link when the event has none.
	AddMeet      bool    `json:"add_meet,omitempty"`
	Visibility   *string `json:"visibility,omitempty"`
	Transparency *string `json:"transparency,omitempty"`
}

// Validate checks the input without calling Google.
func (in EventInput) Validate() error {
	_, err := in.build()
	return err
}

// Validate checks the patch without calling Google.
func (p EventPatch) Validate() error {
	if p.isEmpty() {
		return errors.New("nothing to update: no changes given")
	}
	if _, err := normalizeEmails(p.AddAttendees); err != nil {
		return err
	}
	if _, err := normalizeEmails(p.RemoveAttendees); err != nil {
		return err
	}
	if _, err := loadZone(p.TimeZone); err != nil {
		return err
	}
	if p.Visibility != nil {
		if err := checkVisibility(*p.Visibility); err != nil {
			return err
		}
	}
	if p.Transparency != nil {
		if err := checkTransparency(*p.Transparency); err != nil {
			return err
		}
	}
	if p.Start != nil && p.End != nil && !p.End.After(*p.Start) {
		return errors.New("end must be after start")
	}
	return nil
}

func (p EventPatch) isEmpty() bool {
	return p.Summary == nil && p.Description == nil && p.Location == nil && p.Start == nil &&
		p.End == nil && p.AllDay == nil && len(p.AddAttendees) == 0 && len(p.RemoveAttendees) == 0 &&
		!p.AddMeet && p.Visibility == nil && p.Transparency == nil
}

// HasGuestsOtherThan reports whether the input invites anyone other than
// self (an email address, compared case-insensitively).
func (in EventInput) HasGuestsOtherThan(self string) bool {
	return hasOthers(in.Attendees, self)
}

// HasGuestsOtherThan reports whether the patch invites anyone other than self.
func (p EventPatch) HasGuestsOtherThan(self string) bool {
	return hasOthers(p.AddAttendees, self)
}

func hasOthers(emails []string, self string) bool {
	for _, e := range emails {
		if !strings.EqualFold(strings.TrimSpace(e), strings.TrimSpace(self)) {
			return true
		}
	}
	return false
}

// CreateEvent creates an event in calendarIDArg (empty means primary).
func CreateEvent(ctx context.Context, calendarIDArg string, in EventInput, send SendUpdates, opts ...option.ClientOption) (*Event, error) {
	ev, err := in.build()
	if err != nil {
		return nil, err
	}
	calID := calendarID(calendarIDArg)
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	call := svc.Events.Insert(calID, ev).Context(ctx).SendUpdates(send.apiValue())
	if in.Meet {
		call = call.ConferenceDataVersion(1)
	}
	created, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("create event in calendar %s: %w", calID, err)
	}
	return detail(calID, created), nil
}

// UpdateEvent applies patch to an event. The event is read first when the
// change needs its current state (times, attendees, Meet).
func UpdateEvent(ctx context.Context, calendarIDArg, eventID string, patch EventPatch, send SendUpdates, opts ...option.ClientOption) (*Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, errors.New("event id is required")
	}
	if err := patch.Validate(); err != nil {
		return nil, err
	}
	calID := calendarID(calendarIDArg)
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	body := &calendarapi.Event{}
	if patch.Summary != nil {
		body.Summary = *patch.Summary
		body.ForceSendFields = append(body.ForceSendFields, "Summary")
	}
	if patch.Description != nil {
		body.Description = *patch.Description
		body.ForceSendFields = append(body.ForceSendFields, "Description")
	}
	if patch.Location != nil {
		body.Location = *patch.Location
		body.ForceSendFields = append(body.ForceSendFields, "Location")
	}
	if patch.Visibility != nil {
		body.Visibility = *patch.Visibility
		body.ForceSendFields = append(body.ForceSendFields, "Visibility")
	}
	if patch.Transparency != nil {
		body.Transparency = *patch.Transparency
		body.ForceSendFields = append(body.ForceSendFields, "Transparency")
	}

	needCurrent := patch.Start != nil || patch.End != nil || patch.AllDay != nil ||
		len(patch.AddAttendees) > 0 || len(patch.RemoveAttendees) > 0 || patch.AddMeet
	conference := false
	if needCurrent {
		cur, err := svc.Events.Get(calID, eventID).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("get event %s in calendar %s: %w", eventID, calID, err)
		}
		if err := patch.applyTimes(body, cur); err != nil {
			return nil, err
		}
		if len(patch.AddAttendees) > 0 || len(patch.RemoveAttendees) > 0 {
			att, changed, err := patch.mergeAttendees(cur.Attendees)
			if err != nil {
				return nil, err
			}
			if changed {
				body.Attendees = att
				body.ForceSendFields = append(body.ForceSendFields, "Attendees")
			}
		}
		if patch.AddMeet && meetLink(cur) == "" {
			body.ConferenceData = newMeetRequest()
			conference = true
		}
	}
	call := svc.Events.Patch(calID, eventID, body).Context(ctx).SendUpdates(send.apiValue())
	if conference {
		call = call.ConferenceDataVersion(1)
	}
	updated, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("update event %s in calendar %s: %w", eventID, calID, err)
	}
	return detail(calID, updated), nil
}

// DeleteEvent deletes an event (a single instance when eventID is an
// instance id of a recurring event).
func DeleteEvent(ctx context.Context, calendarIDArg, eventID string, send SendUpdates, opts ...option.ClientOption) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return errors.New("event id is required")
	}
	calID := calendarID(calendarIDArg)
	svc, err := New(ctx, opts...)
	if err != nil {
		return err
	}
	if err := svc.Events.Delete(calID, eventID).Context(ctx).SendUpdates(send.apiValue()).Do(); err != nil {
		return fmt.Errorf("delete event %s in calendar %s: %w", eventID, calID, err)
	}
	return nil
}

// RespondEvent sets the user's response (accepted, declined or tentative) to
// an event, with an optional comment, notifying the organizer. The other
// attendees are preserved. eventID may be a single instance of a recurring
// event.
func RespondEvent(ctx context.Context, calendarIDArg, eventID, response, comment string, opts ...option.ClientOption) (*Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, errors.New("event id is required")
	}
	response = strings.ToLower(strings.TrimSpace(response))
	switch response {
	case ResponseAccepted, ResponseDeclined, ResponseTentative:
	default:
		return nil, fmt.Errorf("invalid response %q: use accepted, declined or tentative", response)
	}
	calID := calendarID(calendarIDArg)
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	cur, err := svc.Events.Get(calID, eventID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get event %s in calendar %s: %w", eventID, calID, err)
	}
	found := false
	for _, a := range cur.Attendees {
		if a != nil && a.Self {
			a.ResponseStatus = response
			if comment != "" {
				a.Comment = comment
			}
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("cannot respond to event %s: you are not an attendee (you may be its organizer only, or the event has no guests)", eventID)
	}
	body := &calendarapi.Event{Attendees: cur.Attendees}
	updated, err := svc.Events.Patch(calID, eventID, body).Context(ctx).SendUpdates(SendAll.apiValue()).Do()
	if err != nil {
		return nil, fmt.Errorf("respond to event %s in calendar %s: %w", eventID, calID, err)
	}
	return detail(calID, updated), nil
}

func (in EventInput) build() (*calendarapi.Event, error) {
	if strings.TrimSpace(in.Summary) == "" {
		return nil, errors.New("summary is required")
	}
	loc, err := loadZone(in.TimeZone)
	if err != nil {
		return nil, err
	}
	if in.Start.IsZero() {
		return nil, errors.New("start is required")
	}
	end := in.End
	if in.AllDay {
		if end.IsZero() {
			end = in.Start.AddDate(0, 0, 1)
		}
		if !dateOf(end).After(dateOf(in.Start)) {
			return nil, errors.New("end date must be after start date (end is exclusive)")
		}
	} else {
		if end.IsZero() {
			return nil, errors.New("end is required for timed events")
		}
		if !end.After(in.Start) {
			return nil, errors.New("end must be after start")
		}
	}
	emails, err := normalizeEmails(in.Attendees)
	if err != nil {
		return nil, err
	}
	if err := checkVisibility(in.Visibility); err != nil {
		return nil, err
	}
	if err := checkTransparency(in.Transparency); err != nil {
		return nil, err
	}
	ev := &calendarapi.Event{
		Summary:      in.Summary,
		Description:  in.Description,
		Location:     in.Location,
		Start:        eventDateTime(in.Start, in.AllDay, loc, in.TimeZone),
		End:          eventDateTime(end, in.AllDay, loc, in.TimeZone),
		Visibility:   in.Visibility,
		Transparency: in.Transparency,
	}
	// NullFields only matter when patching.
	ev.Start.NullFields, ev.End.NullFields = nil, nil
	for _, e := range emails {
		ev.Attendees = append(ev.Attendees, &calendarapi.EventAttendee{Email: e})
	}
	if in.Meet {
		ev.ConferenceData = newMeetRequest()
	}
	return ev, nil
}

// applyTimes sets body.Start/End from the patch and the current event.
func (p EventPatch) applyTimes(body, cur *calendarapi.Event) error {
	if p.Start == nil && p.End == nil && p.AllDay == nil {
		return nil
	}
	curStart, curAllDay, err := parseAPITime(cur.Start)
	if err != nil {
		return err
	}
	curEnd, _, err := parseAPITime(cur.End)
	if err != nil {
		return err
	}
	allDay := curAllDay
	if p.AllDay != nil {
		allDay = *p.AllDay
	}
	start, end := curStart, curEnd
	if p.Start != nil {
		start = *p.Start
		end = start.Add(curEnd.Sub(curStart))
	}
	if p.End != nil {
		end = *p.End
	}
	if allDay {
		if !dateOf(end).After(dateOf(start)) {
			if p.End != nil {
				return errors.New("end date must be after start date (end is exclusive)")
			}
			end = start.AddDate(0, 0, 1)
		}
	} else if !end.After(start) {
		return errors.New("end must be after start")
	}
	loc, err := loadZone(p.TimeZone)
	if err != nil {
		return err
	}
	tz := p.TimeZone
	if tz == "" && cur.Start != nil {
		tz = cur.Start.TimeZone
	}
	body.Start = eventDateTime(start, allDay, loc, tz)
	body.End = eventDateTime(end, allDay, loc, tz)
	return nil
}

// mergeAttendees applies the add/remove lists to cur, keeping the existing
// attendee records (and their response status).
func (p EventPatch) mergeAttendees(cur []*calendarapi.EventAttendee) ([]*calendarapi.EventAttendee, bool, error) {
	add, err := normalizeEmails(p.AddAttendees)
	if err != nil {
		return nil, false, err
	}
	remove, err := normalizeEmails(p.RemoveAttendees)
	if err != nil {
		return nil, false, err
	}
	out := []*calendarapi.EventAttendee{}
	changed := false
	for _, a := range cur {
		if a == nil {
			continue
		}
		if slices.ContainsFunc(remove, func(r string) bool { return strings.EqualFold(r, a.Email) }) {
			changed = true
			continue
		}
		out = append(out, a)
	}
	for _, e := range add {
		if slices.ContainsFunc(out, func(a *calendarapi.EventAttendee) bool { return strings.EqualFold(a.Email, e) }) {
			continue
		}
		out = append(out, &calendarapi.EventAttendee{Email: e})
		changed = true
	}
	return out, changed, nil
}

// parseAPITime reads an EventDateTime keeping its offset (dates are UTC
// midnight).
func parseAPITime(dt *calendarapi.EventDateTime) (time.Time, bool, error) {
	s, allDay := eventTime(dt)
	if s == "" {
		return time.Time{}, false, errors.New("event has no start/end time")
	}
	if allDay {
		t, err := time.ParseInLocation(timeutil.DateLayout, s, time.UTC)
		return t, true, err
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, false, err
}

// dateOf returns midnight UTC of t's calendar date in its own location, so
// dates compare independently of the zone.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// eventDateTime builds the API value for t: a date for all-day events, else
// an RFC 3339 timestamp (rendered in loc when given). It nulls the field of
// the other kind so PATCH can switch between them.
func eventDateTime(t time.Time, allDay bool, loc *time.Location, tz string) *calendarapi.EventDateTime {
	if allDay {
		return &calendarapi.EventDateTime{Date: t.Format(timeutil.DateLayout), NullFields: []string{"DateTime"}}
	}
	if loc != nil {
		t = t.In(loc)
	}
	return &calendarapi.EventDateTime{DateTime: t.Format(time.RFC3339), TimeZone: tz, NullFields: []string{"Date"}}
}

func loadZone(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid time zone %q: %w", name, err)
	}
	return loc, nil
}

// normalizeEmails validates addresses and returns their bare form.
func normalizeEmails(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		a, err := mail.ParseAddress(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("invalid attendee email %q: %w", s, err)
		}
		if !slices.ContainsFunc(out, func(e string) bool { return strings.EqualFold(e, a.Address) }) {
			out = append(out, a.Address)
		}
	}
	return out, nil
}

func checkVisibility(v string) error {
	switch v {
	case "", "default", "public", "private", "confidential":
		return nil
	}
	return fmt.Errorf("invalid visibility %q: use default, public, private or confidential", v)
}

func checkTransparency(v string) error {
	switch v {
	case "", "opaque", "transparent":
		return nil
	}
	return fmt.Errorf("invalid transparency %q: use opaque or transparent", v)
}

// newMeetRequest asks the API to create a Google Meet conference.
func newMeetRequest() *calendarapi.ConferenceData {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return &calendarapi.ConferenceData{CreateRequest: &calendarapi.CreateConferenceRequest{
		RequestId:             hex.EncodeToString(b[:]),
		ConferenceSolutionKey: &calendarapi.ConferenceSolutionKey{Type: "hangoutsMeet"},
	}}
}
