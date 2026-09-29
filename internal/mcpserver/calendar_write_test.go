package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

var calendarWriteTools = map[string]struct{ destructive, idempotent bool }{
	"calendar_create_event":  {false, false},
	"calendar_update_event":  {true, true},
	"calendar_delete_event":  {true, true},
	"calendar_respond_event": {false, true},
}

func calendarWriteMux(t *testing.T, calls *[]string, body *map[string]any) *http.ServeMux {
	t.Helper()
	cur := map[string]any{
		"id": "e1", "summary": "Old",
		"start": map[string]any{"dateTime": "2026-09-25T10:00:00+02:00"},
		"end":   map[string]any{"dateTime": "2026-09-25T11:00:00+02:00"},
		"attendees": []any{
			map[string]any{"email": "tester@digio.es", "self": true, "responseStatus": "needsAction"},
			map[string]any{"email": "ana@digio.es", "responseStatus": "accepted"},
		},
	}
	rec := func(r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			*body = nil
			_ = json.NewDecoder(r.Body).Decode(body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		(*body)["id"] = "new1"
		testutil.WriteJSON(t, w, *body)
	})
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		testutil.WriteJSON(t, w, cur)
	})
	mux.HandleFunc("PATCH /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		out := map[string]any{"id": r.PathValue("event"), "start": cur["start"], "end": cur["end"]}
		for k, v := range *body {
			out[k] = v
		}
		testutil.WriteJSON(t, w, out)
	})
	mux.HandleFunc("DELETE /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func TestCalendarWriteToolsGating(t *testing.T) {
	var calls []string
	var body map[string]any
	// Without --allow-write calendar: absent.
	cs, _ := newTestSession(t, testDeps(t, calendarWriteMux(t, &calls, &body)), auth.Calendar)
	tools := listTools(t, cs)
	for name := range calendarWriteTools {
		if tools[name] != nil {
			t.Errorf("%s registered without allow-write", name)
		}
	}
	// With it: present with the right annotations.
	deps := testDeps(t, calendarWriteMux(t, &calls, &body))
	deps.Write = WriteOptions{Services: []auth.Service{auth.Calendar}}
	cs, _ = newTestSession(t, deps, auth.Calendar)
	tools = listTools(t, cs)
	for name, want := range calendarWriteTools {
		tool := tools[name]
		if tool == nil {
			t.Fatalf("%s missing", name)
		}
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint != want.destructive ||
			a.IdempotentHint != want.idempotent || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations %+v", name, a)
		}
		if !strings.Contains(tool.Description, "confirmation") {
			t.Errorf("%s description lacks confirmation note", name)
		}
	}
	// Write granted to another service only: absent.
	deps = testDeps(t, calendarWriteMux(t, &calls, &body))
	deps.Provider = testutil.NewFakeProvider(t, calendarWriteMux(t, &calls, &body)).GrantWrite(auth.Gmail)
	deps.Write = WriteOptions{Services: []auth.Service{auth.Calendar}}
	cs, _ = newTestSession(t, deps, auth.Calendar)
	if listTools(t, cs)["calendar_create_event"] != nil {
		t.Error("registered without write grant")
	}
}

