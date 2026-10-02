package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/testutil"
)

// addChatUnreadFake adds sections and read states to the chat fake: section
// "Favorites" holds space A; A was read before its two messages and B
// is fully read. With denied set, read states answer 403 (missing scope).
func addChatUnreadFake(t *testing.T, mux *http.ServeMux, denied bool) {
	t.Helper()
	mux.HandleFunc("GET /v1/users/me/sections", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"sections": []any{
			map[string]any{"name": "users/9/sections/fav", "displayName": "Favorites", "type": "CUSTOM_SECTION"},
			map[string]any{"name": "users/9/sections/default-spaces", "type": "DEFAULT_SPACES"},
		}})
	})
	mux.HandleFunc("GET /v1/users/9/sections/fav/items", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"sectionItems": []any{map[string]any{"space": "spaces/A"}}})
	})
	mux.HandleFunc("GET /v1/users/me/spaces/{id}/spaceReadState", func(w http.ResponseWriter, r *http.Request) {
		if denied {
			testutil.WriteGoogleError(w, 403, "insufficientPermissions", "Request had insufficient authentication scopes.")
			return
		}
		last := testNow
		if r.PathValue("id") == "A" {
			last = testNow.Add(-150 * time.Minute)
		}
		testutil.WriteJSON(t, w, map[string]any{"lastReadTime": last.Format(time.RFC3339)})
	})
}

func TestChatSectionsCLI(t *testing.T) {
	_, mux := newChatCLIFake(t)
	addChatUnreadFake(t, mux, false)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "sections")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"NAME", "users/9/sections/fav", "CUSTOM_SECTION", "Favorites", "DEFAULT_SPACES"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, errOut, code = runCLI(t, p, "chat", "spaces", "--section", "favorites", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var spaces []map[string]any
	if err := json.Unmarshal([]byte(out), &spaces); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(spaces) != 1 || spaces[0]["name"] != "spaces/A" || spaces[0]["display_name"] != "Team A" {
		t.Errorf("json = %v", spaces)
	}

	_, errOut, code = runCLI(t, p, "chat", "spaces", "--section", "nope")
	if code != 1 || !strings.Contains(errOut, `no chat section matches "nope"`) {
		t.Errorf("unknown section: code %d, %s", code, errOut)
	}
}

func TestChatUnreadCLI(t *testing.T) {
	f, mux := newChatCLIFake(t)
	addChatUnreadFake(t, mux, false)
	p := testutil.NewFakeProvider(t, mux)

	out, errOut, code := runCLI(t, p, "chat", "unread")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{
		"== Team A (spaces/A)  2 unread, last read 2026-09-24 09:30",
		"2026-09-24 11:00  Ana (users/1)  [spaces/A/messages/m1]",
		"  Deploy done in A",
		"  lunch?",
		"1 spaces with unread messages; checked 2/2 spaces",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "spaces/B") {
		t.Errorf("the read space B must not be listed:\n%s", out)
	}
	if got := f.get("A.filter"); len(got) != 1 || got[0] != `createTime > "2026-09-24T09:30:00Z"` {
		t.Errorf("filter = %q", got)
	}

	out, _, code = runCLI(t, p, "chat", "unread", "--section", "Favorites", "--json", "--no-resolve-names")
	if code != 0 {
		t.Fatal(code)
	}
	var res struct {
		Spaces []struct {
			Space    map[string]any   `json:"space"`
			Messages []map[string]any `json:"messages"`
			More     bool             `json:"more"`
		} `json:"spaces"`
		SpacesTotal  int   `json:"spaces_total"`
		FailedSpaces []any `json:"failed_spaces"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.SpacesTotal != 1 || len(res.Spaces) != 1 || len(res.Spaces[0].Messages) != 2 || res.FailedSpaces == nil ||
		res.Spaces[0].Space["name"] != "spaces/A" {
		t.Errorf("json = %s", out)
	}
}

func TestChatUnreadCLIMissingScope(t *testing.T) {
	_, mux := newChatCLIFake(t)
	addChatUnreadFake(t, mux, true)
	p := testutil.NewFakeProvider(t, mux)

	_, errOut, code := runCLI(t, p, "chat", "unread")
	if code != 1 || !strings.Contains(errOut, "missing OAuth scope") ||
		!strings.Contains(errOut, "hint: run: gwork auth login --services chat") {
		t.Errorf("code %d, %s", code, errOut)
	}
}
