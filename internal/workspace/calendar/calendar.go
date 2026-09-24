package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	calendarapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"

	"github.com/digio/gwork-cli/internal/timeutil"
)

// PrimaryCalendar is the alias of the account's primary calendar.
const PrimaryCalendar = "primary"

// DefaultMaxEvents is the number of events ListEvents returns when
// ListEventsOptions.Max is not positive.
const DefaultMaxEvents = 50

// Default window of ListEvents callers, in timeutil syntax: from the start
// of today to 7 days from now.
const (
	DefaultFrom = "today"
	DefaultTo   = "+7d"
)

// maxPageSize is the largest page requested from the API.
const maxPageSize = 250

// Calendar is an entry of the user's calendar list.
type Calendar struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary"`
	AccessRole  string `json:"access_role"`
	TimeZone    string `json:"time_zone,omitempty"`
}

// EventSummary is the compact view of an event returned by ListEvents.
//
// Start and End are strings: RFC 3339 timestamps for timed events (with the
// offset returned by the API) or YYYY-MM-DD dates for all-day events, in
// which case AllDay is true and End is exclusive (the day after the last
// day). Use ParseEventTime to turn them into time.Time values.
type EventSummary struct {
	ID         string `json:"id"`
	CalendarID string `json:"calendar_id"`
	Summary    string `json:"summary"`
	Start      string `json:"start"`
	End        string `json:"end"`
	AllDay     bool   `json:"all_day"`
	Location   string `json:"location,omitempty"`
	Status     string `json:"status,omitempty"`
	Organizer  string `json:"organizer,omitempty"`
	HTMLLink   string `json:"html_link,omitempty"`
	MeetLink   string `json:"meet_link,omitempty"`
}

// Person identifies the organizer or creator of an event.
type Person struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// Attendee is an event participant and their response.
type Attendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"display_name,omitempty"`
	ResponseStatus string `json:"response_status,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
	Organizer      bool   `json:"organizer,omitempty"`
	Self           bool   `json:"self,omitempty"`
	Resource       bool   `json:"resource,omitempty"`
}

