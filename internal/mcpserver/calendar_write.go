package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/timeutil"
	"github.com/madeindigio/gwork/internal/workspace/calendar"
)

// calendarDefaultMinutes is the length of a timed event created without
// end or duration_minutes.
const calendarDefaultMinutes = 30

const calendarConfirmNote = "Ask the user for explicit confirmation before calling this tool: it may email invitations or " +
	"notifications to other people. "

type calendarCreateEventInput struct {
	CalendarID      string   `json:"calendar_id,omitempty" jsonschema:"calendar id from calendar_list_calendars; default primary"`
	Summary         string   `json:"summary" jsonschema:"event title"`
	Description     string   `json:"description,omitempty" jsonschema:"event description"`
	Location        string   `json:"location,omitempty" jsonschema:"event location"`
	Start           string   `json:"start" jsonschema:"start time (or first day when all_day). RFC 3339, YYYY-MM-DD, today, tomorrow, now or relative +3d / +2h"`
	End             string   `json:"end,omitempty" jsonschema:"end time (for all_day: exclusive end date; default the day after start). Same formats as start"`
	DurationMinutes int      `json:"duration_minutes,omitempty" jsonschema:"length of a timed event in minutes when end is not given (default 30)"`
	AllDay          bool     `json:"all_day,omitempty" jsonschema:"create an all-day event using only the dates"`
	TimeZone        string   `json:"time_zone,omitempty" jsonschema:"IANA time zone of the event, e.g. Europe/Madrid"`
	Attendees       []string `json:"attendees,omitempty" jsonschema:"email addresses to invite; they receive an invitation unless send_updates is none"`
	AddMeet         bool     `json:"add_meet,omitempty" jsonschema:"add a Google Meet link"`
	Visibility      string   `json:"visibility,omitempty" jsonschema:"default, public, private or confidential"`
	Transparency    string   `json:"transparency,omitempty" jsonschema:"opaque (busy) or transparent (free)"`
	SendUpdates     string   `json:"send_updates,omitempty" jsonschema:"who is emailed about the change: all (default), external_only or none"`
}

type calendarEventOutput struct {
	Event calendar.Event `json:"event" jsonschema:"the resulting event: id, times, attendees with response status, meet link, html link"`
}

type calendarUpdateEventInput struct {
	CalendarID      string   `json:"calendar_id,omitempty" jsonschema:"calendar id; default primary"`
	EventID         string   `json:"event_id" jsonschema:"event id (or instance id of a recurring event) from calendar_list_events"`
	Summary         *string  `json:"summary,omitempty" jsonschema:"new title"`
	Description     *string  `json:"description,omitempty" jsonschema:"new description (empty string clears it)"`
	Location        *string  `json:"location,omitempty" jsonschema:"new location (empty string clears it)"`
	Start           string   `json:"start,omitempty" jsonschema:"new start; when only start is given the duration is kept. RFC 3339, YYYY-MM-DD, today, tomorrow, now or relative +3d / +2h"`
	End             string   `json:"end,omitempty" jsonschema:"new end (exclusive date for all-day events). Same formats as start"`
	DurationMinutes int      `json:"duration_minutes,omitempty" jsonschema:"new length in minutes counted from start (requires start)"`
	AllDay          *bool    `json:"all_day,omitempty" jsonschema:"convert the event to all-day (true) or timed (false)"`
	TimeZone        string   `json:"time_zone,omitempty" jsonschema:"IANA time zone for the new times"`
	AddAttendees    []string `json:"add_attendees,omitempty" jsonschema:"emails to invite; existing guests and their responses are kept"`
	RemoveAttendees []string `json:"remove_attendees,omitempty" jsonschema:"emails to remove from the guest list"`
	AddMeet         bool     `json:"add_meet,omitempty" jsonschema:"add a Google Meet link if the event has none"`
	Visibility      *string  `json:"visibility,omitempty" jsonschema:"default, public, private or confidential"`
	Transparency    *string  `json:"transparency,omitempty" jsonschema:"opaque (busy) or transparent (free)"`
	SendUpdates     string   `json:"send_updates,omitempty" jsonschema:"who is emailed about the change: all (default), external_only or none"`
}

