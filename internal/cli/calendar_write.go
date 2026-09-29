package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/timeutil"
	"github.com/madeindigio/gwork/internal/workspace/calendar"
)

// calendarDefaultDuration is the length of a timed event created without
// --end or --duration.
const calendarDefaultDuration = 30 * time.Minute

const calendarTimeHelp = "--start and --end accept RFC 3339 timestamps (2026-09-24T10:00:00+02:00), dates\n" +
	"(2026-09-24), today, tomorrow, now, and relative values (+3d, +2h; units m, h, d, w).\n" +
	"For --all-day events only the dates count and --end is exclusive (the day after the last day)."

// newCalendarEventCmd returns the "gwork calendar event" group with the
// commands that change events.
func newCalendarEventCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Create, update, delete events and answer invitations (needs write access)",
		Long: "Change Google Calendar events. These commands need calendar write access:\n" +
			"gwork auth login --services calendar --write calendar.\n" +
			"Changes that notify other people ask for confirmation (--yes to skip); use\n" +
			"--dry-run to preview the request.",
	}
	cmd.AddCommand(
		newCalendarEventCreateCmd(app),
		newCalendarEventUpdateCmd(app),
		newCalendarEventDeleteCmd(app),
		newCalendarEventRespondCmd(app),
	)
	return cmd
}

// calendarSelf returns the account email, or "" when it cannot be resolved
// (then every attendee counts as another person).
func (a *App) calendarSelf(ctx context.Context) string {
	p, err := a.Provider(ctx)
	if err != nil {
		return ""
	}
	return p.Account()
}

func newCalendarEventCreateCmd(app *App) *cobra.Command {
	var (
		wf                                   writeFlags
		calID, summary, desc, loc, tz        string
		start, end, sendUpdates, vis, transp string
		duration                             time.Duration
		allDay, meet                         bool
		attendees                            []string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an event",
		Long: "Create an event in a calendar (default: primary). Timed events last 30 minutes\n" +
			"unless --end or --duration is given.\n\n" + calendarTimeHelp + "\n\n" +
			"Inviting other people sends them an email (--send-updates none to avoid it), so\n" +
			"the command asks for confirmation unless --yes is given.",
		Example: "  gwork calendar event create --summary Sync --start tomorrow --duration 45m --attendee ana@digio.es --meet\n" +
			"  gwork calendar event create --summary Holiday --start 2026-10-12 --all-day\n" +
			"  gwork calendar event create --summary Focus --start +2h --end +4h --dry-run",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			send, err := calendar.ParseSendUpdates(sendUpdates)
			if err != nil {
				return err
			}
			if start == "" {
				return errors.New("--start is required")
			}
			if end != "" && duration != 0 {
				return errors.New("use either --end or --duration, not both")
			}
			now := app.CurrentTime()
			s, err := timeutil.Parse(start, now)
			if err != nil {
				return fmt.Errorf("--start: %w", err)
			}
			in := calendar.EventInput{
				Summary: summary, Description: desc, Location: loc, Start: s, AllDay: allDay,
				TimeZone: tz, Attendees: attendees, Meet: meet, Visibility: vis, Transparency: transp,
			}
			switch {
			case end != "":
				if in.End, err = timeutil.Parse(end, now); err != nil {
					return fmt.Errorf("--end: %w", err)
				}
			case duration != 0 && allDay:
				return errors.New("--duration cannot be used with --all-day; use --end (exclusive date)")
			case duration != 0:
				in.End = s.Add(duration)
			case !allDay:
				in.End = s.Add(calendarDefaultDuration)
			}
			if err := in.Validate(); err != nil {
				return err
			}
			if wf.DryRun {
				return app.printDryRun(map[string]any{
					"action": "create_event", "calendar_id": calendarIDOrPrimary(calID),
					"send_updates": send, "event": in,
				})
			}
			if in.HasGuestsOtherThan(app.calendarSelf(ctx)) && send != calendar.SendNone {
				if err := app.confirmWrite(wf, fmt.Sprintf("Create event %q and email an invitation to %s.",
					summary, strings.Join(in.Attendees, ", "))); err != nil {
					return err
				}
			}
			opts, err := app.WriteClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			ev, err := calendar.CreateEvent(ctx, calID, in, send, opts...)
			if err != nil {
				return classifyWrite(err, auth.Calendar)
			}
			return printCalendarEventResult(app, "Created event", ev)
		},
	}
	f := cmd.Flags()
	f.StringVar(&summary, "summary", "", "event title (required)")
	f.StringVar(&start, "start", "", "start time or date (required)")
	f.StringVar(&end, "end", "", "end time or date (exclusive for --all-day)")
	f.DurationVar(&duration, "duration", 0, "length of a timed event, e.g. 45m or 1h30m (default 30m)")
	f.BoolVar(&allDay, "all-day", false, "all-day event (uses the dates of --start/--end)")
	f.StringSliceVar(&attendees, "attendee", nil, "email to invite (repeatable or comma separated)")
	f.StringVar(&desc, "description", "", "event description")
	f.StringVar(&loc, "location", "", "event location")
	f.BoolVar(&meet, "meet", false, "add a Google Meet link")
	f.StringVar(&tz, "time-zone", "", "IANA time zone of the event, e.g. Europe/Madrid")
	f.StringVar(&vis, "visibility", "", "default, public, private or confidential")
	f.StringVar(&transp, "transparency", "", "opaque (busy) or transparent (free)")
	addCalendarWriteFlags(cmd, &calID, &sendUpdates)
	addWriteFlags(cmd, &wf)
	return cmd
}