// Attachment is a file attached to an event (usually a Drive file).
type Attachment struct {
	Title    string `json:"title"`
	FileURL  string `json:"file_url"`
	MimeType string `json:"mime_type,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

// Event is the full detail of an event returned by GetEvent. Start, End and
// AllDay follow the same rules as in EventSummary; TimeZone is the event's
// own time zone when the API reports one.
type Event struct {
	ID               string       `json:"id"`
	CalendarID       string       `json:"calendar_id"`
	Summary          string       `json:"summary"`
	Description      string       `json:"description,omitempty"`
	Start            string       `json:"start"`
	End              string       `json:"end"`
	AllDay           bool         `json:"all_day"`
	TimeZone         string       `json:"time_zone,omitempty"`
	Location         string       `json:"location,omitempty"`
	Status           string       `json:"status,omitempty"`
	HTMLLink         string       `json:"html_link,omitempty"`
	MeetLink         string       `json:"meet_link,omitempty"`
	Organizer        Person       `json:"organizer"`
	Creator          Person       `json:"creator"`
	Attendees        []Attendee   `json:"attendees"`
	Recurrence       []string     `json:"recurrence"`
	RecurringEventID string       `json:"recurring_event_id,omitempty"`
	Attachments      []Attachment `json:"attachments"`
	Created          time.Time    `json:"created,omitzero"`
	Updated          time.Time    `json:"updated,omitzero"`
}

// ListEventsOptions select the events returned by ListEvents.
type ListEventsOptions struct {
	// CalendarID is the calendar to read; empty means "primary".
	CalendarID string
	// From and To bound the window [From, To) on event end/start times; a
	// zero value leaves that bound open.
	From, To time.Time
	// Query is a free-text filter (summary, description, location,
	// attendees...).
	Query string
	// Max caps the number of events; <= 0 means DefaultMaxEvents.
	Max int
}

// ListCalendars returns the calendars in the user's calendar list.
func ListCalendars(ctx context.Context, opts ...option.ClientOption) ([]Calendar, error) {
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	out := []Calendar{}
	token := ""
	for {
		call := svc.CalendarList.List().Context(ctx).MaxResults(maxPageSize)
		if token != "" {
			call = call.PageToken(token)
		}
		res, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list calendars: %w", err)
		}
		for _, c := range res.Items {
			if c == nil {
				continue
			}
			summary := c.Summary
			if c.SummaryOverride != "" {
				summary = c.SummaryOverride
			}
			out = append(out, Calendar{
				ID:          c.Id,
				Summary:     summary,
				Description: c.Description,
				Primary:     c.Primary,
				AccessRole:  c.AccessRole,
				TimeZone:    c.TimeZone,
			})
		}
		if res.NextPageToken == "" {
			return out, nil
		}
		token = res.NextPageToken
	}
}

// ListEvents returns the events of a calendar in a time window, expanding
// recurring events into single instances ordered by start time.
func ListEvents(ctx context.Context, o ListEventsOptions, opts ...option.ClientOption) ([]EventSummary, error) {
	calID := calendarID(o.CalendarID)
	limit := o.Max
	if limit <= 0 {
		limit = DefaultMaxEvents
	}
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	out := []EventSummary{}
	token := ""
	for len(out) < limit {
		call := svc.Events.List(calID).Context(ctx).
			SingleEvents(true).
			OrderBy("startTime").
			MaxResults(int64(min(limit-len(out), maxPageSize)))
		if s := timeutil.FormatRFC3339(o.From); s != "" {
			call = call.TimeMin(s)
		}
		if s := timeutil.FormatRFC3339(o.To); s != "" {
			call = call.TimeMax(s)
		}
		if q := strings.TrimSpace(o.Query); q != "" {
			call = call.Q(q)
		}
		if token != "" {
			call = call.PageToken(token)
		}
		res, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list events in calendar %s: %w", calID, err)
		}
		for _, e := range res.Items {
			if e == nil || len(out) >= limit {
				continue
			}
			out = append(out, summarize(calID, e))
		}
		if res.NextPageToken == "" {
			break
		}
		token = res.NextPageToken
	}
	return out, nil
}

// GetEvent returns the full detail of an event.
func GetEvent(ctx context.Context, calendarIDArg, eventID string, opts ...option.ClientOption) (*Event, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, errors.New("event id is required")
	}
	calID := calendarID(calendarIDArg)
	svc, err := New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	e, err := svc.Events.Get(calID, eventID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get event %s in calendar %s: %w", eventID, calID, err)
	}
	return detail(calID, e), nil
}

// ParseEventTime parses an EventSummary/Event Start or End value in loc.
// Dates (YYYY-MM-DD) resolve to midnight in loc and report allDay=true;
// RFC 3339 timestamps are converted to loc.
func ParseEventTime(s string, loc *time.Location) (t time.Time, allDay bool, err error) {
	if loc == nil {
		loc = time.Local
	}
	if len(s) == len(timeutil.DateLayout) {
		t, err = time.ParseInLocation(timeutil.DateLayout, s, loc)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("parse event date %q: %w", s, err)
		}
		return t, true, nil
	}
	t, err = time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse event time %q: %w", s, err)
	}
	return t.In(loc), false, nil
}

func calendarID(id string) string {
	if id = strings.TrimSpace(id); id == "" {
		return PrimaryCalendar
	}
	return id
}

// eventTime returns the string form of an EventDateTime and whether it is
// an all-day value.
func eventTime(dt *calendarapi.EventDateTime) (string, bool) {
	if dt == nil {
		return "", false
	}
	if dt.DateTime != "" {
		return dt.DateTime, false
	}
	return dt.Date, dt.Date != ""
}

// meetLink returns the Meet (or other video conference) URL of e.
func meetLink(e *calendarapi.Event) string {
	if e.HangoutLink != "" {
		return e.HangoutLink
	}
	if e.ConferenceData != nil {
		for _, ep := range e.ConferenceData.EntryPoints {
			if ep != nil && ep.EntryPointType == "video" && ep.Uri != "" {
				return ep.Uri
			}
		}
	}
	return ""
}

func summarize(calID string, e *calendarapi.Event) EventSummary {
	start, allDay := eventTime(e.Start)
	end, _ := eventTime(e.End)
	s := EventSummary{
		ID:         e.Id,
		CalendarID: calID,
		Summary:    e.Summary,
		Start:      start,
		End:        end,
		AllDay:     allDay,
		Location:   e.Location,
		Status:     e.Status,
		HTMLLink:   e.HtmlLink,
		MeetLink:   meetLink(e),
	}
	if e.Organizer != nil {
		s.Organizer = e.Organizer.Email
	}
	return s
}

func detail(calID string, e *calendarapi.Event) *Event {
	start, allDay := eventTime(e.Start)
	end, _ := eventTime(e.End)
	ev := &Event{
		ID:               e.Id,
		CalendarID:       calID,
		Summary:          e.Summary,
		Description:      e.Description,
		Start:            start,
		End:              end,
		AllDay:           allDay,
		Location:         e.Location,
		Status:           e.Status,
		HTMLLink:         e.HtmlLink,
		MeetLink:         meetLink(e),
		Attendees:        []Attendee{},
		Recurrence:       []string{},
		RecurringEventID: e.RecurringEventId,
		Attachments:      []Attachment{},
		Created:          parseStamp(e.Created),
		Updated:          parseStamp(e.Updated),
	}
	if e.Start != nil {
		ev.TimeZone = e.Start.TimeZone
	}
	if e.Organizer != nil {
		ev.Organizer = Person{Email: e.Organizer.Email, DisplayName: e.Organizer.DisplayName, Self: e.Organizer.Self}
	}
	if e.Creator != nil {
		ev.Creator = Person{Email: e.Creator.Email, DisplayName: e.Creator.DisplayName, Self: e.Creator.Self}
	}
	for _, a := range e.Attendees {
		if a == nil {
			continue
		}
		ev.Attendees = append(ev.Attendees, Attendee{
			Email:          a.Email,
			DisplayName:    a.DisplayName,
			ResponseStatus: a.ResponseStatus,
			Optional:       a.Optional,
			Organizer:      a.Organizer,
			Self:           a.Self,
			Resource:       a.Resource,
		})
	}
	ev.Recurrence = append(ev.Recurrence, e.Recurrence...)
	for _, a := range e.Attachments {
		if a == nil {
			continue
		}
		ev.Attachments = append(ev.Attachments, Attachment{
			Title:    a.Title,
			FileURL:  a.FileUrl,
			MimeType: a.MimeType,
			FileID:   a.FileId,
		})
	}
	return ev
}

// parseStamp parses an RFC 3339 API timestamp, returning the zero time when
// it is empty or malformed.
func parseStamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
