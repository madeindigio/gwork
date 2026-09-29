package gmail

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madeindigio/gwork/internal/testutil"
)

// fakeMessage returns a metadata/full message JSON object.
func fakeMessage(id string, payload map[string]any) map[string]any {
	return map[string]any{
		"id":           id,
		"threadId":     "t-" + id,
		"labelIds":     []string{"INBOX", "UNREAD"},
		"snippet":      "snippet " + id,
		"internalDate": "1790000000000",
		"payload":      payload,
	}
}

func headers(kv ...string) []any {
	var hs []any
	for i := 0; i+1 < len(kv); i += 2 {
		hs = append(hs, map[string]any{"name": kv[i], "value": kv[i+1]})
	}
	return hs
}

func TestSearchPaginationAndMetadata(t *testing.T) {
	var (
		mu        sync.Mutex
		listCalls []string
		inFlight  atomic.Int32
		maxFlight atomic.Int32
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		listCalls = append(listCalls, fmt.Sprintf("q=%s max=%s page=%s spam=%s", q.Get("q"), q.Get("maxResults"), q.Get("pageToken"), q.Get("includeSpamTrash")))
		mu.Unlock()
		switch q.Get("pageToken") {
		case "":
			testutil.WriteJSON(t, w, map[string]any{
				"messages":      []any{map[string]any{"id": "m1"}, map[string]any{"id": "m2"}},
				"nextPageToken": "p2",
			})
		case "p2":
			testutil.WriteJSON(t, w, map[string]any{
				"messages":      []any{map[string]any{"id": "m3"}, map[string]any{"id": "m4"}},
				"nextPageToken": "p3",
			})
		default:
			t.Errorf("unexpected page token %q", q.Get("pageToken"))
		}
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxFlight.Load()
			if n <= m || maxFlight.CompareAndSwap(m, n) {
				break
			}
		}
		q := r.URL.Query()
		if q.Get("format") != "metadata" {
			t.Errorf("format = %q, want metadata", q.Get("format"))
		}
		if got := q["metadataHeaders"]; !slices.Equal(got, []string{"From", "To", "Subject", "Date"}) {
			t.Errorf("metadataHeaders = %v", got)
		}
		id := r.PathValue("id")
		// Make earlier messages slower to check that order is preserved.
		if id == "m1" {
			time.Sleep(20 * time.Millisecond)
		}
		testutil.WriteJSON(t, w, fakeMessage(id, map[string]any{
			"headers": headers("From", "Alice <alice@digio.es>", "To", "bob@digio.es",
				"Subject", "Subject "+id, "Date", "Wed, 23 Sep 2026 10:30:00 +0200"),
		}))
	})

	got, err := Search(context.Background(), "from:alice", SearchOptions{MaxResults: 3, IncludeSpamTrash: true}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	wantCalls := []string{
		"q=from:alice max=3 page= spam=true",
		"q=from:alice max=1 page=p2 spam=true",
	}
	if !slices.Equal(listCalls, wantCalls) {
		t.Errorf("list calls = %q, want %q", listCalls, wantCalls)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	for i, id := range []string{"m1", "m2", "m3"} {
		if got[i].ID != id {
			t.Errorf("result %d id = %q, want %q", i, got[i].ID, id)
		}
	}
	want := MessageSummary{
		ID:       "m1",
		ThreadID: "t-m1",
		From:     "Alice <alice@digio.es>",
		To:       "bob@digio.es",
		Subject:  "Subject m1",
		Date:     time.Date(2026, 9, 23, 8, 30, 0, 0, time.UTC),
		Snippet:  "snippet m1",
		Labels:   []string{"INBOX", "UNREAD"},
	}
	if fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Errorf("summary = %+v\nwant      %+v", got[0], want)
	}
	if maxFlight.Load() > metadataConcurrency {
		t.Errorf("max concurrent metadata requests = %d", maxFlight.Load())
	}
}

func TestSearchEmptyAndDefaults(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("maxResults"); got != "20" {
			t.Errorf("maxResults = %q, want default 20", got)
		}
		testutil.WriteJSON(t, w, map[string]any{"resultSizeEstimate": 0})
	})
	got, err := Search(context.Background(), "nothing", SearchOptions{}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %#v, want empty non-nil slice", got)
	}
}

func TestSearchMetadataError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{map[string]any{"id": "bad"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteGoogleError(w, 403, "forbidden", "Forbidden.")
	})
	_, err := Search(context.Background(), "x", SearchOptions{}, testutil.FakeGoogle(t, mux)...)
	if err == nil || !strings.Contains(err.Error(), "get message bad metadata") {
		t.Errorf("err = %v", err)
	}
}

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = old })
}

func TestSearchSkipsVanishedAndRetriesTransient(t *testing.T) {
	fastRetries(t)
	var flaky atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{
			map[string]any{"id": "m1"}, map[string]any{"id": "gone"}, map[string]any{"id": "flaky"},
		}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		switch {
		case id == "gone":
			testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
			return
		case id == "flaky" && flaky.Add(1) == 1:
			testutil.WriteGoogleError(w, 429, "rateLimitExceeded", "Too many requests.")
			return
		case id == "flaky" && flaky.Load() == 2:
			testutil.WriteGoogleError(w, 503, "backendError", "Backend error.")
			return
		}
		testutil.WriteJSON(t, w, fakeMessage(id, map[string]any{"headers": headers("Subject", "S "+id)}))
	})
	got, err := Search(context.Background(), "x", SearchOptions{}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "flaky" {
		t.Fatalf("got %+v, want m1 and flaky", got)
	}
	if n := flaky.Load(); n != 3 {
		t.Errorf("flaky attempts = %d, want 3", n)
	}
}