func TestCalendarWriteTools(t *testing.T) {
	var calls []string
	var body map[string]any
	deps := testDeps(t, calendarWriteMux(t, &calls, &body))
	deps.Write = WriteOptions{Services: []auth.Service{auth.Calendar}}
	cs, _ := newTestSession(t, deps, auth.Calendar)

	t.Run("create", func(t *testing.T) {
		out, res := callTool[calendarEventOutput](t, cs, "calendar_create_event", map[string]any{
			"summary": "Sync", "start": "tomorrow", "duration_minutes": 45,
			"attendees": []string{"ana@digio.es"}, "add_meet": true, "send_updates": "external_only",
		})
		if res.IsError {
			t.Fatal(resultText(res))
		}
		if out.Event.ID != "new1" {
			t.Errorf("event %+v", out.Event)
		}
		c := calls[len(calls)-1]
		if !strings.Contains(c, "sendUpdates=externalOnly") || !strings.Contains(c, "conferenceDataVersion=1") {
			t.Errorf("call %s", c)
		}
		s, e := body["start"].(map[string]any)["dateTime"], body["end"].(map[string]any)["dateTime"]
		if s != "2026-09-25T00:00:00Z" || e != "2026-09-25T00:45:00Z" {
			t.Errorf("start %v end %v", s, e)
		}
	})
	t.Run("create all day", func(t *testing.T) {
		_, res := callTool[calendarEventOutput](t, cs, "calendar_create_event", map[string]any{
			"summary": "Off", "start": "2026-10-12", "all_day": true,
		})
		if res.IsError {
			t.Fatal(resultText(res))
		}
		if body["start"].(map[string]any)["date"] != "2026-10-12" || body["end"].(map[string]any)["date"] != "2026-10-13" {
			t.Errorf("body %v", body)
		}
	})
	t.Run("create invalid", func(t *testing.T) {
		n := len(calls)
		_, res := callTool[calendarEventOutput](t, cs, "calendar_create_event", map[string]any{"summary": "x", "start": "+1d", "send_updates": "bogus"})
		if !res.IsError || len(calls) != n {
			t.Fatalf("res %v calls %v", res, calls[n:])
		}
	})
	t.Run("duration and summary validation", func(t *testing.T) {
		n := len(calls)
		for _, tc := range []struct {
			tool string
			args map[string]any
		}{
			{"calendar_create_event", map[string]any{"summary": "x", "start": "+1d", "duration_minutes": -5}},
			{"calendar_create_event", map[string]any{"summary": "x", "start": "+1d", "end": "+2d", "duration_minutes": 30}},
			{"calendar_create_event", map[string]any{"summary": "x", "start": "+1d", "all_day": true, "duration_minutes": 30}},
			{"calendar_update_event", map[string]any{"event_id": "e1", "start": "+1d", "duration_minutes": -5}},
			{"calendar_update_event", map[string]any{"event_id": "e1", "start": "+1d", "end": "+2d", "duration_minutes": 30}},
			{"calendar_update_event", map[string]any{"event_id": "e1", "summary": "  "}},
		} {
			_, res := callTool[calendarEventOutput](t, cs, tc.tool, tc.args)
			if !res.IsError {
				t.Errorf("%s %v: expected error", tc.tool, tc.args)
			}
		}
		if len(calls) != n {
			t.Errorf("unexpected calls %v", calls[n:])
		}
	})
	t.Run("update", func(t *testing.T) {
		out, res := callTool[calendarEventOutput](t, cs, "calendar_update_event", map[string]any{
			"event_id": "e1", "summary": "New", "add_attendees": []string{"bob@digio.es"}, "remove_attendees": []string{"ana@digio.es"},
		})
		if res.IsError {
			t.Fatal(resultText(res))
		}
		att := body["attendees"].([]any)
		if body["summary"] != "New" || len(att) != 2 || att[0].(map[string]any)["self"] != true || att[1].(map[string]any)["email"] != "bob@digio.es" {
			t.Errorf("body %v", body)
		}
		if out.Event.ID != "e1" {
			t.Errorf("event %+v", out.Event)
		}
	})
	t.Run("delete", func(t *testing.T) {
		out, res := callTool[calendarDeleteEventOutput](t, cs, "calendar_delete_event", map[string]any{"event_id": "e1", "send_updates": "none"})
		if res.IsError || !out.Deleted || out.CalendarID != "primary" || out.EventID != "e1" {
			t.Fatalf("out %+v: %s", out, resultText(res))
		}
		if c := calls[len(calls)-1]; !strings.HasPrefix(c, "DELETE /calendars/primary/events/e1") || !strings.Contains(c, "sendUpdates=none") {
			t.Errorf("call %s", c)
		}
	})
	t.Run("respond", func(t *testing.T) {
		_, res := callTool[calendarEventOutput](t, cs, "calendar_respond_event", map[string]any{"event_id": "e1", "response": "declined", "comment": "busy"})
		if res.IsError {
			t.Fatal(resultText(res))
		}
		att := body["attendees"].([]any)
		me := att[0].(map[string]any)
		if me["responseStatus"] != "declined" || me["comment"] != "busy" || att[1].(map[string]any)["responseStatus"] != "accepted" {
			t.Errorf("attendees %v", att)
		}
	})
}
