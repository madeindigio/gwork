package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	chatapi "google.golang.org/api/chat/v1"

	"github.com/digio/gwork-cli/internal/testutil"
	"github.com/digio/gwork-cli/internal/timeutil"
)

var testNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func newTestService(t *testing.T, mux *http.ServeMux) *chatapi.Service {
	t.Helper()
	svc, err := New(context.Background(), testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func apiMsg(space, id, text string, at time.Time, sender string) map[string]any {
	return map[string]any{
		"name":       space + "/messages/" + id,
		"space":      map[string]any{"name": space},
		"thread":     map[string]any{"name": space + "/threads/" + id},
		"createTime": at.Format(time.RFC3339Nano),
		"sender":     map[string]any{"name": sender, "type": "HUMAN"},
		"text":       text,
	}
}

func TestNormalizeSpace(t *testing.T) {
	for in, want := range map[string]string{"AAA": "spaces/AAA", "spaces/AAA": "spaces/AAA", " spaces/B ": "spaces/B"} {
		got, err := NormalizeSpace(in)
		if err != nil || got != want {
			t.Errorf("NormalizeSpace(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "spaces/", "spaces/A/messages/B"} {
		if _, err := NormalizeSpace(in); err == nil {
			t.Errorf("NormalizeSpace(%q): want error", in)
		}
	}
}

func TestMessageFilter(t *testing.T) {
	w := timeutil.Window{From: testNow.Add(-24 * time.Hour), To: testNow}
	tests := []struct {
		name   string
		w      timeutil.Window
		thread string
		want   string
	}{
		{"empty", timeutil.Window{}, "", ""},
		{"from", timeutil.Window{From: w.From}, "", `createTime > "2026-09-23T11:59:59Z"`},
		{"window+thread", w, "spaces/S/threads/T",
			`createTime > "2026-09-23T11:59:59Z" AND createTime < "2026-09-24T12:00:00Z" AND thread.name = spaces/S/threads/T`},
		{"thread", timeutil.Window{}, "spaces/S/threads/T", "thread.name = spaces/S/threads/T"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MessageFilter(tt.w, tt.thread); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListSpaces(t *testing.T) {
	var filters, sizes []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		filters = append(filters, r.URL.Query().Get("filter"))
		sizes = append(sizes, r.URL.Query().Get("pageSize"))
		if r.URL.Query().Get("pageToken") == "" {
			testutil.WriteJSON(t, w, map[string]any{
				"spaces": []any{map[string]any{
					"name": "spaces/A", "displayName": "Team", "spaceType": "SPACE",
					"lastActiveTime": "2026-09-24T10:00:00Z", "spaceUri": "https://chat.google.com/room/A",
					"membershipCount": map[string]any{"joinedDirectHumanUserCount": 7},
				}},
				"nextPageToken": "p2",
			})
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{
			map[string]any{"name": "spaces/B", "spaceType": "SPACE"},
			map[string]any{"name": "spaces/C", "spaceType": "SPACE"},
		}})
	})
	svc := newTestService(t, mux)
	got, err := ListSpaces(context.Background(), svc, ListSpacesOptions{Type: "space", Max: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "spaces/A" || got[1].Name != "spaces/B" {
		t.Fatalf("got %+v", got)
	}
	if got[0].MemberCount != 7 || got[0].DisplayName != "Team" || got[0].Type != TypeSpace ||
		!got[0].LastActiveTime.Equal(time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("space conversion: %+v", got[0])
	}
	if filters[0] != `spaceType = "SPACE"` || filters[1] != filters[0] {
		t.Errorf("filters = %q", filters)
	}
	if sizes[0] != "2" || sizes[1] != "1" {
		t.Errorf("page sizes = %q", sizes)
	}

	if _, err := ListSpaces(context.Background(), svc, ListSpacesOptions{Type: "bogus"}); err == nil {
		t.Error("want error for invalid type")
	}
}

func TestSpaceTypeFilter(t *testing.T) {
	for in, want := range map[string]string{"": "", "dm": TypeDirectMessage, "group": TypeGroupChat, "SPACE": TypeSpace, "direct_message": TypeDirectMessage} {
		if got, err := SpaceTypeFilter(in); err != nil || got != want {
			t.Errorf("SpaceTypeFilter(%q) = %q, %v", in, got, err)
		}
	}
}

func TestFindDirectMessage(t *testing.T) {
	var gotName string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("name")
		if gotName == "users/nobody@digio.es" {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		if gotName == "users/boom@digio.es" {
			testutil.WriteGoogleError(w, 403, "forbidden", "denied")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
	})
	svc := newTestService(t, mux)
	ctx := context.Background()

	sp, err := FindDirectMessage(ctx, svc, "ana@digio.es")
	if err != nil || sp.Name != "spaces/DM1" || sp.Type != TypeDirectMessage || gotName != "users/ana@digio.es" {
		t.Fatalf("got %+v, %v (name %q)", sp, err, gotName)
	}
	if _, err := FindDirectMessage(ctx, svc, "users/ana@digio.es"); err != nil || gotName != "users/ana@digio.es" {
		t.Fatalf("users/ prefix: %v, %q", err, gotName)
	}
	_, err = FindDirectMessage(ctx, svc, "nobody@digio.es")
	if !errors.Is(err, ErrDirectMessageNotFound) || !strings.Contains(err.Error(), "nobody@digio.es") {
		t.Fatalf("not found: %v", err)
	}
	_, err = FindDirectMessage(ctx, svc, "boom@digio.es")
	if err == nil || errors.Is(err, ErrDirectMessageNotFound) {
		t.Fatalf("403: %v", err)
	}
	if _, err := FindDirectMessage(ctx, svc, " "); err == nil {
		t.Fatal("empty: want error")
	}
}

func TestListMessages(t *testing.T) {
	var q []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces/S/messages", func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query()
		q = append(q, map[string]string{"filter": v.Get("filter"), "orderBy": v.Get("orderBy"), "pageSize": v.Get("pageSize"), "pageToken": v.Get("pageToken")})
		m1 := apiMsg("spaces/S", "m1", "", testNow.Add(-time.Hour), "users/1")
		m1["formattedText"] = "*bold*"
		m1["attachment"] = []any{map[string]any{"name": "spaces/S/messages/m1/attachments/a", "contentName": "doc.pdf", "contentType": "application/pdf", "source": "DRIVE_FILE", "driveDataRef": map[string]any{"driveFileId": "F1"}}}
		if v.Get("pageToken") == "" {
			testutil.WriteJSON(t, w, map[string]any{"messages": []any{m1}, "nextPageToken": "n"})
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{apiMsg("spaces/S", "m2", "hi", testNow, "users/2"), apiMsg("spaces/S", "m3", "x", testNow, "users/2")}})
	})
	svc := newTestService(t, mux)
	win := timeutil.Window{From: testNow.Add(-7 * 24 * time.Hour), To: testNow}
	got, err := ListMessages(context.Background(), svc, "S", ListMessagesOptions{Window: win, Thread: "T", Max: 2, Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "spaces/S/messages/m1" || got[1].Name != "spaces/S/messages/m2" {
		t.Fatalf("got %+v", got)
	}
	m := got[0]
	if m.Text != "*bold*" || m.Space != "spaces/S" || m.Thread != "spaces/S/threads/m1" || m.Sender.Name != "users/1" || m.Sender.Type != "HUMAN" {
		t.Errorf("conversion: %+v", m)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ContentName != "doc.pdf" || m.Attachments[0].DriveFileID != "F1" {
		t.Errorf("attachments: %+v", m.Attachments)
	}
	if got[1].Attachments == nil {
		t.Error("attachments must be an empty slice, not nil")
	}
	wantFilter := `createTime > "2026-09-17T11:59:59Z" AND createTime < "2026-09-24T12:00:00Z" AND thread.name = spaces/S/threads/T`
	if q[0]["filter"] != wantFilter || q[0]["orderBy"] != "createTime desc" || q[0]["pageSize"] != "2" {
		t.Errorf("first request = %v", q[0])
	}
	if q[1]["pageToken"] != "n" || q[1]["pageSize"] != "1" || q[1]["filter"] != wantFilter {
		t.Errorf("second request = %v", q[1])
	}

	for _, bad := range []ListMessagesOptions{{Order: "sideways"}, {Thread: "spaces/OTHER/threads/T"}} {
		if _, err := ListMessages(context.Background(), svc, "S", bad); err == nil {
			t.Errorf("%+v: want error", bad)
		}
	}
}

func TestGetMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces/S/messages/M", func(w http.ResponseWriter, r *http.Request) {
		m := apiMsg("spaces/S", "M", "", testNow, "users/1")
		m["argumentText"] = "arg"
		testutil.WriteJSON(t, w, m)
	})
	svc := newTestService(t, mux)
	m, err := GetMessage(context.Background(), svc, "spaces/S/messages/M")
	if err != nil || m.Text != "arg" || !m.CreateTime.Equal(testNow) {
		t.Fatalf("got %+v, %v", m, err)
	}
	for _, bad := range []string{"M", "spaces/S", "spaces//messages/M", "spaces/S/threads/M"} {
		if _, err := GetMessage(context.Background(), svc, bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestNameResolver(t *testing.T) {
	var calls sync.Map
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		n, _ := calls.LoadOrStore(id, new(int))
		*(n.(*int))++
		switch id {
		case "A":
			testutil.WriteJSON(t, w, map[string]any{"memberships": []any{
				map[string]any{"name": "spaces/A/members/1", "member": map[string]any{"name": "users/1", "displayName": "Ana", "type": "HUMAN"}},
				map[string]any{"name": "spaces/A/members/2", "member": map[string]any{"name": "users/2", "type": "HUMAN"}},
			}})
		default:
			testutil.WriteGoogleError(w, 403, "forbidden", "no")
		}
	})
	svc := newTestService(t, mux)
	msgs := []Message{
		{Space: "spaces/A", Sender: User{Name: "users/1"}},
		{Space: "spaces/A", Sender: User{Name: "users/2"}},
		{Space: "spaces/A", Sender: User{Name: "users/1", DisplayName: "Kept"}},
		{Space: "spaces/B", Sender: User{Name: "users/1"}},
	}
	r := NewNameResolver(svc)
	r.Resolve(context.Background(), msgs)
	r.Resolve(context.Background(), msgs)
	want := []string{"Ana", "", "Kept", ""}
	for i, m := range msgs {
		if m.Sender.DisplayName != want[i] {
			t.Errorf("msg %d display name = %q, want %q", i, m.Sender.DisplayName, want[i])
		}
	}
	for _, id := range []string{"A", "B"} {
		n, _ := calls.Load(id)
		if n == nil || *(n.(*int)) != 1 {
			t.Errorf("space %s members listed %v times, want 1 (cached)", id, n)
		}
	}
	if r.Err("spaces/B") == nil || r.Err("spaces/A") != nil {
		t.Errorf("errs: A=%v B=%v", r.Err("spaces/A"), r.Err("spaces/B"))
	}
}

// searchFake serves spaces A, B and C with n messages each; message i of a
// space is created i minutes before testNow. Space FAIL returns 403.
func searchFake(t *testing.T, n int, texts map[string]string) (*http.ServeMux, *sync.Map) {
	t.Helper()
	filters := &sync.Map{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{
			map[string]any{"name": "spaces/A"}, map[string]any{"name": "spaces/B"}, map[string]any{"name": "spaces/C"},
		}})
	})
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "FAIL" {
			testutil.WriteGoogleError(w, 403, "forbidden", "denied")
			return
		}
		v := r.URL.Query()
		filters.Store(id, v.Get("filter")+"|"+v.Get("orderBy"))
		size, _ := strconv.Atoi(v.Get("pageSize"))
		start, _ := strconv.Atoi(v.Get("pageToken"))
		space := "spaces/" + id
		var msgs []any
		end := min(start+size, n)
		for i := start; i < end; i++ {
			mid := fmt.Sprintf("m%03d", i)
			text := texts[space+"/messages/"+mid]
			if text == "" {
				text = "noise " + mid
			}
			msgs = append(msgs, apiMsg(space, mid, text, testNow.Add(-time.Duration(i)*time.Minute), "users/1"))
		}
		resp := map[string]any{"messages": msgs}
		if end < n {
			resp["nextPageToken"] = strconv.Itoa(end)
		}
		testutil.WriteJSON(t, w, resp)
	})
	return mux, filters
}

