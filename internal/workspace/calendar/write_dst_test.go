package calendar

import (
	"strings"
	"testing"
	"time"

	calendarapi "google.golang.org/api/calendar/v3"
)

func TestApplyTimesDST(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	allDay := func(s, e string) *calendarapi.Event {
		return &calendarapi.Event{Start: &calendarapi.EventDateTime{Date: s}, End: &calendarapi.EventDateTime{Date: e}}
	}
	tests := []struct {
		name         string
		cur          *calendarapi.Event
		start        time.Time
		wantS, wantE string
		timed        bool
	}{
		{"1-day onto autumn DST day", allDay("2026-10-20", "2026-10-21"), time.Date(2026, 10, 25, 0, 0, 0, 0, madrid), "2026-10-25", "2026-10-26", false},
		{"3-day over autumn DST", allDay("2026-10-20", "2026-10-23"), time.Date(2026, 10, 24, 0, 0, 0, 0, madrid), "2026-10-24", "2026-10-27", false},
		{"1-day onto spring DST day", allDay("2026-03-20", "2026-03-21"), time.Date(2026, 3, 29, 0, 0, 0, 0, madrid), "2026-03-29", "2026-03-30", false},
		{"3-day over spring DST", allDay("2026-03-20", "2026-03-23"), time.Date(2026, 3, 27, 0, 0, 0, 0, madrid), "2026-03-27", "2026-03-30", false},
		{"timed 1h move keeps absolute duration", &calendarapi.Event{
			Start: &calendarapi.EventDateTime{DateTime: "2026-10-20T10:00:00+02:00", TimeZone: "Europe/Madrid"},
			End:   &calendarapi.EventDateTime{DateTime: "2026-10-20T11:00:00+02:00", TimeZone: "Europe/Madrid"},
		}, time.Date(2026, 10, 25, 10, 0, 0, 0, madrid), "2026-10-25T10:00:00+01:00", "2026-10-25T11:00:00+01:00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &calendarapi.Event{}
			st := tt.start
			if err := (EventPatch{Start: &st}).applyTimes(body, tt.cur); err != nil {
				t.Fatal(err)
			}
			gs, ge := body.Start.Date, body.End.Date
			if tt.timed {
				gs, ge = body.Start.DateTime, body.End.DateTime
			}
			if gs != tt.wantS || ge != tt.wantE {
				t.Errorf("got %s..%s want %s..%s", gs, ge, tt.wantS, tt.wantE)
			}
		})
	}
}

func TestUpdateValidationAndConversion(t *testing.T) {
	cur := &calendarapi.Event{Start: &calendarapi.EventDateTime{Date: "2026-10-20"}, End: &calendarapi.EventDateTime{Date: "2026-10-21"}}
	f := false
	err := (EventPatch{AllDay: &f}).applyTimes(&calendarapi.Event{}, cur)
	if err == nil || !strings.Contains(err.Error(), "start is required when converting") {
		t.Errorf("err = %v", err)
	}
	for _, s := range []string{"", "   "} {
		if err := (EventPatch{Summary: &s}).Validate(); err == nil || !strings.Contains(err.Error(), "summary") {
			t.Errorf("summary %q: err = %v", s, err)
		}
	}
}
