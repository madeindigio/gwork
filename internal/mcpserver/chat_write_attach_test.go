package mcpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

type mcpUpload struct {
	space, filename, contentType, data string
}

// chatAttachMux extends chatWriteMux with media.upload; the created message
// echoes one attachment per attachmentDataRef.
func chatAttachMux(t *testing.T, mu *sync.Mutex, bodies *[]map[string]any, ups *[]mcpUpload) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload/v1/spaces/{id}/attachments:upload", func(w http.ResponseWriter, r *http.Request) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Error(err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		meta, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		var req map[string]string
		_ = json.NewDecoder(meta).Decode(&req)
		media, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		data, _ := io.ReadAll(media)
		mu.Lock()
		*ups = append(*ups, mcpUpload{r.PathValue("id"), req["filename"], media.Header.Get("Content-Type"), string(data)})
		mu.Unlock()
		testutil.WriteJSON(t, w, map[string]any{"attachmentDataRef": map[string]any{"attachmentUploadToken": "tok-" + req["filename"]}})
	})
	mux.HandleFunc("POST /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*bodies = append(*bodies, body)
		mu.Unlock()
		space := "spaces/" + r.PathValue("id")
		atts := []any{}
		list, _ := body["attachment"].([]any)
		for _, a := range list {
			ref, _ := a.(map[string]any)["attachmentDataRef"].(map[string]any)
			tok, _ := ref["attachmentUploadToken"].(string)
			atts = append(atts, map[string]any{"name": space + "/messages/M1/attachments/" + tok,
				"contentName": strings.TrimPrefix(tok, "tok-"), "source": "UPLOADED_CONTENT"})
		}
		testutil.WriteJSON(t, w, map[string]any{"name": space + "/messages/M1", "text": body["text"],
			"space": map[string]any{"name": space}, "createTime": "2026-09-24T12:00:00Z", "attachment": atts})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
	})
	return mux
}

func TestChatSendMessageAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.csv")
	if err := os.WriteFile(path, []byte("a,b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("TOP-SECRET-CONTENT"))
	tests := []struct {
		name    string
		args    map[string]any
		want    []mcpUpload
		wantErr string
	}{
		{name: "path and base64", args: map[string]any{"space": "AAA", "text": "files", "attachments": []any{
			map[string]any{"path": path},
			map[string]any{"filename": "note.bin", "content_base64": b64, "content_type": "application/x-test"},
		}}, want: []mcpUpload{
			{"AAA", "report.csv", "text/csv; charset=utf-8", "a,b\n"},
			{"AAA", "note.bin", "application/x-test", "TOP-SECRET-CONTENT"},
		}},
		{name: "attachment only dm", args: map[string]any{"user_email": "bob@digio.es", "attachments": []any{
			map[string]any{"filename": "n.txt", "content_base64": strings.TrimRight(b64, "=")},
		}}, want: []mcpUpload{{"DM1", "n.txt", "text/plain; charset=utf-8", "TOP-SECRET-CONTENT"}}},
		{name: "both sources", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"path": path, "content_base64": b64, "filename": "x"}}}, wantErr: "attachments[0]: set exactly one of path or content_base64"},
		{name: "no source", args: map[string]any{"space": "AAA", "text": "x", "attachments": []any{
			map[string]any{"filename": "x"}}}, wantErr: "exactly one of path or content_base64"},
		{name: "base64 without filename", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"content_base64": b64}}}, wantErr: "filename is required"},
		{name: "path with filename", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"path": path, "filename": "other.csv"}}}, wantErr: "only used with content_base64"},
		{name: "bad base64", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"filename": "x", "content_base64": "!!!notbase64"}}}, wantErr: "not valid base64"},
		{name: "missing path", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"path": filepath.Join(dir, "nope")}}}, wantErr: "attachments[0]"},
		{name: "directory path", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"path": dir}}}, wantErr: "not a regular file"},
		{name: "bad filename", args: map[string]any{"space": "AAA", "attachments": []any{
			map[string]any{"filename": "../x", "content_base64": b64}}}, wantErr: "base name"},
		{name: "no text no attachments", args: map[string]any{"space": "AAA"}, wantErr: "text is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []map[string]any
			var ups []mcpUpload
			var logs bytes.Buffer
			deps := testDeps(t, chatAttachMux(t, &mu, &bodies, &ups))
			deps.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			deps.Write = WriteOptions{Services: []auth.Service{auth.Chat}}
			cs, _ := newTestSession(t, deps, auth.Chat)
			out, res := callTool[chatSendMessageOutput](t, cs, "chat_send_message", tc.args)
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(logs.String(), "TOP-SECRET") || strings.Contains(logs.String(), b64[:12]) {
				t.Errorf("log leaked content: %s", logs.String())
			}
			if !strings.Contains(logs.String(), "write tool call") {
				t.Errorf("no audit line: %s", logs.String())
			}
			if tc.wantErr != "" {
				if !res.IsError || !strings.Contains(resultText(res), tc.wantErr) || len(bodies) != 0 || len(ups) != 0 {
					t.Fatalf("res %s, bodies %v, uploads %v", resultText(res), bodies, ups)
				}
				if strings.Contains(resultText(res), "TOP-SECRET") {
					t.Errorf("error leaked content: %s", resultText(res))
				}
				return
			}
			if res.IsError {
				t.Fatal(resultText(res))
			}
			if len(ups) != len(tc.want) {
				t.Fatalf("uploads %+v", ups)
			}
			for i := range ups {
				if ups[i] != tc.want[i] {
					t.Errorf("upload %d = %+v, want %+v", i, ups[i], tc.want[i])
				}
			}
			atts, _ := bodies[0]["attachment"].([]any)
			if len(bodies) != 1 || len(atts) != len(tc.want) {
				t.Fatalf("bodies %v", bodies)
			}
			if len(out.Message.Attachments) != len(tc.want) || out.Message.Attachments[0].ContentName != tc.want[0].filename {
				t.Errorf("out %+v", out.Message)
			}
		})
	}
}

func TestLoadUploadSizeLimit(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(make([]byte, 64))
	if _, _, _, err := (uploadInput{Filename: "x", ContentBase64: big}).loadUpload(32); err == nil || !strings.Contains(err.Error(), "more than 32 bytes") {
		t.Errorf("base64 limit: %v", err)
	}
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := (uploadInput{Path: p}).loadUpload(32); err == nil || !strings.Contains(err.Error(), "limit is 32 bytes") {
		t.Errorf("path limit: %v", err)
	}
	name, _, data, err := (uploadInput{Path: p}).loadUpload(64)
	if err != nil || name != "f" || len(data) != 64 {
		t.Errorf("name %q len %d err %v", name, len(data), err)
	}
}
