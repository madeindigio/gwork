package drive

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestSearch(t *testing.T) {
	fd := newFakeDrive(t)
	fd.list = []map[string]any{
		{"nextPageToken": "1", "files": []any{
			meta("d1", "Plan", MimeDoc, "size", "0"),
			meta("p1", "Report.pdf", "application/pdf", "size", "2048", "driveId", "SD1"),
		}},
		{"files": []any{meta("s1", "Budget", MimeSheet)}},
	}
	got, err := Search(context.Background(), SearchOptions{Name: "O'Neil", Max: 10}, fd.options()...)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(got), got)
	}
	p := got[1]
	if p.ID != "p1" || p.Type != "pdf" || p.Size != 2048 || p.DriveID != "SD1" ||
		!slices.Equal(p.Owners, []string{"owner@digio.es"}) || !slices.Equal(p.Parents, []string{"root"}) ||
		!p.ModifiedTime.Equal(time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)) || p.WebViewLink == "" {
		t.Errorf("unexpected summary: %+v", p)
	}
	if got[0].Type != "doc" || got[2].Type != "sheet" {
		t.Errorf("types = %s, %s", got[0].Type, got[2].Type)
	}

	reqs := fd.requestsTo("/files")
	if len(reqs) != 2 {
		t.Fatalf("list requests = %d, want 2", len(reqs))
	}
	q := reqs[0].Query()
	for k, want := range map[string]string{
		"supportsAllDrives":         "true",
		"includeItemsFromAllDrives": "true",
		"corpora":                   "allDrives",
		"q":                         `trashed = false and name contains 'O\'Neil'`,
		"orderBy":                   "modifiedTime desc",
		"pageSize":                  "10",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("param %s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(q.Get("fields"), "driveId") {
		t.Errorf("fields = %q", q.Get("fields"))
	}
	if reqs[1].Query().Get("pageToken") != "1" || reqs[1].Query().Get("pageSize") != "8" {
		t.Errorf("second page params = %v", reqs[1].Query())
	}
}

func TestSearchMaxStopsPaging(t *testing.T) {
	fd := newFakeDrive(t)
	fd.list = []map[string]any{
		{"nextPageToken": "1", "files": []any{meta("a", "A", "text/plain"), meta("b", "B", "text/plain")}},
		{"files": []any{meta("c", "C", "text/plain")}},
	}
	got, err := Search(context.Background(), SearchOptions{Max: 2}, fd.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(fd.requestsTo("/files")) != 1 {
		t.Fatalf("got %d files after %d requests", len(got), len(fd.requestsTo("/files")))
	}
}

func TestSearchFullTextHasNoDefaultOrder(t *testing.T) {
	fd := newFakeDrive(t)
	got, err := Search(context.Background(), SearchOptions{Text: "hello"}, fd.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", got)
	}
	data, _ := json.Marshal(got)
	if string(data) != "[]" {
		t.Errorf("json = %s", data)
	}
	q := fd.requestsTo("/files")[0].Query()
	if q.Has("orderBy") {
		t.Errorf("orderBy = %q, want unset for full-text search", q.Get("orderBy"))
	}
	if q.Get("pageSize") != "25" {
		t.Errorf("pageSize = %s, want default 25", q.Get("pageSize"))
	}
}

func TestSearchRawFullTextHasNoDefaultOrder(t *testing.T) {
	fd := newFakeDrive(t)
	if _, err := Search(context.Background(), SearchOptions{RawQuery: "fullText contains 'budget'"}, fd.options()...); err != nil {
		t.Fatal(err)
	}
	q := fd.requestsTo("/files")[0].Query()
	if q.Has("orderBy") {
		t.Errorf("orderBy = %q, want unset for a raw full-text query", q.Get("orderBy"))
	}
}

func TestSearchInvalidOptions(t *testing.T) {
	fd := newFakeDrive(t)
	if _, err := Search(context.Background(), SearchOptions{Type: "nope"}, fd.options()...); err == nil {
		t.Fatal("want error")
	}
	if len(fd.requestsTo("/files")) != 0 {
		t.Error("no request expected")
	}
}

func TestGetFile(t *testing.T) {
	fd := newFakeDrive(t)
	fd.files["d1"] = fakeFile{meta: meta("d1", "Plan", MimeDoc,
		"description", "Q3 plan",
		"createdTime", "2026-01-02T03:04:05Z",
		"lastModifyingUser", map[string]any{"emailAddress": "ed@digio.es", "displayName": "Ed"},
		"shared", true,
		"exportLinks", map[string]any{"text/plain": "x", "application/pdf": "y"},
	)}
	f, err := GetFile(context.Background(), "d1", fd.options()...)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if f.Name != "Plan" || f.Description != "Q3 plan" || f.LastModifyingUser != "ed@digio.es" || !f.Shared ||
		!f.CreatedTime.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) ||
		!slices.Equal(f.ExportLinks, []string{"application/pdf", "text/plain"}) {
		t.Errorf("unexpected file: %+v", f)
	}
	q := fd.requestsTo("/files/d1")[0].Query()
	if q.Get("supportsAllDrives") != "true" || !strings.Contains(q.Get("fields"), "exportLinks") {
		t.Errorf("params = %v", q)
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"mime_type"`, `"modified_time"`, `"created_time"`, `"last_modifying_user"`, `"export_links"`, `"web_view_link"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("json lacks %s: %s", key, data)
		}
	}
}

func TestGetFileNotFound(t *testing.T) {
	fd := newFakeDrive(t)
	_, err := GetFile(context.Background(), "missing", fd.options()...)
	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code != 404 {
		t.Fatalf("err = %v, want wrapped 404", err)
	}
	if !strings.Contains(err.Error(), "get drive file missing") {
		t.Errorf("err lacks context: %v", err)
	}
}