func newCalendarEventUpdateCmd(app *App) *cobra.Command {
	var (
		wf                                   writeFlags
		calID, summary, desc, loc, tz        string
		start, end, sendUpdates, vis, transp string
		duration                             time.Duration
		allDay, meet                         bool
		addAtt, removeAtt                    []string
	)
	cmd := &cobra.Command{
		Use:   "update <eventId>",
		Short: "Change an event",
		Long: "Change fields of an event; only the flags you give are modified. Moving the\n" +
			"start keeps the duration unless --end or --duration is given. --add-attendee and\n" +
			"--remove-attendee change the guest list and keep everyone's response.\n\n" +
			calendarTimeHelp + "\n\nGuests are notified by email (--send-updates none to avoid it), so the command\n" +
			"asks for confirmation when the event has other guests, unless --yes is given.",
		Example: "  gwork calendar event update abc123 --summary 'New title' --location 'Room 2'\n" +
			"  gwork calendar event update abc123 --start +1d --add-attendee ana@digio.es",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fl := cmd.Flags()
			send, err := calendar.ParseSendUpdates(sendUpdates)
			if err != nil {
				return err
			}
			if end != "" && duration != 0 {
				return errors.New("use either --end or --duration, not both")
			}
			now := app.CurrentTime()
			p := calendar.EventPatch{
				TimeZone: tz, AddAttendees: addAtt, RemoveAttendees: removeAtt, AddMeet: meet,
			}
			setStr := func(name string, v string, dst **string) {
				if fl.Changed(name) {
					*dst = &v
				}
			}
			setStr("summary", summary, &p.Summary)
			setStr("description", desc, &p.Description)
			setStr("location", loc, &p.Location)
			setStr("visibility", vis, &p.Visibility)
			setStr("transparency", transp, &p.Transparency)
			if fl.Changed("all-day") {
				p.AllDay = &allDay
			}
			if start != "" {
				s, err := timeutil.Parse(start, now)
				if err != nil {
					return fmt.Errorf("--start: %w", err)
				}
				p.Start = &s
			}
			switch {
			case end != "":
				e, err := timeutil.Parse(end, now)
				if err != nil {
					return fmt.Errorf("--end: %w", err)
				}
				p.End = &e
			case duration != 0:
				if p.Start == nil {
					return errors.New("--duration needs --start (use --end to change only the end)")
				}
				e := p.Start.Add(duration)
				p.End = &e
			}
			if err := p.Validate(); err != nil {
				return err
			}
			if wf.DryRun {
				return app.printDryRun(map[string]any{
					"action": "update_event", "calendar_id": calendarIDOrPrimary(calID),
					"event_id": args[0], "send_updates": send, "changes": p,
				})
			}
			opts, err := app.WriteClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			if send != calendar.SendNone {
				notifies := p.HasGuestsOtherThan(app.calendarSelf(ctx))
				if !notifies {
					cur, err := calendar.GetEvent(ctx, calID, args[0], opts...)
					if err != nil {
						return classifyWrite(err, auth.Calendar)
					}
					self := app.calendarSelf(ctx)
					for _, a := range cur.Attendees {
						if !a.Self && !strings.EqualFold(a.Email, self) && !a.Resource {
							notifies = true
							break
						}
					}
				}
				if notifies {
					if err := app.confirmWrite(wf, fmt.Sprintf("Update event %s; its guests will be notified by email.", args[0])); err != nil {
						return err
					}
				}
			}
			ev, err := calendar.UpdateEvent(ctx, calID, args[0], p, send, opts...)
			if err != nil {
				return classifyWrite(err, auth.Calendar)
			}
			return printCalendarEventResult(app, "Updated event", ev)
		},
	}
	f := cmd.Flags()
	f.StringVar(&summary, "summary", "", "new title")
	f.StringVar(&start, "start", "", "new start time or date")
	f.StringVar(&end, "end", "", "new end time or date (exclusive for all-day events)")
	f.DurationVar(&duration, "duration", 0, "new length, counted from --start")
	f.BoolVar(&allDay, "all-day", false, "make the event all-day (--all-day=false makes it timed)")
	f.StringSliceVar(&addAtt, "add-attendee", nil, "email to invite (repeatable or comma separated)")
	f.StringSliceVar(&removeAtt, "remove-attendee", nil, "email to remove from the guests (repeatable)")
	f.StringVar(&desc, "description", "", "new description (empty clears it)")
	f.StringVar(&loc, "location", "", "new location (empty clears it)")
	f.BoolVar(&meet, "meet", false, "add a Google Meet link if the event has none")
	f.StringVar(&tz, "time-zone", "", "IANA time zone for the new times")
	f.StringVar(&vis, "visibility", "", "default, public, private or confidential")
	f.StringVar(&transp, "transparency", "", "opaque (busy) or transparent (free)")
	addCalendarWriteFlags(cmd, &calID, &sendUpdates)
	addWriteFlags(cmd, &wf)
	return cmd
}

