package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

// calWriteAPI fakes the Calendar write endpoints and records requests as
// "METHOD path?query".
type calWriteAPI struct {
	mu   sync.Mutex
	reqs []string
	body map[string]any
	cur  map[string]any
}

func (c *calWriteAPI) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.reqs...)
}

func (c *calWriteAPI) mux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	rec := func(r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reqs = append(c.reqs, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			c.body = nil
			_ = json.NewDecoder(r.Body).Decode(&c.body)
		}
	}
	mux.HandleFunc("POST /calendars/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		out := map[string]any{"id": "new1", "summary": c.body["summary"], "start": c.body["start"], "end": c.body["end"]}
		testutil.WriteJSON(t, w, out)
	})
	mux.HandleFunc("GET /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		testutil.WriteJSON(t, w, c.cur)
	})
	mux.HandleFunc("PATCH /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		out := map[string]any{"id": r.PathValue("event"), "summary": "s", "start": c.cur["start"], "end": c.cur["end"]}
		if a, ok := c.body["attendees"]; ok {
			out["attendees"] = a
		}
		testutil.WriteJSON(t, w, out)
	})
	mux.HandleFunc("DELETE /calendars/{id}/events/{event}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func newCalWriteAPI(guests bool) *calWriteAPI {
	cur := map[string]any{
		"id": "e1", "summary": "Old",
		"start": map[string]any{"dateTime": "2026-09-25T10:00:00+02:00"},
		"end":   map[string]any{"dateTime": "2026-09-25T11:00:00+02:00"},
		"attendees": []any{
			map[string]any{"email": "tester@digio.es", "self": true, "responseStatus": "needsAction"},
		},
	}
	if guests {
		cur["attendees"] = append(cur["attendees"].([]any), map[string]any{"email": "ana@digio.es", "responseStatus": "accepted"})
	}
	return &calWriteAPI{cur: cur}
}

// runCalWrite runs args with optional stdin on a terminal.
func runCalWrite(t *testing.T, p auth.ClientProvider, tty bool, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	app, out, errw := newTestApp(t, p)
	app.In = strings.NewReader(stdin)
	app.IsTerminal = func() bool { return tty }
	code = app.Run(context.Background(), args)
	return out.String(), errw.String(), code
}

func TestCalendarEventCreate(t *testing.T) {
	t.Run("no attendees needs no confirmation", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		out, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Focus", "--start", "2026-09-25T10:00:00Z", "--json")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		var ev struct{ ID string }
		if err := json.Unmarshal([]byte(out), &ev); err != nil || ev.ID != "new1" {
			t.Fatalf("out %q err %v", out, err)
		}
		if got := api.calls(); len(got) != 1 || !strings.HasPrefix(got[0], "POST /calendars/primary/events?") || !strings.Contains(got[0], "sendUpdates=all") {
			t.Fatalf("calls %v", got)
		}
		// default duration 30m
		if end := api.body["end"].(map[string]any)["dateTime"]; end != "2026-09-25T10:30:00Z" {
			t.Errorf("end = %v", end)
		}
	})
	t.Run("attendees on non-tty fail without yes", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Sync", "--start", "+1d", "--attendee", "ana@digio.es")
		if code == 0 || !strings.Contains(errOut, "--yes") {
			t.Fatalf("code %d: %s", code, errOut)
		}
		if len(api.calls()) != 0 {
			t.Fatalf("calls %v", api.calls())
		}
	})
	t.Run("attendees with yes", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		out, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Sync", "--start", "+1d",
			"--duration", "1h", "--attendee", "ana@digio.es", "--meet", "--yes")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if !strings.Contains(out, "Created event new1") {
			t.Errorf("out %q", out)
		}
		if got := api.calls()[0]; !strings.Contains(got, "conferenceDataVersion=1") {
			t.Errorf("call %s", got)
		}
	})
	t.Run("attendees with send-updates none need no confirmation", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Sync", "--start", "+1d",
			"--attendee", "ana@digio.es", "--send-updates", "none")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	})
	t.Run("tty prompt", func(t *testing.T) {
		for _, c := range []struct {
			in   string
			code int
			n    int
		}{{"y\n", 0, 1}, {"n\n", 1, 0}} {
			api := newCalWriteAPI(false)
			p := testutil.NewFakeProvider(t, api.mux(t))
			_, errOut, code := runCalWrite(t, p, true, c.in, "calendar", "event", "create", "--summary", "Sync", "--start", "+1d", "--attendee", "ana@digio.es")
			if code != c.code || len(api.calls()) != c.n || !strings.Contains(errOut, "Proceed? [y/N]") {
				t.Fatalf("in %q: code %d calls %v stderr %s", c.in, code, api.calls(), errOut)
			}
		}
	})
	t.Run("dry run makes no call", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		out, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Sync", "--start", "2026-09-25T10:00:00Z",
			"--attendee", "ana@digio.es", "--dry-run", "--json")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if len(api.calls()) != 0 {
			t.Fatalf("calls %v", api.calls())
		}
		if !strings.Contains(out, `"create_event"`) || !strings.Contains(out, "ana@digio.es") {
			t.Errorf("out %s", out)
		}
	})
	t.Run("validation", func(t *testing.T) {
		p := testutil.NewFakeProvider(t, newCalWriteAPI(false).mux(t))
		for _, args := range [][]string{
			{"--start", "+1d"},
			{"--summary", "x"},
			{"--summary", "x", "--start", "+1d", "--end", "+2d", "--duration", "1h"},
			{"--summary", "x", "--start", "+1d", "--send-updates", "some"},
			{"--summary", "x", "--start", "+1d", "--attendee", "nope"},
			{"--summary", "x", "--start", "+2d", "--end", "+1d"},
		} {
			_, _, code := runCalWrite(t, p, false, "", append([]string{"calendar", "event", "create", "--dry-run"}, args...)...)
			if code == 0 {
				t.Errorf("%v: want failure", args)
			}
		}
	})
	t.Run("all day", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "Off", "--start", "2026-10-12", "--all-day")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		s, e := api.body["start"].(map[string]any), api.body["end"].(map[string]any)
		if s["date"] != "2026-10-12" || e["date"] != "2026-10-13" {
			t.Errorf("start %v end %v", s, e)
		}
	})
	t.Run("missing write scope", func(t *testing.T) {
		p := testutil.NewFakeProvider(t, newCalWriteAPI(false).mux(t)).GrantWrite()
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "create", "--summary", "x", "--start", "+1d")
		if code == 0 || !strings.Contains(errOut, "--write calendar") {
			t.Fatalf("code %d: %s", code, errOut)
		}
	})
}

