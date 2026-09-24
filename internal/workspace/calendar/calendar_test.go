package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // deterministic time zones regardless of the host

	"google.golang.org/api/googleapi"

	"github.com/digio/gwork-cli/internal/testutil"
)

func TestListCalendars(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/me/calendarList", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			testutil.WriteJSON(t, w, map[string]any{
				"items": []any{map[string]any{
					"id": "tester@digio.es", "summary": "Tester", "primary": true,
					"accessRole": "owner", "timeZone": "Europe/Madrid",
				}},
				"nextPageToken": "p2",
			})
			return
		}
		testutil.WriteJSON(t, w, map[string]any{
			"items": []any{map[string]any{
				"id": "team@group.calendar.google.com", "summary": "Team", "summaryOverride": "My team",
				"accessRole": "reader",
			}},
		})
	})
	got, err := ListCalendars(context.Background(), testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	want := []Calendar{
		{ID: "tester@digio.es", Summary: "Tester", Primary: true, AccessRole: "owner", TimeZone: "Europe/Madrid"},
		{ID: "team@group.calendar.google.com", Summary: "My team", AccessRole: "reader"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestListCalendarsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/me/calendarList", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{})
	})
	got, err := ListCalendars(context.Background(), testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	if string(data) != "[]" {
		t.Fatalf("want [], got %s", data)
	}
}

func TestListEventsQueryAndMapping(t *testing.T) {
	from := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	var gotQuery []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if id := r.PathValue("id"); id != "team@group.calendar.google.com" {
			t.Errorf("calendar id %q", id)
		}
		q := r.URL.Query()
		gotQuery = append(gotQuery, map[string]string{
			"timeMin": q.Get("timeMin"), "timeMax": q.Get("timeMax"), "singleEvents": q.Get("singleEvents"),
			"orderBy": q.Get("orderBy"), "q": q.Get("q"), "maxResults": q.Get("maxResults"),
			"pageToken": q.Get("pageToken"),
		})
		if q.Get("pageToken") == "" {
			testutil.WriteJSON(t, w, map[string]any{
				"items": []any{
					map[string]any{
						"id": "e1", "summary": "Standup", "status": "confirmed",
						"start":       map[string]any{"dateTime": "2026-09-24T10:00:00-04:00", "timeZone": "America/New_York"},
						"end":         map[string]any{"dateTime": "2026-09-24T10:30:00-04:00"},
						"organizer":   map[string]any{"email": "boss@digio.es"},
						"hangoutLink": "https://meet.google.com/abc-defg-hij",
						"location":    "Room 1", "htmlLink": "https://calendar.google.com/e1",
					},
				},
				"nextPageToken": "p2",
			})
			return
		}
		testutil.WriteJSON(t, w, map[string]any{
			"items": []any{
				map[string]any{
					"id": "e2", "summary": "Holiday",
					"start": map[string]any{"date": "2026-09-25"},
					"end":   map[string]any{"date": "2026-09-26"},
					"conferenceData": map[string]any{"entryPoints": []any{
						map[string]any{"entryPointType": "phone", "uri": "tel:+1"},
						map[string]any{"entryPointType": "video", "uri": "https://zoom.example/x"},
					}},
				},
				map[string]any{"id": "e3", "summary": "over the limit"},
			},
		})
	})

	got, err := ListEvents(context.Background(), ListEventsOptions{
		CalendarID: "team@group.calendar.google.com", From: from, To: to, Query: " review ", Max: 2,
	}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	want := []EventSummary{
		{
			ID: "e1", CalendarID: "team@group.calendar.google.com", Summary: "Standup",
			Start: "2026-09-24T10:00:00-04:00", End: "2026-09-24T10:30:00-04:00", Location: "Room 1",
			Status: "confirmed", Organizer: "boss@digio.es", HTMLLink: "https://calendar.google.com/e1",
			MeetLink: "https://meet.google.com/abc-defg-hij",
		},
		{
			ID: "e2", CalendarID: "team@group.calendar.google.com", Summary: "Holiday",
			Start: "2026-09-25", End: "2026-09-26", AllDay: true, MeetLink: "https://zoom.example/x",
		},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if len(gotQuery) != 2 {
		t.Fatalf("want 2 requests, got %d", len(gotQuery))
	}
	first := gotQuery[0]
	for k, v := range map[string]string{
		"timeMin": "2026-09-24T00:00:00Z", "timeMax": "2026-10-01T00:00:00Z", "singleEvents": "true",
		"orderBy": "startTime", "q": "review", "maxResults": "2",
	} {
		if first[k] != v {
			t.Errorf("%s = %q, want %q", k, first[k], v)
		}
	}
	if gotQuery[1]["pageToken"] != "p2" || gotQuery[1]["maxResults"] != "1" {
		t.Errorf("second page query %+v", gotQuery[1])
	}
}

func TestListEventsDefaultsAndOpenWindow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.PathValue("id") != "primary" || q.Get("maxResults") != "50" || q.Has("timeMin") || q.Has("timeMax") || q.Has("q") {
			t.Errorf("unexpected request %s", r.URL)
		}
		testutil.WriteJSON(t, w, map[string]any{})
	})
	got, err := ListEvents(context.Background(), ListEventsOptions{}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
}

func TestGetEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "primary" || r.PathValue("event") != "ev1_20260924" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		testutil.WriteJSON(t, w, map[string]any{
			"id": "ev1_20260924", "summary": "Planning", "description": "Agenda",
			"start":            map[string]any{"dateTime": "2026-09-24T16:00:00+02:00", "timeZone": "Europe/Madrid"},
			"end":              map[string]any{"dateTime": "2026-09-24T17:00:00+02:00", "timeZone": "Europe/Madrid"},
			"organizer":        map[string]any{"email": "boss@digio.es", "displayName": "Boss"},
			"creator":          map[string]any{"email": "tester@digio.es", "self": true},
			"recurringEventId": "ev1",
			"recurrence":       []any{"RRULE:FREQ=WEEKLY"},
			"attendees": []any{
				map[string]any{"email": "boss@digio.es", "organizer": true, "responseStatus": "accepted"},
				map[string]any{"email": "tester@digio.es", "self": true, "optional": true, "responseStatus": "tentative", "displayName": "Tester"},
			},
			"attachments":    []any{map[string]any{"title": "Doc", "fileUrl": "https://drive.google.com/x", "mimeType": "application/vnd.google-apps.document", "fileId": "x"}},
			"conferenceData": map[string]any{"entryPoints": []any{map[string]any{"entryPointType": "video", "uri": "https://meet.google.com/xyz"}}},
			"created":        "2026-09-01T08:00:00.000Z",
			"updated":        "2026-09-02T08:00:00.000Z",
		})
	})
	ev, err := GetEvent(context.Background(), "", "ev1_20260924", testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Summary != "Planning" || ev.Description != "Agenda" || ev.AllDay || ev.TimeZone != "Europe/Madrid" ||
		ev.MeetLink != "https://meet.google.com/xyz" || ev.RecurringEventID != "ev1" || ev.CalendarID != "primary" {
		t.Fatalf("unexpected event %+v", ev)
	}
	if ev.Organizer != (Person{Email: "boss@digio.es", DisplayName: "Boss"}) || ev.Creator != (Person{Email: "tester@digio.es", Self: true}) {
		t.Fatalf("organizer/creator %+v %+v", ev.Organizer, ev.Creator)
	}
	wantAtt := []Attendee{
		{Email: "boss@digio.es", Organizer: true, ResponseStatus: "accepted"},
		{Email: "tester@digio.es", Self: true, Optional: true, ResponseStatus: "tentative", DisplayName: "Tester"},
	}
	if !slices.Equal(ev.Attendees, wantAtt) {
		t.Fatalf("attendees %+v", ev.Attendees)
	}
	if !slices.Equal(ev.Recurrence, []string{"RRULE:FREQ=WEEKLY"}) {
		t.Fatalf("recurrence %v", ev.Recurrence)
	}
	if !slices.Equal(ev.Attachments, []Attachment{{Title: "Doc", FileURL: "https://drive.google.com/x", MimeType: "application/vnd.google-apps.document", FileID: "x"}}) {
		t.Fatalf("attachments %+v", ev.Attachments)
	}
	if !ev.Created.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("created %v", ev.Created)
	}
}

func TestGetEventEmptySlicesAndErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("event") == "missing" {
			testutil.WriteGoogleError(w, 404, "notFound", "Not Found")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"id": "e", "start": map[string]any{"date": "2026-09-24"}, "end": map[string]any{"date": "2026-09-25"}})
	})
	opts := testutil.FakeGoogle(t, mux)
	ev, err := GetEvent(context.Background(), "primary", "e", opts...)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(ev)
	for _, s := range []string{`"attendees":[]`, `"recurrence":[]`, `"attachments":[]`, `"all_day":true`} {
		if !strings.Contains(string(data), s) {
			t.Errorf("json missing %s: %s", s, data)
		}
	}
	if strings.Contains(string(data), `"created"`) {
		t.Errorf("zero created should be omitted: %s", data)
	}

	_, err = GetEvent(context.Background(), "primary", "missing", opts...)
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 || !strings.Contains(err.Error(), "get event missing") {
		t.Fatalf("want wrapped 404, got %v", err)
	}
	if _, err := GetEvent(context.Background(), "primary", " ", opts...); err == nil {
		t.Fatal("want error for empty event id")
	}
}

func TestParseEventTime(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in     string
		want   time.Time
		allDay bool
	}{
		// 10:00 in New York (EDT, -04:00) is 16:00 in Madrid (CEST, +02:00).
		{"2026-09-24T10:00:00-04:00", time.Date(2026, 9, 24, 16, 0, 0, 0, madrid), false},
		{"2026-09-24T22:30:00Z", time.Date(2026, 9, 25, 0, 30, 0, 0, madrid), false},
		{"2026-09-24", time.Date(2026, 9, 24, 0, 0, 0, 0, madrid), true},
	}
	for _, tt := range tests {
		got, allDay, err := ParseEventTime(tt.in, madrid)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if !got.Equal(tt.want) || allDay != tt.allDay || got.Location() != madrid {
			t.Errorf("%s: got %v allDay=%v", tt.in, got, allDay)
		}
		if got.Hour() != tt.want.Hour() {
			t.Errorf("%s: hour %d, want %d", tt.in, got.Hour(), tt.want.Hour())
		}
	}
	if _, _, err := ParseEventTime("garbage", madrid); err == nil {
		t.Fatal("want error")
	}
}
