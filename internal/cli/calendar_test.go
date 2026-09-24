package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // deterministic time zones regardless of the host

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
	"github.com/digio/gwork-cli/internal/workspace/calendar"
)

// calendarMux fakes the Calendar API with a primary calendar containing a
// timed event scheduled in New York and an all-day event.
func calendarMux(t *testing.T, gotQuery *[]string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/me/calendarList", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"items": []any{
			map[string]any{"id": "tester@digio.es", "summary": "Tester", "primary": true, "accessRole": "owner", "timeZone": "Europe/Madrid"},
			map[string]any{"id": "team@group.calendar.google.com", "summary": "Team", "accessRole": "reader"},
		}})
	})
	mux.HandleFunc("GET /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if gotQuery != nil {
			*gotQuery = append(*gotQuery, r.PathValue("id")+"?"+r.URL.RawQuery)
		}
		testutil.WriteJSON(t, w, map[string]any{"items": []any{
			map[string]any{
				"id": "ny1", "summary": "NY sync",
				"start":       map[string]any{"dateTime": "2026-09-24T10:00:00-04:00", "timeZone": "America/New_York"},
				"end":         map[string]any{"dateTime": "2026-09-24T11:00:00-04:00", "timeZone": "America/New_York"},
				"location":    "Zoom",
				"hangoutLink": "https://meet.google.com/aaa",
			},
			map[string]any{
				"id": "hol", "summary": "Offsite",
				"start": map[string]any{"date": "2026-09-25"},
				"end":   map[string]any{"date": "2026-09-27"},
			},
		}})
	})
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("event") != "ny1" {
			testutil.WriteGoogleError(w, 404, "notFound", "Not Found")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{
			"id": "ny1", "summary": "NY sync", "description": "Quarterly numbers",
			"start":     map[string]any{"dateTime": "2026-09-24T10:00:00-04:00", "timeZone": "America/New_York"},
			"end":       map[string]any{"dateTime": "2026-09-24T11:00:00-04:00", "timeZone": "America/New_York"},
			"organizer": map[string]any{"email": "boss@digio.es", "displayName": "Boss"},
			"attendees": []any{
				map[string]any{"email": "boss@digio.es", "organizer": true, "responseStatus": "accepted"},
				map[string]any{"email": "tester@digio.es", "self": true, "optional": true, "responseStatus": "needsAction"},
			},
			"attachments": []any{map[string]any{"title": "Slides", "fileUrl": "https://drive.google.com/s"}},
			"recurrence":  []any{"RRULE:FREQ=MONTHLY"},
			"hangoutLink": "https://meet.google.com/aaa",
		})
	})
	return mux
}

// runCalendarCLI runs args with the clock set to testNow in Europe/Madrid.
func runCalendarCLI(t *testing.T, p auth.ClientProvider, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	app, out, errw := newTestApp(t, p)
	app.Now = func() time.Time { return testNow.In(madrid) }
	code = app.Run(context.Background(), args)
	return out.String(), errw.String(), code
}

