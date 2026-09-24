package drive

import (
	"strings"
	"testing"
	"time"
)

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", `'plain'`},
		{"", `''`},
		{"O'Brien", `'O\'Brien'`},
		{`back\slash`, `'back\\slash'`},
		{`\'`, `'\\\''`},
		{`'`, `'\''`},
		{"ñandú 📄", `'ñandú 📄'`},
	}
	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestBuildQuery(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		opts SearchOptions
		want string
	}{
		{"empty", SearchOptions{}, "trashed = false"},
		{"blank values ignored", SearchOptions{Text: "  ", Name: " "}, "trashed = false"},
		{"text", SearchOptions{Text: "budget 2026"}, "trashed = false and fullText contains 'budget 2026'"},
		{"name", SearchOptions{Name: "report"}, "trashed = false and name contains 'report'"},
		{"type doc", SearchOptions{Type: "doc"}, "trashed = false and mimeType = '" + MimeDoc + "'"},
		{"type case-insensitive", SearchOptions{Type: "Sheet"}, "trashed = false and mimeType = '" + MimeSheet + "'"},
		{"type slides", SearchOptions{Type: "slides"}, "trashed = false and mimeType = '" + MimeSlides + "'"},
		{"type pdf", SearchOptions{Type: "pdf"}, "trashed = false and mimeType = 'application/pdf'"},
		{"type folder", SearchOptions{Type: "folder"}, "trashed = false and mimeType = '" + MimeFolder + "'"},
		{"type form", SearchOptions{Type: "form"}, "trashed = false and mimeType = '" + MimeForm + "'"},
		{"type drawing", SearchOptions{Type: "drawing"}, "trashed = false and mimeType = '" + MimeDrawing + "'"},
		{"type image", SearchOptions{Type: "image"}, "trashed = false and mimeType contains 'image/'"},
		{"mime", SearchOptions{MimeType: "text/plain"}, "trashed = false and mimeType = 'text/plain'"},
		{"owner", SearchOptions{Owner: "ana@digio.es"}, "trashed = false and 'ana@digio.es' in owners"},
		{"folder", SearchOptions{FolderID: "abc123"}, "trashed = false and 'abc123' in parents"},
		{"modified after date", SearchOptions{ModifiedAfter: "2026-09-01", Now: now}, "trashed = false and modifiedTime > '2026-09-01T00:00:00Z'"},
		{"modified after relative", SearchOptions{ModifiedAfter: "7d", Now: now}, "trashed = false and modifiedTime > '2026-09-17T12:00:00Z'"},
		{"modified after offset converted to UTC", SearchOptions{ModifiedAfter: "2026-09-01T10:00:00+02:00", Now: now}, "trashed = false and modifiedTime > '2026-09-01T08:00:00Z'"},
		{"raw", SearchOptions{RawQuery: "starred = true or sharedWithMe"}, "trashed = false and (starred = true or sharedWithMe)"},
		{
			"all combined",
			SearchOptions{Text: "q3", Name: "plan", Type: "doc", Owner: "a@b.c", FolderID: "F1", ModifiedAfter: "2026-01-01", Now: now, RawQuery: "starred = true"},
			"trashed = false and fullText contains 'q3' and name contains 'plan' and mimeType = '" + MimeDoc +
				"' and 'a@b.c' in owners and 'F1' in parents and modifiedTime > '2026-01-01T00:00:00Z' and (starred = true)",
		},
		// Injection attempts: quotes and backslashes stay inside the literal.
		{
			"name injection",
			SearchOptions{Name: "x' or name contains 'y"},
			`trashed = false and name contains 'x\' or name contains \'y'`,
		},
		{
			"text injection closing and reopening",
			SearchOptions{Text: "a') or trashed = true or ('"},
			`trashed = false and fullText contains 'a\') or trashed = true or (\''`,
		},
		{
			"backslash escape attempt",
			SearchOptions{Name: `x\' or trashed = true or name contains \'`},
			`trashed = false and name contains 'x\\\' or trashed = true or name contains \\\''`,
		},
		{
			"trailing backslash cannot escape the closing quote",
			SearchOptions{FolderID: `abc\`},
			`trashed = false and 'abc\\' in parents`,
		},
		{
			"owner injection",
			SearchOptions{Owner: "' in owners or 'x"},
			`trashed = false and '\' in owners or \'x' in owners`,
		},
		{
			"mime injection",
			SearchOptions{MimeType: "text/plain' or mimeType != '"},
			`trashed = false and mimeType = 'text/plain\' or mimeType != \''`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildQuery(tt.opts)
			if err != nil {
				t.Fatalf("BuildQuery: %v", err)
			}
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestBuildQueryErrors(t *testing.T) {
	tests := []struct {
		name string
		opts SearchOptions
		want string
	}{
		{"unknown type", SearchOptions{Type: "video"}, `unknown file type "video"`},
		{"type and mime", SearchOptions{Type: "doc", MimeType: "text/plain"}, "not both"},
		{"bad date", SearchOptions{ModifiedAfter: "someday"}, `modified after "someday"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildQuery(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// TestBuildQueryEscapingRoundTrip checks that, for adversarial inputs, the
// quoted literal is a single well-formed token: parsing it back with Drive's
// escaping rules yields the original value and consumes the whole literal.
func TestBuildQueryEscapingRoundTrip(t *testing.T) {
	inputs := []string{`'`, `\`, `\'`, `''`, `\\'`, `a'b\c'd`, `' or '1'='1`, "multi\nline'"}
	for _, in := range inputs {
		lit := quote(in)
		got, rest, ok := unquote(lit)
		if !ok || rest != "" || got != in {
			t.Errorf("quote(%q) = %s: unquote -> %q rest %q ok %v", in, lit, got, rest, ok)
		}
	}
}

// unquote parses a Drive string literal at the start of s and returns its
// value and the remaining input.
func unquote(s string) (val, rest string, ok bool) {
	if !strings.HasPrefix(s, "'") {
		return "", s, false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return "", "", false
			}
			i++
			b.WriteByte(s[i])
		case '\'':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}

func TestTypeLabel(t *testing.T) {
	tests := map[string]string{
		MimeDoc:                            "doc",
		MimeSheet:                          "sheet",
		MimeSlides:                         "slides",
		MimeDrawing:                        "drawing",
		MimeForm:                           "form",
		MimeFolder:                         "folder",
		MimeShortcut:                       "shortcut",
		"application/vnd.google-apps.site": "site",
		"application/pdf":                  "pdf",
		"image/png":                        "image",
		"text/plain":                       "text",
		"application/json":                 "text",
		"application/zip":                  "file",
	}
	for mime, want := range tests {
		if got := TypeLabel(mime); got != want {
			t.Errorf("TypeLabel(%s) = %s, want %s", mime, got, want)
		}
	}
}

func TestIsTextMime(t *testing.T) {
	yes := []string{"text/plain", "text/markdown", "text/csv", "application/json", "application/xml",
		"application/x-yaml", "application/ld+json", "image/svg+xml", "text/plain; charset=utf-8"}
	no := []string{"application/pdf", "image/png", "application/zip", "application/octet-stream",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document"}
	for _, m := range yes {
		if !isTextMime(m) {
			t.Errorf("isTextMime(%s) = false", m)
		}
	}
	for _, m := range no {
		if isTextMime(m) {
			t.Errorf("isTextMime(%s) = true", m)
		}
	}
}

func TestDefaultExportFormat(t *testing.T) {
	tests := map[string]string{MimeDoc: "docx", MimeSheet: "xlsx", MimeSlides: "pptx", MimeDrawing: "pdf", MimeForm: "", "text/plain": ""}
	for mime, want := range tests {
		if got := DefaultExportFormat(mime); got != want {
			t.Errorf("DefaultExportFormat(%s) = %q, want %q", mime, got, want)
		}
	}
}