func newCalendarEventDeleteCmd(app *App) *cobra.Command {
	var (
		wf                 writeFlags
		calID, sendUpdates string
	)
	cmd := &cobra.Command{
		Use:   "delete <eventId>",
		Short: "Delete an event",
		Long: "Delete an event, or a single occurrence of a recurring event when given the\n" +
			"instance id shown by 'gwork calendar events --json'. Guests are notified by\n" +
			"email (--send-updates none to avoid it). Always asks for confirmation unless\n" +
			"--yes is given.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			send, err := calendar.ParseSendUpdates(sendUpdates)
			if err != nil {
				return err
			}
			result := map[string]any{
				"action": "delete_event", "calendar_id": calendarIDOrPrimary(calID),
				"event_id": args[0], "send_updates": send,
			}
			if wf.DryRun {
				return app.printDryRun(result)
			}
			if err := app.confirmWrite(wf, fmt.Sprintf("Delete event %s from calendar %s.", args[0], calendarIDOrPrimary(calID))); err != nil {
				return err
			}
			opts, err := app.WriteClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			if err := calendar.DeleteEvent(ctx, calID, args[0], send, opts...); err != nil {
				return classifyWrite(err, auth.Calendar)
			}
			out := map[string]any{"deleted": true, "calendar_id": calendarIDOrPrimary(calID), "event_id": args[0]}
			return app.Print(out, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Deleted event %s\n", args[0])
				return err
			})
		},
	}
	addCalendarWriteFlags(cmd, &calID, &sendUpdates)
	addWriteFlags(cmd, &wf)
	return cmd
}

func newCalendarEventRespondCmd(app *App) *cobra.Command {
	var (
		wf                       writeFlags
		calID, response, comment string
	)
	cmd := &cobra.Command{
		Use:   "respond <eventId>",
		Short: "Accept, decline or tentatively accept an invitation",
		Long: "Set your response to an event you are invited to; the organizer is notified.\n" +
			"Use the instance id to answer a single occurrence of a recurring event.",
		Example: "  gwork calendar event respond abc123 --response accepted\n" +
			"  gwork calendar event respond abc123 --response declined --comment 'On holiday'",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			switch strings.ToLower(response) {
			case calendar.ResponseAccepted, calendar.ResponseDeclined, calendar.ResponseTentative:
			default:
				return errors.New("--response is required: accepted, declined or tentative")
			}
			if wf.DryRun {
				return app.printDryRun(map[string]any{
					"action": "respond_event", "calendar_id": calendarIDOrPrimary(calID),
					"event_id": args[0], "response": strings.ToLower(response), "comment": comment,
				})
			}
			opts, err := app.WriteClientOptions(ctx, auth.Calendar)
			if err != nil {
				return err
			}
			ev, err := calendar.RespondEvent(ctx, calID, args[0], response, comment, opts...)
			if err != nil {
				return classifyWrite(err, auth.Calendar)
			}
			return printCalendarEventResult(app, "Responded "+strings.ToLower(response)+" to event", ev)
		},
	}
	f := cmd.Flags()
	f.StringVar(&response, "response", "", "accepted, declined or tentative (required)")
	f.StringVar(&comment, "comment", "", "optional comment for the organizer")
	f.StringVar(&calID, "calendar", calendar.PrimaryCalendar, "calendar id (see: gwork calendar calendars)")
	// respond has no --yes: it never asks for confirmation.
	f.BoolVar(&wf.DryRun, "dry-run", false, "print what would be done and exit without calling Google")
	return cmd
}

// addCalendarWriteFlags registers --calendar and --send-updates.
func addCalendarWriteFlags(cmd *cobra.Command, calID, sendUpdates *string) {
	cmd.Flags().StringVar(calID, "calendar", calendar.PrimaryCalendar, "calendar id (see: gwork calendar calendars)")
	cmd.Flags().StringVar(sendUpdates, "send-updates", "all", "who is emailed about the change: all, external_only or none")
}

func calendarIDOrPrimary(id string) string {
	if strings.TrimSpace(id) == "" {
		return calendar.PrimaryCalendar
	}
	return id
}

// printCalendarEventResult prints the event returned by a write call.
func printCalendarEventResult(app *App, verb string, ev *calendar.Event) error {
	return app.Print(ev, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%s %s\n\n", verb, ev.ID); err != nil {
			return err
		}
		return writeCalendarEventText(w, ev, app.Location())
	})
}
