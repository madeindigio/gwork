package testutil_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
	"github.com/madeindigio/gwork/internal/workspace/calendar"
	"github.com/madeindigio/gwork/internal/workspace/chat"
	"github.com/madeindigio/gwork/internal/workspace/drive"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

// TestFakeGoogleServesEveryAPI documents the request paths each generated
// client uses against FakeGoogle.
func TestFakeGoogleServesEveryAPI(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("fake clients must not send credentials")
		}
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{map[string]any{"id": "INBOX", "name": "INBOX"}}})
	})
	// Calendar's base path already contains /calendar/v3/, so paths are relative to it.
	mux.HandleFunc("GET /users/me/calendarList", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"items": []any{map[string]any{"id": "primary"}}})
	})
	// Same for Drive: /drive/v3/ is part of the base path.
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"files": []any{map[string]any{"id": "f1"}}})
	})
	mux.HandleFunc("GET /v1/spaces", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"spaces": []any{map[string]any{"name": "spaces/AAA"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/missing", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteGoogleError(w, http.StatusNotFound, "notFound", "Requested entity was not found.")
	})

	p := testutil.NewFakeProvider(t, mux)

	opts, err := p.ClientOptions(ctx, auth.Gmail)
	if err != nil {
		t.Fatal(err)
	}
	gs, err := gmail.New(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := gs.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		t.Fatal(err)
	}
	if len(labels.Labels) != 1 || labels.Labels[0].Id != "INBOX" {
		t.Fatalf("labels %+v", labels.Labels)
	}
	_, err = gs.Users.Messages.Get("me", "missing").Context(ctx).Do()
	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code != http.StatusNotFound {
		t.Fatalf("expected googleapi 404, got %v", err)
	}
	if !errors.Is(auth.ClassifyService(err, auth.Gmail), auth.ErrNotFound) {
		t.Fatal("404 should classify as ErrNotFound")
	}

	cs, err := calendar.New(ctx, p.Options...)
	if err != nil {
		t.Fatal(err)
	}
	if cl, err := cs.CalendarList.List().Context(ctx).Do(); err != nil || len(cl.Items) != 1 {
		t.Fatalf("calendar list: %v %+v", err, cl)
	}

	ds, err := drive.New(ctx, p.Options...)
	if err != nil {
		t.Fatal(err)
	}
	if fl, err := ds.Files.List().Context(ctx).Do(); err != nil || len(fl.Files) != 1 {
		t.Fatalf("drive list: %v %+v", err, fl)
	}

	chs, err := chat.New(ctx, p.Options...)
	if err != nil {
		t.Fatal(err)
	}
	if sl, err := chs.Spaces.List().Context(ctx).Do(); err != nil || len(sl.Spaces) != 1 {
		t.Fatalf("chat list: %v %+v", err, sl)
	}
}

func TestFakeProviderScopeCheck(t *testing.T) {
	p := testutil.NewFakeProvider(t, http.NotFoundHandler(), auth.Gmail)
	if _, err := p.ClientOptions(context.Background(), auth.Drive); !errors.Is(err, auth.ErrInsufficientScope) {
		t.Fatalf("expected ErrInsufficientScope, got %v", err)
	}
	if p.Account() != "tester@digio.es" {
		t.Fatalf("account %q", p.Account())
	}
}
