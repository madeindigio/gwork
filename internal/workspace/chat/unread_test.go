package chat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/madeindigio/gwork/internal/testutil"
)

// unreadFake serves three spaces: A (read an hour ago, two newer messages
// and one older in the same second as the read position), B (fully read)
// and C (never read). Section "fav" (display name "Favorites") holds C, A
// and a conversation missing from spaces.list.
type unreadFake struct {
	mu      sync.Mutex
	filters map[string]string
	listed  []string
}

func newUnreadFake(t *testing.T) (*unreadFake, *http.ServeMux) {
	t.Helper()
	f := &unreadFake{filters: map[string]string{}}
	lastRead := testNow.Add(-time.Hour).Add(500 * time.Millisecond)
	spaces := map[string]map[string]any{
		"A": {"name": "spaces/A", "displayName": "Team", "spaceType": "SPACE", "lastActiveTime": testNow.Add(-time.Minute).Format(time.RFC3339)},
		"B": {"name": "spaces/B", "spaceType": "DIRECT_MESSAGE", "lastActiveTime": testNow.Add(-2 * time.Hour).Format(time.RFC3339)},
		"C": {"name": "spaces/C", "spaceType": "GROUP_CHAT", "lastActiveTime": testNow.Add(-3 * time.Hour).Format(time.RFC3339)},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		out := []any{}
		for _, id := range []string{"A", "B", "C"} {
			if ft := r.URL.Query().Get("filter"); ft == "" || strings.Contains(ft, spaces[id]["spaceType"].(string)) {
				out = append(out, spaces[id])
			}
		}
		testutil.WriteJSON(t, w, map[string]any{"spaces": out})
	})
	mux.HandleFunc("GET /v1/spaces/{id}", func(w http.ResponseWriter, r *http.Request) {
		sp, ok := spaces[r.PathValue("id")]
		if !ok {
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		}
		testutil.WriteJSON(t, w, sp)
	})
	mux.HandleFunc("GET /v1/users/me/spaces/{id}/spaceReadState", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		rs := map[string]any{"name": "users/me/spaces/" + id + "/spaceReadState"}
		switch id {
		case "A":
			rs["lastReadTime"] = lastRead.Format(time.RFC3339Nano)
		case "B":
			rs["lastReadTime"] = testNow.Format(time.RFC3339Nano)
		}
		testutil.WriteJSON(t, w, rs)
	})
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		f.filters[id] = r.URL.Query().Get("filter") + "|" + r.URL.Query().Get("orderBy")
		f.listed = append(f.listed, id)
		f.mu.Unlock()
		sp := "spaces/" + id
		msgs := []any{
			apiMsg(sp, "m3", "newest", testNow.Add(-time.Minute), "users/1"),
			apiMsg(sp, "m2", "newer", testNow.Add(-30*time.Minute), "users/1"),
			// Same second as the read position, but before it.
			apiMsg(sp, "m1", "read", testNow.Add(-time.Hour).Add(100*time.Millisecond), "users/1"),
		}
		testutil.WriteJSON(t, w, map[string]any{"messages": msgs})
	})
	mux.HandleFunc("GET /v1/users/me/sections", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"sections": []any{
			map[string]any{"name": "users/9/sections/fav", "displayName": "Favorites", "type": "CUSTOM_SECTION", "sortOrder": 1},
			map[string]any{"name": "users/9/sections/default-spaces", "type": "DEFAULT_SPACES", "sortOrder": 2},
		}})
	})
	mux.HandleFunc("GET /v1/users/9/sections/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		items := []any{}
		if r.PathValue("id") == "fav" {
			for _, sp := range []string{"spaces/C", "spaces/A", "spaces/EMPTY"} {
				items = append(items, map[string]any{"name": "users/9/sections/fav/items/x", "space": sp})
			}
		}
		testutil.WriteJSON(t, w, map[string]any{"sectionItems": items})
	})
	return f, mux
}

func TestListSectionsAndFind(t *testing.T) {
	_, mux := newUnreadFake(t)
	svc := newTestService(t, mux)
	ctx := context.Background()
	got, err := ListSections(ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].DisplayName != "Favorites" || got[0].Type != SectionCustom || got[1].SortOrder != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, in := range []string{"favorites", "fav", "users/9/sections/fav"} {
		s, err := FindSection(ctx, svc, in)
		if err != nil || s.Name != "users/9/sections/fav" {
			t.Errorf("FindSection(%q) = %+v, %v", in, s, err)
		}
	}
	if s, err := FindSection(ctx, svc, "default-spaces"); err != nil || s.Type != SectionDefaultSpaces {
		t.Errorf("default-spaces = %+v, %v", s, err)
	}
	for _, in := range []string{"", "nope"} {
		if _, err := FindSection(ctx, svc, in); err == nil {
			t.Errorf("FindSection(%q): want error", in)
		}
	}
}