type calendarDeleteEventInput struct {
	CalendarID  string `json:"calendar_id,omitempty" jsonschema:"calendar id; default primary"`
	EventID     string `json:"event_id" jsonschema:"event id (or instance id to delete a single occurrence of a recurring event)"`
	SendUpdates string `json:"send_updates,omitempty" jsonschema:"who is emailed about the cancellation: all (default), external_only or none"`
}

type calendarDeleteEventOutput struct {
	Deleted    bool   `json:"deleted" jsonschema:"true when the event was deleted"`
	CalendarID string `json:"calendar_id" jsonschema:"calendar the event was deleted from"`
	EventID    string `json:"event_id" jsonschema:"id of the deleted event"`
}

type calendarRespondEventInput struct {
	CalendarID string `json:"calendar_id,omitempty" jsonschema:"calendar id; default primary"`
	EventID    string `json:"event_id" jsonschema:"event id (or instance id of one occurrence of a recurring event)"`
	Response   string `json:"response" jsonschema:"accepted, declined or tentative"`
	Comment    string `json:"comment,omitempty" jsonschema:"optional comment for the organizer"`
}

// registerCalendarWrite registers the calendar write tools with addWriteTool. It is
// called only when the operator enabled calendar writes (--allow-write) and the
// account granted the calendar write scopes.
func registerCalendarWrite(s *mcp.Server, deps Deps) {
	yes := true
	ann := func(destructive, idempotent bool) *mcp.ToolAnnotations {
		d := destructive
		return &mcp.ToolAnnotations{DestructiveHint: &d, IdempotentHint: idempotent, OpenWorldHint: &yes}
	}

	addWriteTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_create_event",
		Description: "Create a Google Calendar event, optionally with attendees (who get an invitation email) and a Google Meet link. " +
			calendarConfirmNote + "Timed events last 30 minutes unless end or duration_minutes is given. " +
			"start/end: " + timeExpressions,
		Annotations: ann(false, false),
	}, func(ctx context.Context, in calendarCreateEventInput) (calendarEventOutput, error) {
		send, err := calendar.ParseSendUpdates(in.SendUpdates)
		if err != nil {
			return calendarEventOutput{}, err
		}
		if in.Start == "" {
			return calendarEventOutput{}, errors.New("start is required")
		}
		now := deps.CurrentTime()
		start, err := timeutil.Parse(in.Start, now)
		if err != nil {
			return calendarEventOutput{}, fmt.Errorf("start: %w", err)
		}
		ev := calendar.EventInput{
			Summary: in.Summary, Description: in.Description, Location: in.Location, Start: start,
			AllDay: in.AllDay, TimeZone: in.TimeZone, Attendees: in.Attendees, Meet: in.AddMeet,
			Visibility: in.Visibility, Transparency: in.Transparency,
		}
		if in.DurationMinutes < 0 {
			return calendarEventOutput{}, errors.New("duration_minutes must be positive")
		}
		if in.DurationMinutes != 0 && in.End != "" {
			return calendarEventOutput{}, errors.New("use either end or duration_minutes, not both")
		}
		if in.DurationMinutes != 0 && in.AllDay {
			return calendarEventOutput{}, errors.New("duration_minutes cannot be used with all_day; use end (exclusive date)")
		}
		switch {
		case in.End != "":
			if ev.End, err = timeutil.Parse(in.End, now); err != nil {
				return calendarEventOutput{}, fmt.Errorf("end: %w", err)
			}
		case in.DurationMinutes > 0 && !in.AllDay:
			ev.End = start.Add(time.Duration(in.DurationMinutes) * time.Minute)
		case !in.AllDay:
			ev.End = start.Add(calendarDefaultMinutes * time.Minute)
		}
		if err := ev.Validate(); err != nil {
			return calendarEventOutput{}, err
		}
		opts, err := deps.WriteClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarEventOutput{}, err
		}
		created, err := calendar.CreateEvent(ctx, in.CalendarID, ev, send, opts...)
		if err != nil {
			return calendarEventOutput{}, err
		}
		return calendarEventOutput{Event: *created}, nil
	})

	addWriteTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_update_event",
		Description: "Change fields of a Google Calendar event (title, description, location, times, attendees, Meet link). " +
			"Only the given fields change; added attendees are invited and existing guests keep their responses. " +
			calendarConfirmNote + "Existing guests are notified of the change unless send_updates is none. " +
			"start/end: " + timeExpressions,
		Annotations: ann(true, true),
	}, func(ctx context.Context, in calendarUpdateEventInput) (calendarEventOutput, error) {
		send, err := calendar.ParseSendUpdates(in.SendUpdates)
		if err != nil {
			return calendarEventOutput{}, err
		}
		now := deps.CurrentTime()
		p := calendar.EventPatch{
			Summary: in.Summary, Description: in.Description, Location: in.Location, AllDay: in.AllDay,
			TimeZone: in.TimeZone, AddAttendees: in.AddAttendees, RemoveAttendees: in.RemoveAttendees,
			AddMeet: in.AddMeet, Visibility: in.Visibility, Transparency: in.Transparency,
		}
		if in.Start != "" {
			s, err := timeutil.Parse(in.Start, now)
			if err != nil {
				return calendarEventOutput{}, fmt.Errorf("start: %w", err)
			}
			p.Start = &s
		}
		if in.DurationMinutes < 0 {
			return calendarEventOutput{}, errors.New("duration_minutes must be positive")
		}
		if in.DurationMinutes != 0 && in.End != "" {
			return calendarEventOutput{}, errors.New("use either end or duration_minutes, not both")
		}
		if in.DurationMinutes != 0 && in.AllDay != nil && *in.AllDay {
			return calendarEventOutput{}, errors.New("duration_minutes cannot be used with all_day; use end (exclusive date)")
		}
		switch {
		case in.End != "":
			e, err := timeutil.Parse(in.End, now)
			if err != nil {
				return calendarEventOutput{}, fmt.Errorf("end: %w", err)
			}
			p.End = &e
		case in.DurationMinutes > 0:
			if p.Start == nil {
				return calendarEventOutput{}, errors.New("duration_minutes requires start")
			}
			e := p.Start.Add(time.Duration(in.DurationMinutes) * time.Minute)
			p.End = &e
		}
		if err := p.Validate(); err != nil {
			return calendarEventOutput{}, err
		}
		opts, err := deps.WriteClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarEventOutput{}, err
		}
		updated, err := calendar.UpdateEvent(ctx, in.CalendarID, in.EventID, p, send, opts...)
		if err != nil {
			return calendarEventOutput{}, err
		}
		return calendarEventOutput{Event: *updated}, nil
	})

	addWriteTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_delete_event",
		Description: "Delete a Google Calendar event (or one occurrence of a recurring event given its instance id). " +
			"This cannot be undone from here and guests are notified of the cancellation unless send_updates is none. " +
			calendarConfirmNote + "Always confirm the exact event with the user first.",
		Annotations: ann(true, true),
	}, func(ctx context.Context, in calendarDeleteEventInput) (calendarDeleteEventOutput, error) {
		send, err := calendar.ParseSendUpdates(in.SendUpdates)
		if err != nil {
			return calendarDeleteEventOutput{}, err
		}
		opts, err := deps.WriteClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarDeleteEventOutput{}, err
		}
		if err := calendar.DeleteEvent(ctx, in.CalendarID, in.EventID, send, opts...); err != nil {
			return calendarDeleteEventOutput{}, err
		}
		calID := in.CalendarID
		if calID == "" {
			calID = calendar.PrimaryCalendar
		}
		return calendarDeleteEventOutput{Deleted: true, CalendarID: calID, EventID: in.EventID}, nil
	})

	addWriteTool(s, deps, auth.Calendar, &mcp.Tool{
		Name: "calendar_respond_event",
		Description: "Set the user's response (accepted, declined or tentative) to a Google Calendar invitation, " +
			"with an optional comment. The organizer is notified. Ask the user for explicit confirmation of the " +
			"response before calling this tool. Fails if the user is not an attendee of the event.",
		Annotations: ann(false, true),
	}, func(ctx context.Context, in calendarRespondEventInput) (calendarEventOutput, error) {
		opts, err := deps.WriteClientOptions(ctx, auth.Calendar)
		if err != nil {
			return calendarEventOutput{}, err
		}
		ev, err := calendar.RespondEvent(ctx, in.CalendarID, in.EventID, in.Response, in.Comment, opts...)
		if err != nil {
			return calendarEventOutput{}, err
		}
		return calendarEventOutput{Event: *ev}, nil
	})
}
