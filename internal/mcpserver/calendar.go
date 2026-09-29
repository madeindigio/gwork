package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/timeutil"
	"github.com/madeindigio/gwork/internal/workspace/calendar"
)

// calendarMaxEvents caps max_results of calendar_list_events so a single
// call cannot flood the model's context.
const calendarMaxEvents = 250

type calendarListCalendarsInput struct {
	// No fields: an empty struct still yields an object schema.
}

type calendarListCalendarsOutput struct {
	Calendars []calendar.Calendar `json:"calendars" jsonschema:"the calendars in the user's calendar list"`
}

type calendarListEventsInput struct {
	CalendarID string `json:"calendar_id,omitempty" jsonschema:"calendar id from calendar_list_calendars; default primary"`
	TimeMin    string `json:"time_min,omitempty" jsonschema:"start of the window (default: today 00:00 server local time). formats: RFC 3339 (2026-09-24T10:00:00Z), YYYY-MM-DD, today, tomorrow, yesterday, now, or relative +3d / 7d (ago) with units m h d w"`
	TimeMax    string `json:"time_max,omitempty" jsonschema:"end of the window (default: +7d); a date or today/tomorrow is inclusive (end of that day). formats: RFC 3339 (2026-09-24T10:00:00Z), YYYY-MM-DD, today, tomorrow, yesterday, now, or relative +3d / 7d (ago) with units m h d w"`
	Query      string `json:"query,omitempty" jsonschema:"free-text filter on summary, description, location and attendees"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"maximum number of events (default 50, max 250)"`
}

type calendarListEventsOutput struct {
	CalendarID string                  `json:"calendar_id" jsonschema:"calendar that was read"`
	TimeMin    string                  `json:"time_min" jsonschema:"resolved start of the window (RFC 3339)"`
	TimeMax    string                  `json:"time_max" jsonschema:"resolved end of the window (RFC 3339, exclusive)"`
	Events     []calendar.EventSummary `json:"events" jsonschema:"events ordered by start time; start/end are RFC 3339 for timed events or YYYY-MM-DD (end exclusive) when all_day is true"`
}

type calendarGetEventInput struct {
	CalendarID string `json:"calendar_id,omitempty" jsonschema:"calendar id; default primary"`
	EventID    string `json:"event_id" jsonschema:"event id from calendar_list_events"`
	MaxChars   int    `json:"max_chars,omitempty" jsonschema:"maximum characters of the description (default 20000)"`
}

type calendarGetEventOutput struct {
	Event     calendar.Event `json:"event" jsonschema:"full event detail: attendees with response status, organizer, creator, recurrence, attachments, meet link"`
	Truncated bool           `json:"truncated" jsonschema:"true when the description was cut to max_chars"`
	MaxChars  int            `json:"max_chars" jsonschema:"max_chars applied to the description"`
}

// registerCalendar registers the calendar_* tools. It is called by New only when the
// calendar service is requested and granted.
func registerCalendar(s *mcp.Server, deps Deps) {
	addReadOnlyTool(s, deps, auth.Calendar, &mcp.Tool{
		Name:        "calendar_list_calendars",
		Description: "List the Google Calendar calendars of the account (id, summary, primary, access_role, time_zone). Use the ids with calendar_list_events.",
	}, func(ctx context.Context, _ calendarListCalendarsInput) (calendarListCalendarsOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarListCalendarsOutput{}, err
		}
		cals, err := calendar.ListCalendars(ctx, opts...)
		if err != nil {
			return calendarListCalendarsOutput{}, err
		}
		return calendarListCalendarsOutput{Calendars: cals}, nil
	})

	addReadOnlyTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_list_events",
		Description: "List events of a Google Calendar calendar in a time window, with recurring events expanded " +
			"and ordered by start time. Defaults: calendar primary, from today 00:00 to 7 days from now, 50 events. " +
			"time_min/time_max: " + timeExpressions,
	}, func(ctx context.Context, in calendarListEventsInput) (calendarListEventsOutput, error) {
		win, err := timeutil.ParseWindow(in.TimeMin, in.TimeMax, deps.CurrentTime(), calendar.DefaultFrom, calendar.DefaultTo)
		if err != nil {
			return calendarListEventsOutput{}, err
		}
		opts, err := deps.ClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarListEventsOutput{}, err
		}
		calID := in.CalendarID
		if calID == "" {
			calID = calendar.PrimaryCalendar
		}
		events, err := calendar.ListEvents(ctx, calendar.ListEventsOptions{
			CalendarID: calID,
			From:       win.From,
			To:         win.To,
			Query:      in.Query,
			Max:        min(in.MaxResults, calendarMaxEvents),
		}, opts...)
		if err != nil {
			return calendarListEventsOutput{}, err
		}
		return calendarListEventsOutput{
			CalendarID: calID,
			TimeMin:    timeutil.FormatRFC3339(win.From),
			TimeMax:    timeutil.FormatRFC3339(win.To),
			Events:     events,
		}, nil
	})

	addReadOnlyTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_get_event",
		Description: "Get the full detail of a Google Calendar event: description (truncated to max_chars), attendees " +
			"with response status, organizer, creator, location, Meet link, recurrence and attachments.",
	}, func(ctx context.Context, in calendarGetEventInput) (calendarGetEventOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarGetEventOutput{}, err
		}
		ev, err := calendar.GetEvent(ctx, in.CalendarID, in.EventID, opts...)
		if err != nil {
			return calendarGetEventOutput{}, err
		}
		maxChars := effectiveMaxChars(in.MaxChars)
		var truncated bool
		ev.Description, truncated = TruncateText(ev.Description, maxChars)
		return calendarGetEventOutput{Event: *ev, Truncated: truncated, MaxChars: maxChars}, nil
	})
}
