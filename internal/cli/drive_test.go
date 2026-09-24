package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/testutil"
	"github.com/digio/gwork-cli/internal/workspace/drive"
)

// driveTestMux serves a small fake Drive: a search result, a Google Doc
// ("doc1"), a text file ("txt1"), a PDF ("pdf1") and a Sheet ("sheet1").
// Every received query string is appended to *queries.
func driveTestMux(t *testing.T, queries *[]string) *http.ServeMux {
	t.Helper()
	var mu sync.Mutex
	rec := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if queries != nil {
			*queries = append(*queries, r.URL.Path+"?"+r.URL.RawQuery)
		}
	}
	files := map[string]map[string]any{
		"doc1": {"id": "doc1", "name": "Plan Q3", "mimeType": drive.MimeDoc, "modifiedTime": "2026-09-20T10:00:00Z",
			"owners": []any{map[string]any{"emailAddress": "ana@digio.es"}}, "parents": []any{"root"},
			"description": "The plan", "shared": true, "webViewLink": "https://docs.google.com/document/d/doc1",
			"exportLinks": map[string]any{"application/pdf": "x", drive.ExportFormats["docx"]: "y"}},
		"txt1":   {"id": "txt1", "name": "notes.txt", "mimeType": "text/plain", "size": "11"},
		"pdf1":   {"id": "pdf1", "name": "report.pdf", "mimeType": "application/pdf", "size": "2048"},
		"sheet1": {"id": "sheet1", "name": "Budget", "mimeType": drive.MimeSheet},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		testutil.WriteJSON(t, w, map[string]any{"files": []any{files["doc1"], files["pdf1"]}})
	})
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		id := r.PathValue("id")
		f, ok := files[id]
		if !ok {
			testutil.WriteGoogleError(w, 404, "notFound", "File not found: "+id+".")
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			content := map[string]string{"txt1": "hello notes", "pdf1": "%PDF-1.7"}[id]
			_, _ = w.Write([]byte(content))
			return
		}
		testutil.WriteJSON(t, w, f)
	})
	mux.HandleFunc("GET /files/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		rec(r)
		key := r.PathValue("id") + " " + r.URL.Query().Get("mimeType")
		content, ok := map[string]string{
			"doc1 text/markdown":                 "# Plan Q3\n\nBody",
			"doc1 " + drive.ExportFormats["pdf"]: "PDFDATA",
			"sheet1 text/csv":                    "a,b\n1,2\n",
		}[key]
		if !ok {
			testutil.WriteGoogleError(w, 400, "badRequest", "unsupported export")
			return
		}
		_, _ = w.Write([]byte(content))
	})
	return mux
}

func TestDriveSearchText(t *testing.T) {
	var queries []string
	p := testutil.NewFakeProvider(t, driveTestMux(t, &queries))
	stdout, stderr, code := runCLI(t, p, "drive", "search", "plan", "--type", "doc", "--owner", "ana@digio.es", "--max", "5")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"ID", "TYPE", "NAME", "doc1", "Plan Q3", "pdf1", "2.0 KiB"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if len(queries) != 1 {
		t.Fatalf("queries = %v", queries)
	}
	for _, want := range []string{"supportsAllDrives=true", "includeItemsFromAllDrives=true", "corpora=allDrives", "pageSize=5", "fullText+contains+%27plan%27", "in+owners"} {
		if !strings.Contains(queries[0], want) {
			t.Errorf("query lacks %s: %s", want, queries[0])
		}
	}
}

func TestDriveSearchJSON(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	stdout, stderr, code := runCLI(t, p, "drive", "search", "--name", "plan", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, stdout)
	}
	if len(got) != 2 || got[0]["id"] != "doc1" || got[0]["type"] != "doc" || got[0]["mime_type"] != drive.MimeDoc || got[1]["size"] != float64(2048) {
		t.Errorf("unexpected: %v", got)
	}
	if owners, ok := got[1]["owners"].([]any); !ok || len(owners) != 0 {
		t.Errorf("owners must be an empty array: %v", got[1]["owners"])
	}
}