func TestSearchMessages(t *testing.T) {
	texts := map[string]string{
		"spaces/A/messages/m001": "The Quick brown FOX",
		"spaces/B/messages/m001": "fox quick",  // same time as A/m001: tie by name
		"spaces/C/messages/m000": "quick fox!", // newest
		"spaces/C/messages/m002": "only quick",
	}
	texts["spaces/A/messages/m009"] = "quick\nattached"
	mux, filters := searchFake(t, 10, texts)
	svc := newTestService(t, mux)
	win := timeutil.Window{From: testNow.Add(-7 * 24 * time.Hour)}

	res, err := SearchMessages(context.Background(), svc, SearchOptions{Text: "quick  FOX", Window: win})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range res.Matches {
		names = append(names, m.Name)
	}
	want := "spaces/C/messages/m000,spaces/A/messages/m001,spaces/B/messages/m001"
	if strings.Join(names, ",") != want {
		t.Errorf("matches = %v, want %s", names, want)
	}
	if res.Scanned != 30 || res.SpacesScanned != 3 || res.SpacesTotal != 3 || res.CapReached || res.TotalMatches != 3 {
		t.Errorf("result = %+v", res)
	}
	if res.FailedSpaces == nil {
		t.Error("failed_spaces must be an empty slice")
	}
	f, _ := filters.Load("A")
	if f != `createTime > "2026-09-17T11:59:59Z"|createTime desc` {
		t.Errorf("filter = %v", f)
	}

	// Max limits returned matches but not the count.
	res, err = SearchMessages(context.Background(), svc, SearchOptions{Text: "quick", Max: 2, Window: win})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 2 || res.TotalMatches != 5 {
		t.Errorf("max: %d matches, total %d", len(res.Matches), res.TotalMatches)
	}
}

