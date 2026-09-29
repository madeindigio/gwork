package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // deterministic time zones regardless of the host

	"github.com/madeindigio/gwork/internal/testutil"
)

// recorded is one request seen by the fake API.
type recorded struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   map[string]any
}

// writeMux serves GET (returning cur) and POST/PATCH/DELETE for events,
// recording the requests.
func writeMux(t *testing.T, cur map[string]any, reqs *[]recorded) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	record := func(r *http.Request) map[string]any {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		*reqs = append(*reqs, recorded{r.Method, r.URL.Path, r.URL.Query(), body})
		return body
	}
	mux.HandleFunc("POST /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		body := record(r)
		body["id"] = "new1"
		testutil.WriteJSON(t, w, body)
	})
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		testutil.WriteJSON(t, w, cur)
	})
	mux.HandleFunc("PATCH /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		body := record(r)
		out := map[string]any{"id": r.PathValue("event")}
		for k, v := range cur {
			out[k] = v
		}
		for k, v := range body {
			out[k] = v
		}
		testutil.WriteJSON(t, w, out)
	})
	mux.HandleFunc("DELETE /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func tp(t time.Time) *time.Time { return &t }
func sp(s string) *string       { return &s }
func bp(b bool) *bool           { return &b }

func TestParseSendUpdates(t *testing.T) {
	for in, want := range map[string]SendUpdates{"": SendAll, "ALL": SendAll, "external_only": SendExternalOnly, "none": SendNone} {
		got, err := ParseSendUpdates(in)
		if err != nil || got != want {
			t.Errorf("ParseSendUpdates(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseSendUpdates("some"); err == nil {
		t.Error("want error")
	}
}

func TestCreateEvent(t *testing.T) {
	madrid, _ := time.LoadLocation("Europe/Madrid")
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, madrid)
	cases := []struct {
		name      string
		in        EventInput
		send      SendUpdates
		wantQuery map[string]string
		check     func(t *testing.T, body map[string]any)
		wantErr   string
	}{
		{
			name: "timed with attendees and meet",
			in: EventInput{Summary: "Sync", Description: "d", Location: "HQ", Start: start, End: start.Add(30 * time.Minute),
				Attendees: []string{"a@x.com", "Bob <b@x.com>", "A@X.com"}, Meet: true, TimeZone: "America/New_York"},
			send:      SendExternalOnly,
			wantQuery: map[string]string{"sendUpdates": "externalOnly", "conferenceDataVersion": "1"},
			check: func(t *testing.T, b map[string]any) {
				s := b["start"].(map[string]any)
				if s["dateTime"] != "2026-09-25T04:00:00-04:00" || s["timeZone"] != "America/New_York" || s["date"] != nil {
					t.Errorf("start = %v", s)
				}
				if att := b["attendees"].([]any); len(att) != 2 || att[1].(map[string]any)["email"] != "b@x.com" {
					t.Errorf("attendees = %v", att)
				}
				cd := b["conferenceData"].(map[string]any)["createRequest"].(map[string]any)
				if cd["requestId"] == "" || cd["conferenceSolutionKey"].(map[string]any)["type"] != "hangoutsMeet" {
					t.Errorf("conferenceData = %v", cd)
				}
			},
		},
		{
			name:      "all day defaults to one day",
			in:        EventInput{Summary: "Holiday", Start: start, AllDay: true},
			send:      SendNone,
			wantQuery: map[string]string{"sendUpdates": "none", "conferenceDataVersion": ""},
			check: func(t *testing.T, b map[string]any) {
				s, e := b["start"].(map[string]any), b["end"].(map[string]any)
				if s["date"] != "2026-09-25" || e["date"] != "2026-09-26" || s["dateTime"] != nil {
					t.Errorf("start %v end %v", s, e)
				}
				if b["attendees"] != nil || b["conferenceData"] != nil {
					t.Errorf("unexpected fields: %v", b)
				}
			},
		},
		{name: "no summary", in: EventInput{Start: start, End: start.Add(time.Hour)}, wantErr: "summary is required"},
		{name: "end before start", in: EventInput{Summary: "x", Start: start, End: start.Add(-time.Hour)}, wantErr: "end must be after start"},
		{name: "timed needs end", in: EventInput{Summary: "x", Start: start}, wantErr: "end is required"},
		{name: "bad email", in: EventInput{Summary: "x", Start: start, End: start.Add(time.Hour), Attendees: []string{"nope"}}, wantErr: "invalid attendee email"},
		{name: "bad zone", in: EventInput{Summary: "x", Start: start, End: start.Add(time.Hour), TimeZone: "Mars/Base"}, wantErr: "invalid time zone"},
		{name: "bad visibility", in: EventInput{Summary: "x", Start: start, End: start.Add(time.Hour), Visibility: "secret"}, wantErr: "invalid visibility"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var reqs []recorded
			opts := testutil.FakeGoogle(t, writeMux(t, nil, &reqs))
			ev, err := CreateEvent(context.Background(), "", c.in, c.send, opts...)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				if len(reqs) != 0 {
					t.Fatalf("requests sent: %v", reqs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ev.ID != "new1" || len(reqs) != 1 || reqs[0].Path != "/calendars/primary/events" {
				t.Fatalf("ev %+v reqs %+v", ev, reqs)
			}
			for k, v := range c.wantQuery {
				if got := reqs[0].Query[k]; (v == "" && len(got) != 0) || (v != "" && (len(got) != 1 || got[0] != v)) {
					t.Errorf("query %s = %v, want %q", k, got, v)
				}
			}
			c.check(t, reqs[0].Body)
		})
	}
}

func curEvent() map[string]any {
	return map[string]any{
		"id": "e1", "summary": "Old",
		"start": map[string]any{"dateTime": "2026-09-25T10:00:00+02:00", "timeZone": "Europe/Madrid"},
		"end":   map[string]any{"dateTime": "2026-09-25T11:00:00+02:00", "timeZone": "Europe/Madrid"},
		"attendees": []any{
			map[string]any{"email": "me@x.com", "self": true, "responseStatus": "accepted"},
			map[string]any{"email": "a@x.com", "responseStatus": "declined"},
			map[string]any{"email": "b@x.com", "responseStatus": "tentative"},
		},
	}
}

func TestUpdateEvent(t *testing.T) {
	newStart := time.Date(2026, 9, 26, 9, 0, 0, 0, time.FixedZone("", 2*3600))
	t.Run("text only makes a single patch", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		_, err := UpdateEvent(context.Background(), "cal@x.com", "e1", EventPatch{Summary: sp("New"), Description: sp("")}, SendNone, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if len(reqs) != 1 || reqs[0].Method != http.MethodPatch || reqs[0].Path != "/calendars/cal@x.com/events/e1" {
			t.Fatalf("reqs %+v", reqs)
		}
		b := reqs[0].Body
		if b["summary"] != "New" || b["description"] != "" || b["start"] != nil || b["attendees"] != nil {
			t.Errorf("body %v", b)
		}
		if reqs[0].Query["sendUpdates"][0] != "none" {
			t.Errorf("query %v", reqs[0].Query)
		}
	})
	t.Run("move keeps duration", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		if _, err := UpdateEvent(context.Background(), "", "e1", EventPatch{Start: tp(newStart)}, SendAll, opts...); err != nil {
			t.Fatal(err)
		}
		b := reqs[len(reqs)-1].Body
		s, e := b["start"].(map[string]any), b["end"].(map[string]any)
		if s["dateTime"] != "2026-09-26T09:00:00+02:00" || e["dateTime"] != "2026-09-26T10:00:00+02:00" || s["timeZone"] != "Europe/Madrid" {
			t.Errorf("start %v end %v", s, e)
		}
	})
	t.Run("convert to all day", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		if _, err := UpdateEvent(context.Background(), "", "e1", EventPatch{AllDay: bp(true)}, SendAll, opts...); err != nil {
			t.Fatal(err)
		}
		b := reqs[len(reqs)-1].Body
		s, e := b["start"].(map[string]any), b["end"].(map[string]any)
		if s["date"] != "2026-09-25" || e["date"] != "2026-09-26" || s["dateTime"] != nil {
			t.Errorf("start %v end %v", s, e)
		}
	})
	t.Run("attendees add and remove preserve responses", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		_, err := UpdateEvent(context.Background(), "", "e1", EventPatch{
			AddAttendees: []string{"c@x.com", "B@x.com"}, RemoveAttendees: []string{"A@X.com"},
		}, SendAll, opts...)
		if err != nil {
			t.Fatal(err)
		}
		att := reqs[len(reqs)-1].Body["attendees"].([]any)
		got := []string{}
		for _, a := range att {
			m := a.(map[string]any)
			got = append(got, m["email"].(string)+":"+asString(m["responseStatus"]))
		}
		if strings.Join(got, ",") != "me@x.com:accepted,b@x.com:tentative,c@x.com:" {
			t.Errorf("attendees = %v", got)
		}
	})
	t.Run("remove all sends empty list", func(t *testing.T) {
		var reqs []recorded
		cur := curEvent()
		cur["attendees"] = []any{map[string]any{"email": "a@x.com"}}
		opts := testutil.FakeGoogle(t, writeMux(t, cur, &reqs))
		if _, err := UpdateEvent(context.Background(), "", "e1", EventPatch{RemoveAttendees: []string{"a@x.com"}}, SendAll, opts...); err != nil {
			t.Fatal(err)
		}
		att, ok := reqs[len(reqs)-1].Body["attendees"].([]any)
		if !ok || len(att) != 0 {
			t.Errorf("attendees = %v", reqs[len(reqs)-1].Body["attendees"])
		}
	})
	t.Run("add meet only when missing", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		if _, err := UpdateEvent(context.Background(), "", "e1", EventPatch{AddMeet: true}, SendAll, opts...); err != nil {
			t.Fatal(err)
		}
		last := reqs[len(reqs)-1]
		if last.Query["conferenceDataVersion"][0] != "1" || last.Body["conferenceData"] == nil {
			t.Errorf("req %+v", last)
		}
		reqs = nil
		cur := curEvent()
		cur["hangoutLink"] = "https://meet.google.com/x"
		opts = testutil.FakeGoogle(t, writeMux(t, cur, &reqs))
		if _, err := UpdateEvent(context.Background(), "", "e1", EventPatch{AddMeet: true, Summary: sp("s")}, SendAll, opts...); err != nil {
			t.Fatal(err)
		}
		if last := reqs[len(reqs)-1]; last.Body["conferenceData"] != nil || len(last.Query["conferenceDataVersion"]) != 0 {
			t.Errorf("req %+v", last)
		}
	})
	t.Run("validation", func(t *testing.T) {
		var reqs []recorded
		opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
		for name, p := range map[string]EventPatch{
			"empty":    {},
			"bad mail": {AddAttendees: []string{"x"}},
			"bad end":  {End: tp(newStart.Add(-48 * time.Hour))},
		} {
			if _, err := UpdateEvent(context.Background(), "", "e1", p, SendAll, opts...); err == nil {
				t.Errorf("%s: want error", name)
			}
		}
		if _, err := UpdateEvent(context.Background(), "", " ", EventPatch{Summary: sp("x")}, SendAll, opts...); err == nil {
			t.Error("empty id: want error")
		}
	})
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func TestDeleteEvent(t *testing.T) {
	var reqs []recorded
	opts := testutil.FakeGoogle(t, writeMux(t, nil, &reqs))
	if err := DeleteEvent(context.Background(), "", "e1_20260925", SendExternalOnly, opts...); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Method != http.MethodDelete || reqs[0].Path != "/calendars/primary/events/e1_20260925" ||
		reqs[0].Query["sendUpdates"][0] != "externalOnly" {
		t.Fatalf("reqs %+v", reqs)
	}
	if err := DeleteEvent(context.Background(), "", "", SendAll, opts...); err == nil {
		t.Error("want error for empty id")
	}
}

