package mcpserver

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
	"github.com/madeindigio/gwork/internal/workspace/drive"
)

// driveMux serves a fake Drive with a Google Doc ("doc1"), a long text file
// ("txt1"), a PDF ("pdf1") and a Sheet ("sheet1"). The query strings of
// GET /files requests are appended to *listQueries.
func driveMux(t *testing.T, listQueries *[]string) *http.ServeMux {
	t.Helper()
	var mu sync.Mutex
	files := map[string]map[string]any{
		"doc1": {"id": "doc1", "name": "Plan", "mimeType": drive.MimeDoc, "description": "Q3",
			"owners": []any{map[string]any{"emailAddress": "ana@digio.es"}}, "exportLinks": map[string]any{"text/plain": "x"}},
		"txt1":   {"id": "txt1", "name": "long.txt", "mimeType": "text/plain"},
		"pdf1":   {"id": "pdf1", "name": "r.pdf", "mimeType": "application/pdf"},
		"sheet1": {"id": "sheet1", "name": "Budget", "mimeType": drive.MimeSheet},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*listQueries = append(*listQueries, r.URL.RawQuery)
		mu.Unlock()
		testutil.WriteJSON(t, w, map[string]any{"files": []any{files["doc1"], files["pdf1"]}})
	})
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, ok := files[r.PathValue("id")]
		if !ok {
			testutil.WriteGoogleError(w, 404, "notFound", "File not found.")
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			_, _ = w.Write([]byte(strings.Repeat("ab", 50)))
			return
		}
		testutil.WriteJSON(t, w, f)
	})
	mux.HandleFunc("GET /files/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") + " " + r.URL.Query().Get("mimeType") {
		case "doc1 text/markdown":
			_, _ = w.Write([]byte("# Plan"))
		case "sheet1 text/csv":
			_, _ = w.Write([]byte("a,b"))
		default:
			testutil.WriteGoogleError(w, 400, "badRequest", "unsupported")
		}
	})
	return mux
}

func TestDriveToolsRegistered(t *testing.T) {
	var q []string
	cs, _ := newTestSession(t, testDeps(t, driveMux(t, &q)), auth.Drive)
	tools := listTools(t, cs)
	for _, name := range []string{"drive_search", "drive_get_file", "drive_read_file"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Description == "" {
			t.Errorf("%s: bad annotations/description", name)
		}
	}
	for name := range tools {
		if strings.Contains(name, "download") {
			t.Errorf("no download tool expected, got %s", name)
		}
	}
}

func TestDriveSearchTool(t *testing.T) {
	var q []string
	cs, _ := newTestSession(t, testDeps(t, driveMux(t, &q)), auth.Drive)
	out, res := callTool[driveSearchOutput](t, cs, "drive_search", map[string]any{
		"query_text": "plan", "type": "doc", "owner": "ana@digio.es", "folder_id": "F1",
		"modified_after": "7d", "raw_query": "starred = true", "max_results": 3,
	})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if len(out.Files) != 2 || out.Files[0].ID != "doc1" || out.Files[0].Type != "doc" || out.Files[1].Type != "pdf" {
		t.Fatalf("unexpected files: %+v", out.Files)
	}
	if len(q) != 1 {
		t.Fatalf("queries = %v", q)
	}
	for _, want := range []string{"supportsAllDrives=true", "includeItemsFromAllDrives=true", "corpora=allDrives", "pageSize=3",
		"modifiedTime+%3E+%272026-09-17T12%3A00%3A00Z%27", "%28starred+%3D+true%29", "%27F1%27+in+parents"} {
		if !strings.Contains(q[0], want) {
			t.Errorf("query lacks %s: %s", want, q[0])
		}
	}

	_, res = callTool[driveSearchOutput](t, cs, "drive_search", map[string]any{"type": "video"})
	if !res.IsError || !strings.Contains(resultText(res), "unknown file type") {
		t.Errorf("want type error, got %s", resultText(res))
	}
}

func TestDriveGetFileTool(t *testing.T) {
	var q []string
	cs, _ := newTestSession(t, testDeps(t, driveMux(t, &q)), auth.Drive)
	out, res := callTool[driveGetFileOutput](t, cs, "drive_get_file", map[string]any{"file_id": "doc1"})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.File == nil || out.File.Name != "Plan" || out.File.Description != "Q3" || len(out.File.ExportLinks) != 1 {
		t.Fatalf("unexpected: %+v", out.File)
	}

	_, res = callTool[driveGetFileOutput](t, cs, "drive_get_file", map[string]any{"file_id": "missing"})
	if !res.IsError || !strings.Contains(resultText(res), "not found") {
		t.Errorf("want classified not found, got %s", resultText(res))
	}
}

func TestDriveReadFileTool(t *testing.T) {
	var q []string
	cs, _ := newTestSession(t, testDeps(t, driveMux(t, &q)), auth.Drive)

	out, res := callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{"file_id": "doc1"})
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	if out.Text != "# Plan" || out.ContentMimeType != "text/markdown" || !out.Exported || out.Truncated || out.Notes == nil {
		t.Errorf("doc: %+v", out)
	}

	out, res = callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{"file_id": "sheet1"})
	if res.IsError || out.Text != "a,b" || len(out.Notes) != 1 || !strings.Contains(out.Notes[0], "first sheet") {
		t.Errorf("sheet: %+v %s", out, resultText(res))
	}

	out, res = callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{"file_id": "txt1", "max_chars": 10})
	if res.IsError || out.Text != "ababababab" || !out.Truncated || out.Exported {
		t.Errorf("txt: %+v %s", out, resultText(res))
	}

	out, res = callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{"file_id": "txt1"})
	if res.IsError || len(out.Text) != 100 || out.Truncated {
		t.Errorf("txt default: %+v", out)
	}

	_, res = callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{"file_id": "pdf1"})
	if !res.IsError || !strings.Contains(resultText(res), "gwork drive download") {
		t.Errorf("pdf: want binary error, got %s", resultText(res))
	}

	_, res = callTool[driveReadFileOutput](t, cs, "drive_read_file", map[string]any{})
	if !res.IsError {
		t.Error("missing file_id must fail")
	}
}

func TestDriveToolsNotRegisteredWithoutScope(t *testing.T) {
	var q []string
	cs, _ := newTestSession(t, testDeps(t, driveMux(t, &q), auth.Gmail), auth.Drive)
	if _, ok := listTools(t, cs)["drive_search"]; ok {
		t.Error("drive tools must not be registered when drive is not granted")
	}
}
