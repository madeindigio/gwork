package drive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDownloadFake(t *testing.T) *fakeDrive {
	fd := newFakeDrive(t)
	links := func(mimes ...string) map[string]any {
		m := map[string]any{}
		for _, x := range mimes {
			m[x] = "https://export/" + x
		}
		return m
	}
	fd.files["bin"] = fakeFile{meta: meta("bin", "r.pdf", "application/pdf"), content: "%PDF-1.7 data"}
	fd.files["doc"] = fakeFile{
		meta:    meta("doc", "Plan", MimeDoc, "exportLinks", links(ExportFormats["docx"], ExportFormats["pdf"])),
		exports: map[string]string{ExportFormats["docx"]: "DOCX", ExportFormats["pdf"]: "PDF"},
	}
	fd.files["sheet"] = fakeFile{meta: meta("sheet", "B", MimeSheet), exports: map[string]string{ExportFormats["xlsx"]: "XLSX"}}
	fd.files["slides"] = fakeFile{meta: meta("slides", "S", MimeSlides), exports: map[string]string{ExportFormats["pptx"]: "PPTX"}}
	fd.files["drawing"] = fakeFile{meta: meta("drawing", "D", MimeDrawing), exports: map[string]string{ExportFormats["pdf"]: "DPDF"}}
	fd.files["form"] = fakeFile{meta: meta("form", "F", MimeForm)}
	fd.files["folder"] = fakeFile{meta: meta("folder", "Dir", MimeFolder)}
	fd.files["big"] = fakeFile{meta: meta("big", "Huge", MimeSheet), exportErr: map[string][2]any{ExportFormats["xlsx"]: {403, "exportSizeLimitExceeded"}}}
	return fd
}

func TestDownload(t *testing.T) {
	fd := newDownloadFake(t)
	tests := []struct {
		name, id, format string
		want, wantFormat string
		exported         bool
	}{
		{"regular file", "bin", "", "%PDF-1.7 data", "", false},
		{"doc default docx", "doc", "", "DOCX", "docx", true},
		{"doc as pdf", "doc", "PDF", "PDF", "pdf", true},
		{"sheet default xlsx", "sheet", "", "XLSX", "xlsx", true},
		{"slides default pptx", "slides", "", "PPTX", "pptx", true},
		{"drawing default pdf", "drawing", "", "DPDF", "pdf", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.bin")
			res, err := Download(context.Background(), tt.id, DownloadOptions{Out: out, ExportFormat: tt.format}, fd.options()...)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want || res.Bytes != int64(len(tt.want)) || res.Path != out ||
				res.Exported != tt.exported || res.ExportFormat != tt.wantFormat || res.File.ID != tt.id {
				t.Errorf("content %q, result %+v", data, res)
			}
			entries, _ := os.ReadDir(filepath.Dir(out))
			if len(entries) != 1 {
				t.Errorf("temporary files left: %v", entries)
			}
		})
	}
	for _, u := range fd.requestsTo("/files/bin") {
		if u.Query().Get("supportsAllDrives") != "true" {
			t.Errorf("%s lacks supportsAllDrives", u)
		}
	}
}

func TestDownloadRefusesOverwrite(t *testing.T) {
	fd := newDownloadFake(t)
	out := filepath.Join(t.TempDir(), "r.pdf")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Download(context.Background(), "bin", DownloadOptions{Out: out}, fd.options()...)
	if !errors.Is(err, ErrExists) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if len(fd.requestsTo("/files/bin")) != 0 {
		t.Error("no request expected before the overwrite check")
	}
	if data, _ := os.ReadFile(out); string(data) != "old" {
		t.Errorf("file modified: %q", data)
	}
	if _, err := Download(context.Background(), "bin", DownloadOptions{Out: out, Force: true}, fd.options()...); err != nil {
		t.Fatalf("with force: %v", err)
	}
	if data, _ := os.ReadFile(out); string(data) != "%PDF-1.7 data" {
		t.Errorf("file not replaced: %q", data)
	}
}

func TestDownloadErrors(t *testing.T) {
	fd := newDownloadFake(t)
	dir := t.TempDir()
	tests := []struct {
		name, id, format, out string
		kind                  error
		want                  string
	}{
		{"unknown format", "doc", "mp3", "", ErrUnsupported, "unknown export format"},
		{"format not offered", "doc", "xlsx", "", ErrUnsupported, "available: docx, pdf"},
		{"format for regular file", "bin", "pdf", "", ErrUnsupported, "downloaded as-is"},
		{"form", "form", "", "", ErrUnsupported, "cannot be exported"},
		{"folder", "folder", "", "", ErrUnsupported, "is a folder"},
		{"export too large", "big", "", "", ErrExportTooLarge, "about 10 MB"},
		{"directory out", "bin", "", dir, nil, "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.out
			if out == "" {
				out = filepath.Join(t.TempDir(), "x")
			}
			_, err := Download(context.Background(), tt.id, DownloadOptions{Out: out, ExportFormat: tt.format}, fd.options()...)
			if err == nil || (tt.kind != nil && !errors.Is(err, tt.kind)) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %v containing %q", err, tt.kind, tt.want)
			}
			if tt.out == "" {
				if _, statErr := os.Stat(out); statErr == nil {
					t.Error("output file created on error")
				}
				entries, _ := os.ReadDir(filepath.Dir(out))
				if len(entries) != 0 {
					t.Errorf("files left: %v", entries)
				}
			}
		})
	}
	if _, err := Download(context.Background(), "bin", DownloadOptions{}, fd.options()...); err == nil {
		t.Error("missing out: want error")
	}
}