func TestSearchGivesUpAfterRetries(t *testing.T) {
	fastRetries(t)
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"messages": []any{map[string]any{"id": "busy"}}})
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		testutil.WriteGoogleError(w, 500, "backendError", "Backend error.")
	})
	_, err := Search(context.Background(), "x", SearchOptions{}, testutil.FakeGoogle(t, mux)...)
	if err == nil || !strings.Contains(err.Error(), "get message busy metadata") {
		t.Errorf("err = %v", err)
	}
	if n := calls.Load(); n != metadataAttempts {
		t.Errorf("attempts = %d, want %d", n, metadataAttempts)
	}
}

// fullPayload is a multipart/mixed message with a text body and a PDF.
func fullPayload(text string) map[string]any {
	return map[string]any{
		"mimeType": "multipart/mixed",
		"headers": headers("From", "Alice <alice@digio.es>", "To", "bob@digio.es", "Cc", "carol@digio.es",
			"Subject", "Quarterly report", "Date", "Wed, 23 Sep 2026 10:30:00 +0000", "Message-ID", "<abc@mail.test>"),
		"parts": []any{
			map[string]any{"mimeType": "text/plain", "headers": headers("Content-Type", "text/plain; charset=UTF-8"),
				"body": map[string]any{"data": b64(text), "size": len(text)}},
			map[string]any{"mimeType": "application/pdf", "filename": "q3.pdf",
				"body": map[string]any{"attachmentId": "att1", "size": 2048}},
		},
	}
}

func TestGetMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "full" {
			t.Errorf("format = %q, want full", r.URL.Query().Get("format"))
		}
		testutil.WriteJSON(t, w, fakeMessage(r.PathValue("id"), fullPayload("Numbers attached.")))
	})
	m, err := GetMessage(context.Background(), "m1", GetOptions{}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if m.ID != "m1" || m.Cc != "carol@digio.es" || m.MessageID != "<abc@mail.test>" || m.Subject != "Quarterly report" {
		t.Errorf("headers = %+v", m)
	}
	if m.Body != "Numbers attached." || m.BodySource != "text/plain" {
		t.Errorf("body = %q (%s)", m.Body, m.BodySource)
	}
	if len(m.Attachments) != 1 || m.Attachments[0] != (Attachment{AttachmentID: "att1", Filename: "q3.pdf", MimeType: "application/pdf", Size: 2048}) {
		t.Errorf("attachments = %+v", m.Attachments)
	}
}

func TestGetMessageNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteGoogleError(w, 404, "notFound", "Requested entity was not found.")
	})
	_, err := GetMessage(context.Background(), "nope", GetOptions{}, testutil.FakeGoogle(t, mux)...)
	if err == nil || !strings.Contains(err.Error(), "get message nope") {
		t.Errorf("err = %v", err)
	}
}

func TestGetThread(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "t1" || r.URL.Query().Get("format") != "full" {
			t.Errorf("unexpected request %s", r.URL)
		}
		testutil.WriteJSON(t, w, map[string]any{
			"id": "t1",
			"messages": []any{
				fakeMessage("m1", fullPayload("first")),
				fakeMessage("m2", map[string]any{
					"mimeType": "text/html",
					"headers":  headers("From", "bob@digio.es", "Subject", "Re: Quarterly report"),
					"body":     map[string]any{"data": b64("<p>second</p>")},
				}),
			},
		})
	})
	th, err := GetThread(context.Background(), "t1", GetOptions{IncludeHTML: true}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if th.ID != "t1" || len(th.Messages) != 2 {
		t.Fatalf("thread = %+v", th)
	}
	if th.Messages[0].Body != "first" || th.Messages[1].Body != "second" || th.Messages[1].HTML != "<p>second</p>" {
		t.Errorf("bodies = %q, %q (html %q)", th.Messages[0].Body, th.Messages[1].Body, th.Messages[1].HTML)
	}
	// No Date header: falls back to internalDate.
	if !th.Messages[1].Date.Equal(time.UnixMilli(1790000000000)) {
		t.Errorf("date = %v", th.Messages[1].Date)
	}
}

func TestGetMessageOutOfLineBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, fakeMessage("m1", map[string]any{
			"mimeType": "text/plain",
			"body":     map[string]any{"attachmentId": "body1", "size": 100000},
		}))
	})
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}/attachments/{att}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("att") != "body1" {
			t.Errorf("att = %q", r.PathValue("att"))
		}
		testutil.WriteJSON(t, w, map[string]any{"data": b64("big body"), "size": 8})
	})
	m, err := GetMessage(context.Background(), "m1", GetOptions{}, testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if m.Body != "big body" {
		t.Errorf("body = %q", m.Body)
	}
}

func TestGetAttachment(t *testing.T) {
	payload := "%PDF-1.4\x00\xff binary"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/messages/{id}/attachments/{att}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "m1" || r.PathValue("att") != "att1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		testutil.WriteJSON(t, w, map[string]any{"data": b64(payload), "size": len(payload)})
	})
	got, err := GetAttachment(context.Background(), "m1", "att1", testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("data = %q", got)
	}
}

func TestListLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{
			map[string]any{"id": "Label_2", "name": "zeta", "type": "user"},
			map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"},
			map[string]any{"id": "Label_1", "name": "Alpha", "type": "user"},
			map[string]any{"id": "SENT", "name": "SENT", "type": "system"},
		}})
	})
	got, err := ListLabels(context.Background(), testutil.FakeGoogle(t, mux)...)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range got {
		ids = append(ids, l.ID)
	}
	if want := []string{"INBOX", "SENT", "Label_1", "Label_2"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestListLabelsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{})
	})
	got, err := ListLabels(context.Background(), testutil.FakeGoogle(t, mux)...)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %#v, %v; want empty slice", got, err)
	}
}
