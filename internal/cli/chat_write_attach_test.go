package cli

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/testutil"
)

// chatUpload is one media.upload request seen by the fake.
type chatUpload struct {
	space, filename, contentType, data string
}

// newChatAttachFake extends newChatSendFake with the media.upload endpoint.
func newChatAttachFake(t *testing.T) (*chatSendFake, *[]chatUpload, *http.ServeMux) {
	t.Helper()
	f, mux := newChatSendFake(t)
	var ups []chatUpload
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
		f.mu.Lock()
		f.calls++
		ups = append(ups, chatUpload{r.PathValue("id"), req["filename"], media.Header.Get("Content-Type"), string(data)})
		f.mu.Unlock()
		testutil.WriteJSON(t, w, map[string]any{"attachmentDataRef": map[string]any{"attachmentUploadToken": "tok-" + req["filename"]}})
	})
	return f, &ups, mux
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChatSendAttach(t *testing.T) {
	dir := t.TempDir()
	csv := writeTemp(t, dir, "data.csv", "a,b\n1,2\n")
	blob := writeTemp(t, dir, "blob", "%PDF-1.4 no extension")
	f, ups, mux := newChatAttachFake(t)
	_, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), "chat", "send", "--to", "ana@digio.es",
		"--attach", csv, "--attach", blob, "--yes")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if len(*ups) != 2 {
		t.Fatalf("uploads %+v", *ups)
	}
	u0, u1 := (*ups)[0], (*ups)[1]
	if u0.space != "DM1" || u0.filename != "data.csv" || !strings.HasPrefix(u0.contentType, "text/csv") || u0.data != "a,b\n1,2\n" {
		t.Errorf("upload 0 %+v", u0)
	}
	if u1.filename != "blob" || u1.contentType != "application/pdf" {
		t.Errorf("upload 1 %+v (want sniffed application/pdf)", u1)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %+v", f.bodies)
	}
	if _, has := f.bodies[0]["text"]; has {
		t.Errorf("attachment-only message has text: %v", f.bodies[0])
	}
	atts, _ := f.bodies[0]["attachment"].([]any)
	if len(atts) != 2 {
		t.Fatalf("attachment %v", f.bodies[0]["attachment"])
	}
	ref, _ := atts[1].(map[string]any)["attachmentDataRef"].(map[string]any)
	if ref["attachmentUploadToken"] != "tok-blob" {
		t.Errorf("ref %v", ref)
	}
}

func TestChatSendAttachDryRunAndPrompt(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "notes.txt", strings.Repeat("x", 2048))
	f, _, mux := newChatAttachFake(t)
	stdout, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), "chat", "send", "--space", "AAA",
		"--text", "hi", "--attach", p, "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	var got chatSendPreview
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Filename != "notes.txt" || got.Attachments[0].Size != 2048 ||
		!strings.HasPrefix(got.Attachments[0].ContentType, "text/plain") {
		t.Errorf("preview %+v", got)
	}
	if strings.Contains(stdout, "xxxx") {
		t.Errorf("dry run leaked file content: %s", stdout)
	}

	app, _, errw := newTestApp(t, testutil.NewFakeProvider(t, mux))
	app.In = strings.NewReader("n\n")
	app.IsTerminal = func() bool { return true }
	if code := app.Run(context.Background(), []string{"chat", "send", "--space", "AAA", "--attach", p}); code == 0 {
		t.Fatal("declined prompt succeeded")
	}
	if !strings.Contains(errw.String(), "notes.txt (2.0 KiB)") {
		t.Errorf("prompt lacks attachment name and size: %s", errw)
	}
	if f.count() != 0 {
		t.Errorf("made %d calls", f.count())
	}
}

func TestChatSendAttachErrors(t *testing.T) {
	dir := t.TempDir()
	ok := writeTemp(t, dir, "ok.txt", "ok")
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing", args: []string{"--attach", filepath.Join(dir, "nope.txt")}, wantErr: "attachment:"},
		{name: "directory", args: []string{"--attach", dir}, wantErr: "not a regular file"},
		{name: "missing after good", args: []string{"--attach", ok, "--attach", filepath.Join(dir, "nope")}, wantErr: "attachment:"},
		{name: "neither text nor attach", args: nil, wantErr: "--attach"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, _, mux := newChatAttachFake(t)
			app, _, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
			app.IsTerminal = func() bool { return true }
			app.In = strings.NewReader("y\n")
			code := app.Run(context.Background(), append([]string{"chat", "send", "--space", "AAA"}, tc.args...))
			if code == 0 || !strings.Contains(stderr.String(), tc.wantErr) {
				t.Fatalf("code %d stderr %s", code, stderr)
			}
			if strings.Contains(stderr.String(), "Send to") {
				t.Errorf("prompted before checking files: %s", stderr)
			}
			if f.count() != 0 {
				t.Errorf("made %d calls", f.count())
			}
		})
	}
}

func TestReadUploadFilesLimit(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "big.bin", "0123456789")
	if _, err := readUploadFiles([]string{p}, 9); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v", err)
	}
	files, err := readUploadFiles([]string{p}, 10)
	if err != nil || len(files) != 1 || string(files[0].Data) != "0123456789" || files[0].Name != "big.bin" {
		t.Errorf("files %+v err %v", files, err)
	}
}