func TestDriveSearchInvalidType(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	_, stderr, code := runCLI(t, p, "drive", "search", "--type", "video")
	if code != 1 || !strings.Contains(stderr, `unknown file type "video"`) {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestDriveSearchScopeMissing(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil), auth.Gmail)
	_, stderr, code := runCLI(t, p, "drive", "search", "x")
	if code != 1 || !strings.Contains(stderr, "gwork auth login --services drive") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestDriveGet(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	stdout, stderr, code := runCLI(t, p, "drive", "get", "doc1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"Plan Q3", "The plan", "ana@digio.es", "application/pdf", "https://docs.google.com/document/d/doc1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	stdout, _, code = runCLI(t, p, "drive", "get", "doc1", "--json")
	if code != 0 {
		t.Fatal("json get failed")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["description"] != "The plan" || got["shared"] != true || len(got["export_links"].([]any)) != 2 {
		t.Errorf("unexpected: %v", got)
	}
}

func TestDriveGetNotFound(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	_, stderr, code := runCLI(t, p, "drive", "get", "nope")
	if code != 1 || !strings.Contains(stderr, "not found") || !strings.Contains(stderr, "hint:") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestDriveRead(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))

	stdout, stderr, code := runCLI(t, p, "drive", "read", "doc1")
	if code != 0 || stdout != "# Plan Q3\n\nBody\n" || stderr != "" {
		t.Fatalf("doc: exit %d stdout %q stderr %q", code, stdout, stderr)
	}

	stdout, stderr, code = runCLI(t, p, "drive", "read", "sheet1")
	if code != 0 || stdout != "a,b\n1,2\n" || !strings.Contains(stderr, "note: only the first sheet") {
		t.Fatalf("sheet: exit %d stdout %q stderr %q", code, stdout, stderr)
	}

	stdout, stderr, code = runCLI(t, p, "drive", "read", "txt1", "--max-bytes", "5")
	if code != 0 || stdout != "hello\n" || !strings.Contains(stderr, "truncated to 5 bytes") {
		t.Fatalf("txt: exit %d stdout %q stderr %q", code, stdout, stderr)
	}

	stdout, _, code = runCLI(t, p, "drive", "read", "txt1", "--json")
	if code != 0 {
		t.Fatal("json read failed")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["text"] != "hello notes" || got["truncated"] != false || got["content_mime_type"] != "text/plain" {
		t.Errorf("unexpected: %v", got)
	}
}

func TestDriveReadBinary(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	stdout, stderr, code := runCLI(t, p, "drive", "read", "pdf1")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "gwork drive download pdf1") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestDriveDownload(t *testing.T) {
	p := testutil.NewFakeProvider(t, driveTestMux(t, nil))
	dir := t.TempDir()
	out := filepath.Join(dir, "plan.pdf")

	stdout, stderr, code := runCLI(t, p, "drive", "download", "doc1", "--out", out, "--export-format", "pdf")
	if code != 0 || !strings.Contains(stdout, "Saved "+out) || !strings.Contains(stdout, "exported as pdf") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if data, _ := os.ReadFile(out); string(data) != "PDFDATA" {
		t.Errorf("content = %q", data)
	}

	_, stderr, code = runCLI(t, p, "drive", "download", "doc1", "--out", out, "--export-format", "pdf")
	if code != 1 || !strings.Contains(stderr, "--force") {
		t.Fatalf("overwrite: exit %d stderr %q", code, stderr)
	}

	out2 := filepath.Join(dir, "report.pdf")
	stdout, _, code = runCLI(t, p, "drive", "download", "pdf1", "--out", out2, "--json")
	if code != 0 {
		t.Fatalf("json download failed: %s", stdout)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["path"] != out2 || got["bytes"] != float64(8) || got["exported"] != false {
		t.Errorf("unexpected: %v", got)
	}

	_, stderr, code = runCLI(t, p, "drive", "download", "pdf1")
	if code != 1 || !strings.Contains(stderr, "out") {
		t.Fatalf("missing --out: exit %d stderr %q", code, stderr)
	}
}

func TestFormatDriveSize(t *testing.T) {
	tests := map[int64]string{0: "", 512: "512 B", 2048: "2.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"}
	for n, want := range tests {
		if got := formatDriveSize(n); got != want {
			t.Errorf("formatDriveSize(%d) = %q, want %q", n, got, want)
		}
	}
}
