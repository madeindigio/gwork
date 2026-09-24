package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/output"
	"github.com/digio/gwork-cli/internal/timeutil"
	"github.com/digio/gwork-cli/internal/workspace/calendar"
)

// newCalendarCmd returns the "gwork calendar" command group.
func newCalendarCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Calendar, "Read Google Calendar calendars and events")
	cmd.AddCommand(
		newCalendarCalendarsCmd(app),
		newCalendarEventsCmd(app),
		newCalendarGetCmd(app),
	)
	return cmd
}

// newCalendarCalendarsCmd returns "gwork calendar calendars".
func newCalendarCalendarsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "calendars",
		Short: "List the calendars in your calendar list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			cals, err := calendar.ListCalendars(ctx, opts...)
			if err != nil {
				return err
			}
			return app.Print(cals, func(w io.Writer) error {
				rows := make([][]string, 0, len(cals))
				for _, c := range cals {
					primary := ""
					if c.Primary {
						primary = "yes"
					}
					rows = append(rows, []string{c.ID, c.Summary, primary, c.AccessRole, c.TimeZone})
				}
				return output.Table(w, []string{"ID", "SUMMARY", "PRIMARY", "ACCESS", "TIME ZONE"}, rows)
			})
		},
	}
}

// newCalendarEventsCmd returns "gwork calendar events".
func newCalendarEventsCmd(app *App) *cobra.Command {
	var calID, from, to, query string
	var maxEvents int
	cmd := &cobra.Command{
		Use:   "events",
		Short: "List events in a time window (default: today to 7 days from now)",
		Long: "List the events of a calendar in a time window, expanding recurring events\n" +
			"and ordering them by start time.\n\n" +
			"--from and --to accept RFC 3339 timestamps (2026-09-24T10:00:00+02:00), dates\n" +
			"(2026-09-24, --to is inclusive), today, tomorrow, yesterday, now, and relative\n" +
			"values (+3d is 3 days from now, 7d or -7d is 7 days ago; units m, h, d, w).",
		Example: "  gwork calendar events\n" +
			"  gwork calendar events --from tomorrow --to +14d --query standup\n" +
			"  gwork calendar events --calendar team@group.calendar.google.com --from 2026-10-01 --to 2026-10-31",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			now := app.CurrentTime()
			win, err := timeutil.ParseWindow(from, to, now, calendar.DefaultFrom, calendar.DefaultTo)
			if err != nil {
				return err
			}
			opts, err := app.ClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			events, err := calendar.ListEvents(ctx, calendar.ListEventsOptions{
				CalendarID: calID, From: win.From, To: win.To, Query: query, Max: maxEvents,
			}, opts...)
			if err != nil {
				return err
			}
			return app.Print(events, func(w io.Writer) error {
				return writeCalendarEventsText(w, events, win, now.Location())
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&calID, "calendar", calendar.PrimaryCalendar, "calendar id (see: gwork calendar calendars)")
	f.StringVar(&from, "from", "", "start of the window (default: today 00:00)")
	f.StringVar(&to, "to", "", "end of the window (default: +7d)")
	f.StringVarP(&query, "query", "q", "", "free-text filter on summary, description, location, attendees")
	f.IntVar(&maxEvents, "max", calendar.DefaultMaxEvents, "maximum number of events")
	return cmd
}

// newCalendarGetCmd returns "gwork calendar get".
func newCalendarGetCmd(app *App) *cobra.Command {
	var calID string
	cmd := &cobra.Command{
		Use:   "get <eventId>",
		Short: "Show the full detail of an event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			ev, err := calendar.GetEvent(ctx, calID, args[0], opts...)
			if err != nil {
				return err
			}
			return app.Print(ev, func(w io.Writer) error {
				return writeCalendarEventText(w, ev, app.CurrentTime().Location())
			})
		},
	}
	cmd.Flags().StringVar(&calID, "calendar", calendar.PrimaryCalendar, "calendar id (see: gwork calendar calendars)")
	return cmd
}

// Layouts used by the calendar text views.
const (
	calDayLayout  = "Mon 02 Jan 2006"
	calTimeLayout = "15:04"
)

// writeCalendarEventsText renders events grouped by local day.
func writeCalendarEventsText(w io.Writer, events []calendar.EventSummary, win timeutil.Window, loc *time.Location) error {
	if len(events) == 0 {
		_, err := fmt.Fprintf(w, "No events between %s and %s.\n",
			output.DateTime(win.From, loc), output.DateTime(win.To, loc))
		return err
	}
	var day string
	var rows [][]string
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if _, err := fmt.Fprintln(w, day); err != nil {
			return err
		}
		err := output.Table(w, nil, rows)
		rows = nil
		return err
	}
	for i, e := range events {
		d, when := calEventSlot(e, loc)
		if d != day {
			if err := flush(); err != nil {
				return err
			}
			if i > 0 {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			day = d
		}
		title := e.Summary
		if title == "" {
			title = "(no title)"
		}
		if e.Status == "cancelled" {
			title += " [cancelled]"
		}
		rows = append(rows, []string{"  " + when, output.Ellipsize(title, 60), output.Ellipsize(e.Location, 40), e.ID})
	}
	return flush()
}

// calEventSlot returns the day heading and the time column of an event in loc.
func calEventSlot(e calendar.EventSummary, loc *time.Location) (day, when string) {
	start, allDay, err := calendar.ParseEventTime(e.Start, loc)
	if err != nil {
		return e.Start, e.Start
	}
	day = start.Format(calDayLayout)
	end, _, err := calendar.ParseEventTime(e.End, loc)
	if allDay {
		when = "all day"
		if err == nil && end.After(start.AddDate(0, 0, 1)) {
			when = "all day, until " + end.AddDate(0, 0, -1).Format("Mon 02 Jan")
		}
		return day, when
	}
	when = start.Format(calTimeLayout)
	if err != nil {
		return day, when
	}
	if output.SameDay(start, end) {
		return day, when + "-" + end.Format(calTimeLayout)
	}
	return day, when + "-" + end.Format("Mon 02 Jan "+calTimeLayout)
}

// calEventWhen renders the start/end of an event in loc for the detail
// view.
func calEventWhen(start, end string, loc *time.Location) string {
	s, allDay, err := calendar.ParseEventTime(start, loc)
	if err != nil {
		return strings.TrimSpace(start + " - " + end)
	}
	e, _, err := calendar.ParseEventTime(end, loc)
	if allDay {
		if err != nil || !e.After(s.AddDate(0, 0, 1)) {
			return s.Format(calDayLayout) + " (all day)"
		}
		return s.Format(calDayLayout) + " - " + e.AddDate(0, 0, -1).Format(calDayLayout) + " (all day)"
	}
	out := s.Format(calDayLayout + " " + calTimeLayout)
	if err != nil {
		return out
	}
	if output.SameDay(s, e) {
		out += " - " + e.Format(calTimeLayout)
	} else {
		out += " - " + e.Format(calDayLayout+" "+calTimeLayout)
	}
	return out + " " + s.Format("MST")
}

// writeCalendarEventText renders the detail view of an event.
func writeCalendarEventText(w io.Writer, ev *calendar.Event, loc *time.Location) error {
	title := ev.Summary
	if title == "" {
		title = "(no title)"
	}
	var recurrence string
	if len(ev.Recurrence) > 0 {
		recurrence = strings.Join(ev.Recurrence, "; ")
	}
	if err := output.KeyValues(w,
		"Summary", title,
		"When", calEventWhen(ev.Start, ev.End, loc),
		"Event time zone", ev.TimeZone,
		"Location", ev.Location,
		"Status", ev.Status,
		"Organizer", output.Person(ev.Organizer.DisplayName, ev.Organizer.Email),
		"Creator", output.Person(ev.Creator.DisplayName, ev.Creator.Email),
		"Meet", ev.MeetLink,
		"Recurrence", recurrence,
		"Recurring event", ev.RecurringEventID,
		"Calendar", ev.CalendarID,
		"ID", ev.ID,
		"Link", ev.HTMLLink,
	); err != nil {
		return err
	}
	if len(ev.Attendees) > 0 {
		if _, err := fmt.Fprintf(w, "\nAttendees (%d):\n", len(ev.Attendees)); err != nil {
			return err
		}
		for _, a := range ev.Attendees {
			tags := []string{}
			if a.ResponseStatus != "" {
				tags = append(tags, a.ResponseStatus)
			}
			if a.Organizer {
				tags = append(tags, "organizer")
			}
			if a.Optional {
				tags = append(tags, "optional")
			}
			if a.Self {
				tags = append(tags, "you")
			}
			line := "  - " + output.Person(a.DisplayName, a.Email)
			if len(tags) > 0 {
				line += " (" + strings.Join(tags, ", ") + ")"
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	if len(ev.Attachments) > 0 {
		if _, err := fmt.Fprintf(w, "\nAttachments (%d):\n", len(ev.Attachments)); err != nil {
			return err
		}
		for _, a := range ev.Attachments {
			if _, err := fmt.Fprintf(w, "  - %s <%s>\n", a.Title, a.FileURL); err != nil {
				return err
			}
		}
	}
	if d := strings.TrimSpace(ev.Description); d != "" {
		if _, err := fmt.Fprintf(w, "\nDescription:\n%s\n", d); err != nil {
			return err
		}
	}
	return nil
}