func TestRespondEvent(t *testing.T) {
	var reqs []recorded
	opts := testutil.FakeGoogle(t, writeMux(t, curEvent(), &reqs))
	ev, err := RespondEvent(context.Background(), "", "e1_20260925T080000Z", "Declined", "on holiday", opts...)
	if err != nil {
		t.Fatal(err)
	}
	last := reqs[len(reqs)-1]
	if last.Method != http.MethodPatch || !strings.HasSuffix(last.Path, "/events/e1_20260925T080000Z") {
		t.Fatalf("req %+v", last)
	}
	att := last.Body["attendees"].([]any)
	if len(att) != 3 {
		t.Fatalf("attendees = %v", att)
	}
	me := att[0].(map[string]any)
	if me["responseStatus"] != "declined" || me["comment"] != "on holiday" || me["self"] != true {
		t.Errorf("me = %v", me)
	}
	if att[1].(map[string]any)["responseStatus"] != "declined" || att[2].(map[string]any)["responseStatus"] != "tentative" {
		t.Errorf("others changed: %v", att)
	}
	if len(ev.Attendees) != 3 {
		t.Errorf("event = %+v", ev)
	}

	t.Run("errors", func(t *testing.T) {
		var reqs []recorded
		noSelf := curEvent()
		noSelf["attendees"] = []any{map[string]any{"email": "a@x.com"}}
		opts := testutil.FakeGoogle(t, writeMux(t, noSelf, &reqs))
		if _, err := RespondEvent(context.Background(), "", "e1", "accepted", "", opts...); err == nil || !strings.Contains(err.Error(), "not an attendee") {
			t.Errorf("err = %v", err)
		}
		if len(reqs) != 1 || reqs[0].Method != http.MethodGet {
			t.Errorf("reqs %+v", reqs)
		}
		if _, err := RespondEvent(context.Background(), "", "e1", "maybe", "", opts...); err == nil {
			t.Error("want invalid response error")
		}
	})
}