func TestCalendarCalendars(t *testing.T) {
	p := testutil.NewFakeProvider(t, calendarMux(t, nil))

	out, errOut, code := runCLI(t, p, "calendar", "calendars")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, s := range []string{"ID", "ACCESS", "tester@digio.es", "yes", "Europe/Madrid", "team@group.calendar.google.com"} {
		if !strings.Contains(out, s) {
			t.Errorf("text output missing %q:\n%s", s, out)
		}
	}

	out, errOut, code = runCLI(t, p, "calendar", "calendars", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var cals []calendar.Calendar
	if err := json.Unmarshal([]byte(out), &cals); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(cals) != 2 || !cals[0].Primary || cals[0].AccessRole != "owner" {
		t.Fatalf("unexpected %+v", cals)
	}
	if !strings.Contains(out, `"access_role"`) || !strings.Contains(out, `"time_zone"`) {
		t.Errorf("want snake_case keys:\n%s", out)
	}
}

func TestCalendarEventsDefaultWindowText(t *testing.T) {
	var q []string
	p := testutil.NewFakeProvider(t, calendarMux(t, &q))

	out, errOut, code := runCalendarCLI(t, p, "calendar", "events")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(q) != 1 {
		t.Fatalf("requests %v", q)
	}
	// testNow is 2026-09-24 14:00 in Madrid: the window starts at local
	// midnight and ends 7 days after now.
	for _, s := range []string{
		"primary?", "singleEvents=true", "orderBy=startTime", "maxResults=50",
		"timeMin=2026-09-24T00%3A00%3A00%2B02%3A00", "timeMax=2026-10-01T14%3A00%3A00%2B02%3A00",
	} {
		if !strings.Contains(q[0], s) {
			t.Errorf("request %q missing %q", q[0], s)
		}
	}
	// 10:00-11:00 New York time is 16:00-17:00 in Madrid.
	for _, s := range []string{"Thu 24 Sep 2026", "16:00-17:00", "NY sync", "Zoom", "ny1",
		"Fri 25 Sep 2026", "all day, until Sat 26 Sep", "Offsite"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	if strings.Index(out, "Thu 24 Sep") > strings.Index(out, "Fri 25 Sep") {
		t.Errorf("days out of order:\n%s", out)
	}
}

func TestCalendarEventsFlagsJSON(t *testing.T) {
	var q []string
	p := testutil.NewFakeProvider(t, calendarMux(t, &q))

	out, errOut, code := runCalendarCLI(t, p, "calendar", "events", "--json",
		"--calendar", "team@group.calendar.google.com", "--from", "2026-10-01", "--to", "2026-10-02",
		"--query", "sync", "--max", "5")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(q) != 1 {
		t.Fatalf("requests %v", q)
	}
	for _, s := range []string{
		"team@group.calendar.google.com?", "q=sync", "maxResults=5",
		"timeMin=2026-10-01T00%3A00%3A00%2B02%3A00", "timeMax=2026-10-03T00%3A00%3A00%2B02%3A00",
	} {
		if !strings.Contains(q[0], s) {
			t.Errorf("request %q missing %q", q[0], s)
		}
	}
	var events []calendar.EventSummary
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(events) != 2 {
		t.Fatalf("events %+v", events)
	}
	if e := events[0]; e.Start != "2026-09-24T10:00:00-04:00" || e.AllDay || e.MeetLink != "https://meet.google.com/aaa" ||
		e.CalendarID != "team@group.calendar.google.com" {
		t.Errorf("timed event %+v", e)
	}
	if e := events[1]; e.Start != "2026-09-25" || e.End != "2026-09-27" || !e.AllDay {
		t.Errorf("all-day event %+v", e)
	}
	if !strings.Contains(out, `"all_day": true`) || !strings.Contains(out, `"calendar_id"`) {
		t.Errorf("want snake_case keys:\n%s", out)
	}
}

func TestCalendarEventsEmptyAndBadWindow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendars/{id}/events", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{})
	})
	p := testutil.NewFakeProvider(t, mux)

	out, _, code := runCLI(t, p, "calendar", "events")
	if code != 0 || !strings.Contains(out, "No events between") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	out, _, code = runCLI(t, p, "calendar", "events", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("exit %d, json %q", code, out)
	}
	_, errOut, code := runCLI(t, p, "calendar", "events", "--from", "+3d", "--to", "today")
	if code != 1 || !strings.Contains(errOut, "invalid time window") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	_, errOut, code = runCLI(t, p, "calendar", "events", "--from", "someday")
	if code != 1 || !strings.Contains(errOut, "invalid time") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestCalendarGet(t *testing.T) {
	p := testutil.NewFakeProvider(t, calendarMux(t, nil))

	out, errOut, code := runCalendarCLI(t, p, "calendar", "get", "ny1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, s := range []string{
		"NY sync", "Thu 24 Sep 2026 16:00 - 17:00 CEST", "America/New_York", "Boss <boss@digio.es>",
		"https://meet.google.com/aaa", "RRULE:FREQ=MONTHLY", "Attendees (2)",
		"boss@digio.es (accepted, organizer)", "tester@digio.es (needsAction, optional, you)",
		"Slides <https://drive.google.com/s>", "Quarterly numbers",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}

	out, errOut, code = runCLI(t, p, "calendar", "get", "ny1", "--calendar", "team@group.calendar.google.com", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var ev calendar.Event
	if err := json.Unmarshal([]byte(out), &ev); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if ev.CalendarID != "team@group.calendar.google.com" || len(ev.Attendees) != 2 || ev.Attendees[1].ResponseStatus != "needsAction" ||
		ev.Organizer.Email != "boss@digio.es" || len(ev.Attachments) != 1 || ev.Description != "Quarterly numbers" {
		t.Fatalf("unexpected %+v", ev)
	}
	for _, s := range []string{`"response_status"`, `"file_url"`, `"display_name"`, `"meet_link"`} {
		if !strings.Contains(out, s) {
			t.Errorf("json missing %s:\n%s", s, out)
		}
	}
}

func TestCalendarGetErrors(t *testing.T) {
	p := testutil.NewFakeProvider(t, calendarMux(t, nil))
	_, errOut, code := runCLI(t, p, "calendar", "get", "missing")
	if code != 1 || !strings.Contains(errOut, "error:") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	_, _, code = runCLI(t, p, "calendar", "get")
	if code != 1 {
		t.Fatalf("missing arg should fail, exit %d", code)
	}

	noCal := testutil.NewFakeProvider(t, calendarMux(t, nil), auth.Gmail)
	_, errOut, code = runCLI(t, noCal, "calendar", "calendars")
	if code != 1 || !strings.Contains(errOut, "calendar") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
