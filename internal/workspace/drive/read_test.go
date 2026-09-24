package drive

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	fd := newFakeDrive(t)
	fd.files["doc"] = fakeFile{meta: meta("doc", "Plan", MimeDoc), exports: map[string]string{"text/markdown": "# Plan\n", "text/plain": "Plan\n"}}
	fd.files["docfb"] = fakeFile{meta: meta("docfb", "Old", MimeDoc), exports: map[string]string{"text/plain": "plain only"}}
	fd.files["sheet"] = fakeFile{meta: meta("sheet", "Budget", MimeSheet), exports: map[string]string{"text/csv": "a,b\n1,2\n"}}
	fd.files["slides"] = fakeFile{meta: meta("slides", "Deck", MimeSlides), exports: map[string]string{"text/plain": "Slide 1"}}
	fd.files["txt"] = fakeFile{meta: meta("txt", "notes.txt", "text/plain"), content: "hello notes"}
	fd.files["json"] = fakeFile{meta: meta("json", "a.json", "application/json"), content: `{"a":1}`}
	fd.files["yaml"] = fakeFile{meta: meta("yaml", "a.yaml", "application/x-yaml"), content: "a: 1"}
	fd.files["md"] = fakeFile{meta: meta("md", "README.md", "text/markdown"), content: "# Readme"}

	tests := []struct {
		name, id, format   string
		wantText, wantMime string
		exported           bool
		note               string
	}{
		{"doc markdown", "doc", "", "# Plan\n", "text/markdown", true, ""},
		{"doc md explicit", "doc", "md", "# Plan\n", "text/markdown", true, ""},
		{"doc txt", "doc", "txt", "Plan\n", "text/plain", true, ""},
		{"doc fallback to plain", "docfb", "", "plain only", "text/plain", true, "Markdown export was rejected"},
		{"sheet csv", "sheet", "", "a,b\n1,2\n", "text/csv", true, "only the first sheet"},
		{"sheet csv explicit", "sheet", "csv", "a,b\n1,2\n", "text/csv", true, "only the first sheet"},
		{"slides", "slides", "", "Slide 1", "text/plain", true, ""},
		{"text file", "txt", "", "hello notes", "text/plain", false, ""},
		{"json file", "json", "", `{"a":1}`, "application/json", false, ""},
		{"yaml file", "yaml", "", "a: 1", "application/x-yaml", false, ""},
		{"markdown file ignores format", "md", "csv", "# Readme", "text/markdown", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Read(context.Background(), tt.id, ReadOptions{Format: tt.format}, fd.options()...)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if c.Text != tt.wantText || c.ContentMimeType != tt.wantMime || c.Exported != tt.exported ||
				c.Truncated || c.Bytes != len(tt.wantText) || c.MaxBytes != DefaultMaxBytes || c.File.ID != tt.id {
				t.Errorf("unexpected content: %+v", c)
			}
			if c.Notes == nil {
				t.Error("notes must not be nil")
			}
			joined := strings.Join(c.Notes, "\n")
			if tt.note == "" && joined != "" || !strings.Contains(joined, tt.note) {
				t.Errorf("notes = %q, want %q", joined, tt.note)
			}
		})
	}

	// Metadata and downloads support shared drives.
	for _, u := range fd.requestsTo("/files/txt") {
		if u.Query().Get("supportsAllDrives") != "true" {
			t.Errorf("request %s lacks supportsAllDrives", u)
		}
	}
	var media bool
	for _, u := range fd.requestsTo("/files/txt") {
		media = media || u.Query().Get("alt") == "media"
	}
	if !media {
		t.Error("text file was not downloaded with alt=media")
	}
}

func TestReadErrors(t *testing.T) {
	fd := newFakeDrive(t)
	fd.files["pdf"] = fakeFile{meta: meta("pdf", "r.pdf", "application/pdf")}
	fd.files["img"] = fakeFile{meta: meta("img", "p.png", "image/png")}
	fd.files["docx"] = fakeFile{meta: meta("docx", "w.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")}
	fd.files["drawing"] = fakeFile{meta: meta("drawing", "D", MimeDrawing)}
	fd.files["form"] = fakeFile{meta: meta("form", "F", MimeForm)}
	fd.files["folder"] = fakeFile{meta: meta("folder", "Dir", MimeFolder)}
	fd.files["short"] = fakeFile{meta: meta("short", "S", MimeShortcut, "shortcutDetails", map[string]any{"targetId": "T1"})}
	fd.files["doc"] = fakeFile{meta: meta("doc", "Plan", MimeDoc)}
	fd.files["big"] = fakeFile{meta: meta("big", "Huge", MimeDoc), exportErr: map[string][2]any{"text/markdown": {403, "exportSizeLimitExceeded"}}}
	fd.files["sheet"] = fakeFile{meta: meta("sheet", "S", MimeSheet)}
	fd.files["slides"] = fakeFile{meta: meta("slides", "S", MimeSlides)}

	tests := []struct {
		name, id, format string
		kind             error
		want             string
	}{
		{"pdf", "pdf", "", ErrBinary, "gwork drive download pdf --out"},
		{"image", "img", "", ErrBinary, "not a text file"},
		{"office", "docx", "", ErrBinary, "gwork drive download docx"},
		{"drawing", "drawing", "", ErrUnsupported, "--export-format pdf"},
		{"form", "form", "", ErrUnsupported, "open it in the browser"},
		{"folder", "folder", "", ErrUnsupported, "gwork drive search --folder folder"},
		{"shortcut", "short", "", ErrUnsupported, "gwork drive read T1"},
		{"doc bad format", "doc", "csv", ErrUnsupported, `format "csv"`},
		{"sheet bad format", "sheet", "md", ErrUnsupported, "valid: csv"},
		{"slides bad format", "slides", "md", ErrUnsupported, "valid: txt"},
		{"export too large", "big", "", ErrExportTooLarge, "about 10 MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(context.Background(), tt.id, ReadOptions{Format: tt.format}, fd.options()...)
			if !errors.Is(err, tt.kind) {
				t.Fatalf("err = %v, want %v", err, tt.kind)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q, want containing %q", err, tt.want)
			}
		})
	}
	// Binary and unsupported types never fetch content.
	if n := len(fd.requestsTo("/files/pdf/export")); n != 0 {
		t.Errorf("unexpected export requests: %d", n)
	}
}

func TestReadTruncates(t *testing.T) {
	fd := newFakeDrive(t)
	fd.files["txt"] = fakeFile{meta: meta("txt", "a.txt", "text/plain"), content: "abcdefghij"}
	// "é" is 2 bytes: cutting at 4 bytes would split the second one.
	fd.files["utf"] = fakeFile{meta: meta("utf", "u.txt", "text/plain"), content: "aéé"}

	c, err := Read(context.Background(), "txt", ReadOptions{MaxBytes: 4}, fd.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "abcd" || !c.Truncated || c.Bytes != 4 || c.MaxBytes != 4 {
		t.Errorf("unexpected: %+v", c)
	}
	c, err = Read(context.Background(), "txt", ReadOptions{MaxBytes: 10}, fd.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Truncated || c.Text != "abcdefghij" {
		t.Errorf("exact size must not be truncated: %+v", c)
	}
	c, err = Read(context.Background(), "utf", ReadOptions{MaxBytes: 4}, fd.options()...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "aé" || !c.Truncated {
		t.Errorf("utf-8 cut: %q truncated=%v", c.Text, c.Truncated)
	}
}
