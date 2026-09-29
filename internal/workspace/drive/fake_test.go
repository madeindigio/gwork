package drive

import (
	"net/http"
	"net/url"
	"sync"
	"testing"

	"google.golang.org/api/option"

	"github.com/madeindigio/gwork/internal/testutil"
)

// fakeFile is a file served by fakeDrive.
type fakeFile struct {
	meta    map[string]any
	content string            // alt=media content
	exports map[string]string // export MIME type -> content
	// exportErr maps an export MIME type to a Google error (code, reason).
	exportErr map[string][2]any
}

// fakeDrive is a minimal Drive v3 fake: GET /files, GET /files/{id}
// (metadata or alt=media) and GET /files/{id}/export.
type fakeDrive struct {
	t     *testing.T
	files map[string]fakeFile
	list  []map[string]any // pages returned by GET /files, in order

	mu       sync.Mutex
	requests []*url.URL
}

func newFakeDrive(t *testing.T) *fakeDrive {
	return &fakeDrive{t: t, files: map[string]fakeFile{}}
}

func (f *fakeDrive) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := *r.URL
	f.requests = append(f.requests, &u)
}

// requestsTo returns the recorded requests whose path is p.
func (f *fakeDrive) requestsTo(p string) []*url.URL {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*url.URL
	for _, u := range f.requests {
		if u.Path == p {
			out = append(out, u)
		}
	}
	return out
}

func (f *fakeDrive) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		page := 0
		if tok := r.URL.Query().Get("pageToken"); tok != "" {
			page = int(tok[0] - '0')
		}
		if page >= len(f.list) {
			testutil.WriteJSON(f.t, w, map[string]any{"files": []any{}})
			return
		}
		testutil.WriteJSON(f.t, w, f.list[page])
	})
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		file, ok := f.files[r.PathValue("id")]
		if !ok {
			testutil.WriteGoogleError(w, 404, "notFound", "File not found: "+r.PathValue("id"))
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			_, _ = w.Write([]byte(file.content))
			return
		}
		testutil.WriteJSON(f.t, w, file.meta)
	})
	mux.HandleFunc("GET /files/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		file, ok := f.files[r.PathValue("id")]
		if !ok {
			testutil.WriteGoogleError(w, 404, "notFound", "File not found")
			return
		}
		mime := r.URL.Query().Get("mimeType")
		if e, ok := file.exportErr[mime]; ok {
			testutil.WriteGoogleError(w, e[0].(int), e[1].(string), "export failed: "+e[1].(string))
			return
		}
		content, ok := file.exports[mime]
		if !ok {
			testutil.WriteGoogleError(w, 400, "badRequest", "Export only supports Docs Editors files.")
			return
		}
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write([]byte(content))
	})
	return mux
}

func (f *fakeDrive) options() []option.ClientOption {
	return testutil.FakeGoogle(f.t, f.handler())
}

// meta returns file metadata as the Drive API returns it.
func meta(id, name, mime string, extra ...any) map[string]any {
	m := map[string]any{
		"id":           id,
		"name":         name,
		"mimeType":     mime,
		"modifiedTime": "2026-09-20T10:00:00.000Z",
		"owners":       []any{map[string]any{"emailAddress": "owner@digio.es"}},
		"webViewLink":  "https://drive.google.com/file/d/" + id + "/view",
		"parents":      []any{"root"},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}