func TestCalendarEventUpdate(t *testing.T) {
	t.Run("guests need confirmation", func(t *testing.T) {
		api := newCalWriteAPI(true)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "update", "e1", "--summary", "New")
		if code == 0 || !strings.Contains(errOut, "--yes") {
			t.Fatalf("code %d: %s", code, errOut)
		}
		for _, c := range api.calls() {
			if strings.HasPrefix(c, "PATCH") {
				t.Fatalf("patched: %v", api.calls())
			}
		}
	})
	t.Run("no other guests needs no confirmation", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		out, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "update", "e1", "--summary", "New", "--description", "")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if !strings.Contains(out, "Updated event e1") || api.body["summary"] != "New" || api.body["description"] != "" {
			t.Errorf("out %q body %v", out, api.body)
		}
	})
	t.Run("add attendee with yes", func(t *testing.T) {
		api := newCalWriteAPI(true)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "update", "e1", "--add-attendee", "bob@digio.es", "--remove-attendee", "ana@digio.es", "-y")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		att := api.body["attendees"].([]any)
		if len(att) != 2 || att[1].(map[string]any)["email"] != "bob@digio.es" {
			t.Errorf("attendees %v", att)
		}
	})
	t.Run("nothing to update and dry run", func(t *testing.T) {
		api := newCalWriteAPI(true)
		p := testutil.NewFakeProvider(t, api.mux(t))
		if _, _, code := runCalWrite(t, p, false, "", "calendar", "event", "update", "e1"); code == 0 {
			t.Error("want failure without changes")
		}
		out, _, code := runCalWrite(t, p, false, "", "calendar", "event", "update", "e1", "--summary", "x", "--dry-run", "--json")
		if code != 0 || len(api.calls()) != 0 || !strings.Contains(out, "update_event") {
			t.Fatalf("code %d calls %v out %s", code, api.calls(), out)
		}
	})
}

func TestCalendarEventDelete(t *testing.T) {
	t.Run("non-tty without yes fails", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		_, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "delete", "e1")
		if code == 0 || !strings.Contains(errOut, "--yes") || len(api.calls()) != 0 {
			t.Fatalf("code %d calls %v: %s", code, api.calls(), errOut)
		}
	})
	t.Run("tty answers", func(t *testing.T) {
		for _, c := range []struct {
			in   string
			code int
			n    int
		}{{"y\n", 0, 1}, {"\n", 1, 0}} {
			api := newCalWriteAPI(false)
			p := testutil.NewFakeProvider(t, api.mux(t))
			out, _, code := runCalWrite(t, p, true, c.in, "calendar", "event", "delete", "e1", "--send-updates", "none")
			if code != c.code || len(api.calls()) != c.n {
				t.Fatalf("in %q: code %d calls %v", c.in, code, api.calls())
			}
			if c.n == 1 && (!strings.Contains(out, "Deleted event e1") || !strings.Contains(api.calls()[0], "sendUpdates=none")) {
				t.Errorf("out %q calls %v", out, api.calls())
			}
		}
	})
	t.Run("yes json and dry run", func(t *testing.T) {
		api := newCalWriteAPI(false)
		p := testutil.NewFakeProvider(t, api.mux(t))
		out, _, code := runCalWrite(t, p, false, "", "calendar", "event", "delete", "e1", "--yes", "--json")
		if code != 0 || !strings.Contains(out, `"deleted": true`) {
			t.Fatalf("code %d out %s", code, out)
		}
		api2 := newCalWriteAPI(false)
		p2 := testutil.NewFakeProvider(t, api2.mux(t))
		if _, _, code := runCalWrite(t, p2, false, "", "calendar", "event", "delete", "e1", "--dry-run"); code != 0 || len(api2.calls()) != 0 {
			t.Fatalf("dry run: code %d calls %v", code, api2.calls())
		}
	})
}

func TestCalendarEventRespond(t *testing.T) {
	api := newCalWriteAPI(true)
	p := testutil.NewFakeProvider(t, api.mux(t))
	out, errOut, code := runCalWrite(t, p, false, "", "calendar", "event", "respond", "e1", "--response", "tentative", "--comment", "maybe", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, `"id": "e1"`) {
		t.Errorf("out %s", out)
	}
	att := api.body["attendees"].([]any)
	me := att[0].(map[string]any)
	if me["responseStatus"] != "tentative" || me["comment"] != "maybe" || att[1].(map[string]any)["responseStatus"] != "accepted" {
		t.Errorf("attendees %v", att)
	}
	if _, _, code := runCalWrite(t, p, false, "", "calendar", "event", "respond", "e1"); code == 0 {
		t.Error("want failure without --response")
	}
	if _, _, code := runCalWrite(t, p, false, "", "calendar", "event", "respond", "e1", "--response", "accepted", "--dry-run"); code != 0 {
		t.Error("dry run failed")
	}
}
