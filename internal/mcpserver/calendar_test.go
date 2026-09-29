package mcpserver

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

func calendarTestMux(t *testing.T, queries *[]url.Values) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/me/calendarList", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"items": []any{
			map[string]any{"id": "tester@digio.es", "summary": "Tester", "primary": true, "accessRole": "owner", "timeZone": "Europe/Madrid"},
		}})
	})
	mux.HandleFunc("GET /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Set("_calendar", r.PathValue("id"))
		*queries = append(*queries, q)
		testutil.WriteJSON(t, w, map[string]any{"items": []any{
			map[string]any{
				"id": "e1", "summary": "Standup",
				"start":       map[string]any{"dateTime": "2026-09-24T10:00:00-04:00"},
				"end":         map[string]any{"dateTime": "2026-09-24T10:15:00-04:00"},
				"hangoutLink": "https://meet.google.com/aaa",
			},
			map[string]any{"id": "e2", "summary": "Holiday", "start": map[string]any{"date": "2026-09-25"}, "end": map[string]any{"date": "2026-09-26"}},
		}})
	})
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("event") != "e1" {
			testutil.WriteGoogleError(w, 404, "notFound", "Not Found")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{
			"id": "e1", "summary": "Standup", "description": "0123456789",
			"start":     map[string]any{"dateTime": "2026-09-24T10:00:00-04:00"},
			"end":       map[string]any{"dateTime": "2026-09-24T10:15:00-04:00"},
			"attendees": []any{map[string]any{"email": "a@digio.es", "responseStatus": "declined"}},
		})
	})
	return mux
}

type calEventsOut struct {
	CalendarID string `json:"calendar_id"`
	TimeMin    string `json:"time_min"`
	TimeMax    string `json:"time_max"`
	Events     []struct {
		ID       string `json:"id"`
		Start    string `json:"start"`
		AllDay   bool   `json:"all_day"`
		MeetLink string `json:"meet_link"`
	} `json:"events"`
}

type calEventOut struct {
	Event struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Attendees   []struct {
			Email          string `json:"email"`
			ResponseStatus string `json:"response_status"`
		} `json:"attendees"`
	} `json:"event"`
	Truncated bool `json:"truncated"`
	MaxChars  int  `json:"max_chars"`
}

func TestCalendarToolsRegistered(t *testing.T) {
	var qs []url.Values
	cs, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs)), auth.Calendar)
	tools := listTools(t, cs)
	for _, name := range []string{"calendar_list_calendars", "calendar_list_events", "calendar_get_event"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s must be readOnlyHint", name)
		}
	}
	if d := tools["calendar_list_events"].Description; !strings.Contains(d, "RFC 3339") || !strings.Contains(d, "+3d") {
		t.Errorf("list_events description should document time formats: %s", d)
	}

	cs2, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs), auth.Gmail))
	if _, ok := listTools(t, cs2)["calendar_list_events"]; ok {
		t.Error("calendar tools must not be registered without the calendar grant")
	}
}

func TestCalendarListCalendarsTool(t *testing.T) {
	var qs []url.Values
	cs, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs)), auth.Calendar)
	out, res := callTool[struct {
		Calendars []struct {
			ID         string `json:"id"`
			Primary    bool   `json:"primary"`
			AccessRole string `json:"access_role"`
		} `json:"calendars"`
	}](t, cs, "calendar_list_calendars", map[string]any{})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if len(out.Calendars) != 1 || !out.Calendars[0].Primary || out.Calendars[0].AccessRole != "owner" {
		t.Fatalf("unexpected %+v", out)
	}
}

func TestCalendarListEventsTool(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		calendar  string
		timeMin   string
		timeMax   string
		query     string
		maxResult string
	}{
		{
			name:     "defaults",
			args:     map[string]any{},
			calendar: "primary", timeMin: "2026-09-24T00:00:00Z", timeMax: "2026-10-01T12:00:00Z", maxResult: "50",
		},
		{
			name: "explicit",
			args: map[string]any{
				"calendar_id": "team@group.calendar.google.com", "time_min": "tomorrow", "time_max": "2026-09-30",
				"query": "standup", "max_results": 1000,
			},
			calendar: "team@group.calendar.google.com", timeMin: "2026-09-25T00:00:00Z", timeMax: "2026-10-01T00:00:00Z",
			query: "standup", maxResult: "250",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var qs []url.Values
			cs, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs)), auth.Calendar)
			out, res := callTool[calEventsOut](t, cs, "calendar_list_events", tt.args)
			if res.IsError {
				t.Fatalf("tool error: %s", resultText(res))
			}
			if len(qs) != 1 {
				t.Fatalf("requests: %v", qs)
			}
			q := qs[0]
			for k, want := range map[string]string{
				"_calendar": tt.calendar, "timeMin": tt.timeMin, "timeMax": tt.timeMax, "q": tt.query,
				"maxResults": tt.maxResult, "singleEvents": "true", "orderBy": "startTime",
			} {
				if got := q.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if out.CalendarID != tt.calendar || out.TimeMin != tt.timeMin || out.TimeMax != tt.timeMax {
				t.Errorf("output window %+v", out)
			}
			if len(out.Events) != 2 || out.Events[0].MeetLink != "https://meet.google.com/aaa" || out.Events[0].AllDay ||
				!out.Events[1].AllDay || out.Events[1].Start != "2026-09-25" {
				t.Errorf("events %+v", out.Events)
			}
		})
	}
}

func TestCalendarListEventsToolBadTime(t *testing.T) {
	var qs []url.Values
	cs, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs)), auth.Calendar)
	_, res := callTool[calEventsOut](t, cs, "calendar_list_events", map[string]any{"time_min": "next friday"})
	if !res.IsError || !strings.Contains(resultText(res), "invalid time") {
		t.Fatalf("want tool error, got %s", resultText(res))
	}
	if len(qs) != 0 {
		t.Fatalf("no API call expected, got %v", qs)
	}
}

func TestCalendarGetEventTool(t *testing.T) {
	var qs []url.Values
	cs, _ := newTestSession(t, testDeps(t, calendarTestMux(t, &qs)), auth.Calendar)

	out, res := callTool[calEventOut](t, cs, "calendar_get_event", map[string]any{"event_id": "e1", "max_chars": 4})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.Event.ID != "e1" || out.Event.Description != "0123" || !out.Truncated || out.MaxChars != 4 {
		t.Fatalf("unexpected %+v", out)
	}
	if len(out.Event.Attendees) != 1 || out.Event.Attendees[0].ResponseStatus != "declined" {
		t.Fatalf("attendees %+v", out.Event.Attendees)
	}

	out, res = callTool[calEventOut](t, cs, "calendar_get_event", map[string]any{"event_id": "e1"})
	if res.IsError || out.Truncated || out.Event.Description != "0123456789" || out.MaxChars != DefaultMaxChars {
		t.Fatalf("unexpected %+v %s", out, resultText(res))
	}

	_, res = callTool[calEventOut](t, cs, "calendar_get_event", map[string]any{"event_id": "nope"})
	if !res.IsError || !strings.Contains(resultText(res), "not found") {
		t.Fatalf("want not found tool error, got %s", resultText(res))
	}
}