func TestListSpacesBySection(t *testing.T) {
	_, mux := newUnreadFake(t)
	svc := newTestService(t, mux)
	ctx := context.Background()
	names := func(spaces []Space) string {
		var out []string
		for _, s := range spaces {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		name string
		o    ListSpacesOptions
		want string
	}{
		{"section order, unknown kept", ListSpacesOptions{Section: "Favorites"}, "spaces/C,spaces/A,spaces/EMPTY"},
		{"type", ListSpacesOptions{Section: "fav", Type: "space"}, "spaces/A"},
		{"max", ListSpacesOptions{Section: "fav", Max: 1}, "spaces/C"},
		{"empty section", ListSpacesOptions{Section: "default-spaces"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ListSpaces(ctx, svc, tt.o)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || names(got) != tt.want {
				t.Errorf("got %q (%v), want %q", names(got), got, tt.want)
			}
		})
	}
	got, _ := ListSpaces(ctx, svc, ListSpacesOptions{Section: "fav"})
	if got[1].DisplayName != "Team" {
		t.Errorf("section spaces must carry details: %+v", got[1])
	}
	if _, err := ListSpaces(ctx, svc, ListSpacesOptions{Section: "nope"}); err == nil {
		t.Error("unknown section: want error")
	}
}

func TestListUnread(t *testing.T) {
	f, mux := newUnreadFake(t)
	svc := newTestService(t, mux)
	res, err := ListUnread(context.Background(), svc, UnreadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpacesTotal != 3 || res.SpacesChecked != 3 || len(res.FailedSpaces) != 0 || len(res.Spaces) != 2 {
		t.Fatalf("got %+v", res)
	}
	a, c := res.Spaces[0], res.Spaces[1]
	if a.Space.Name != "spaces/A" || a.Space.DisplayName != "Team" || len(a.Messages) != 2 || a.More ||
		a.Messages[0].Text != "newest" || a.Messages[1].Text != "newer" {
		t.Errorf("A = %+v", a)
	}
	if !a.LastReadTime.Equal(testNow.Add(-time.Hour).Add(500 * time.Millisecond)) {
		t.Errorf("A last read = %v", a.LastReadTime)
	}
	// Never read: every message is unread and there is no filter.
	if c.Space.Name != "spaces/C" || !c.LastReadTime.IsZero() || len(c.Messages) != 3 {
		t.Errorf("C = %+v", c)
	}
	if got := f.filters["A"]; got != `createTime > "2026-09-24T11:00:00Z"|createTime desc` {
		t.Errorf("A filter = %q", got)
	}
	if got := f.filters["C"]; got != "|createTime desc" {
		t.Errorf("C filter = %q", got)
	}
	// B has no activity after the read position: its messages are not listed.
	if _, ok := f.filters["B"]; ok {
		t.Error("messages of the read space B must not be listed")
	}
}

func TestListUnreadOptions(t *testing.T) {
	_, mux := newUnreadFake(t)
	svc := newTestService(t, mux)
	ctx := context.Background()

	res, err := ListUnread(ctx, svc, UnreadOptions{Spaces: []string{"C", "spaces/C"}, MaxPerSpace: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpacesTotal != 1 || len(res.Spaces) != 1 || len(res.Spaces[0].Messages) != 2 || !res.Spaces[0].More ||
		res.Spaces[0].Space.Type != TypeGroupChat {
		t.Errorf("explicit space = %+v", res)
	}

	res, err = ListUnread(ctx, svc, UnreadOptions{Section: "Favorites", Type: "space"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpacesTotal != 1 || len(res.Spaces) != 1 || res.Spaces[0].Space.Name != "spaces/A" {
		t.Errorf("section+type = %+v", res)
	}

	res, err = ListUnread(ctx, svc, UnreadOptions{Type: "dm"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpacesChecked != 1 || res.Spaces == nil || len(res.Spaces) != 0 {
		t.Errorf("dm = %+v", res)
	}
	if _, err := ListUnread(ctx, svc, UnreadOptions{Spaces: []string{"a/b"}}); err == nil {
		t.Error("invalid space: want error")
	}
}

func TestListUnreadFailures(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{
			map[string]any{"name": "spaces/A", "spaceType": "SPACE"},
			map[string]any{"name": "spaces/B", "spaceType": "SPACE"},
		}})
	})
	allFail := true
	mux.HandleFunc("GET /v1/users/me/spaces/{id}/spaceReadState", func(w http.ResponseWriter, r *http.Request) {
		if allFail || r.PathValue("id") == "B" {
			testutil.WriteGoogleError(w, 403, "insufficientPermissions", "Request had insufficient authentication scopes.")
			return
		}
		testutil.WriteJSON(t, w, map[string]any{"lastReadTime": testNow.Format(time.RFC3339)})
	})
	mux.HandleFunc("GET /v1/spaces/{id}/messages", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{})
	})
	svc := newTestService(t, mux)

	// Every space failed: the first Google error is returned for classification.
	_, err := ListUnread(context.Background(), svc, UnreadOptions{Concurrency: 1})
	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code != 403 || !strings.Contains(err.Error(), "get read state of spaces/A") {
		t.Fatalf("err = %v", err)
	}

	allFail = false
	res, err := ListUnread(context.Background(), svc, UnreadOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.SpacesChecked != 1 || len(res.FailedSpaces) != 1 || res.FailedSpaces[0].Space != "spaces/B" || len(res.Spaces) != 0 {
		t.Errorf("partial = %+v", res)
	}
}