func TestSearchMessagesCap(t *testing.T) {
	mux, _ := searchFake(t, 400, map[string]string{"spaces/A/messages/m000": "needle"})
	svc := newTestService(t, mux)
	res, err := SearchMessages(context.Background(), svc, SearchOptions{Text: "needle", MaxScan: 450, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 450 || !res.CapReached {
		t.Errorf("scanned %d cap %v, want 450 true", res.Scanned, res.CapReached)
	}

	// Exactly enough budget: no cap.
	res, err = SearchMessages(context.Background(), svc, SearchOptions{Text: "needle", MaxScan: 1200, Spaces: []string{"A", "spaces/B", "B", "C"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 1200 || res.CapReached || res.SpacesTotal != 3 || len(res.Matches) != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestSearchMessagesFailures(t *testing.T) {
	mux, _ := searchFake(t, 3, map[string]string{"spaces/A/messages/m000": "hit"})
	svc := newTestService(t, mux)
	res, err := SearchMessages(context.Background(), svc, SearchOptions{Text: "hit", Spaces: []string{"A", "FAIL"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.SpacesScanned != 1 || len(res.FailedSpaces) != 1 || res.FailedSpaces[0].Space != "spaces/FAIL" {
		t.Errorf("result = %+v", res)
	}
	if _, err := SearchMessages(context.Background(), svc, SearchOptions{Text: "hit", Spaces: []string{"FAIL"}}); err == nil {
		t.Error("all spaces failed: want error")
	}
	if _, err := SearchMessages(context.Background(), svc, SearchOptions{Text: "  "}); err == nil {
		t.Error("empty text: want error")
	}
}
