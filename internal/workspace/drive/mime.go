package drive

import (
	"sort"
	"strings"
)

// Google-native MIME types.
const (
	MimeFolder   = "application/vnd.google-apps.folder"
	MimeDoc      = "application/vnd.google-apps.document"
	MimeSheet    = "application/vnd.google-apps.spreadsheet"
	MimeSlides   = "application/vnd.google-apps.presentation"
	MimeDrawing  = "application/vnd.google-apps.drawing"
	MimeForm     = "application/vnd.google-apps.form"
	MimeShortcut = "application/vnd.google-apps.shortcut"
	MimePDF      = "application/pdf"

	googleAppsPrefix = "application/vnd.google-apps."
)

// typeShortcuts maps the --type / type shortcuts to their MIME type. The
// "image" shortcut is special-cased by BuildQuery (it matches a prefix).
var typeShortcuts = map[string]string{
	"doc":     MimeDoc,
	"sheet":   MimeSheet,
	"slides":  MimeSlides,
	"pdf":     MimePDF,
	"folder":  MimeFolder,
	"form":    MimeForm,
	"drawing": MimeDrawing,
	"image":   "image/",
}

// TypeShortcuts returns the accepted type shortcuts, sorted.
func TypeShortcuts() []string {
	out := make([]string, 0, len(typeShortcuts))
	for k := range typeShortcuts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TypeLabel returns a short label for a MIME type: doc, sheet, slides,
// drawing, form, folder, shortcut, pdf, image, text, or the suffix of
// other Google-native types; "file" for anything else.
func TypeLabel(mime string) string {
	switch mime {
	case MimeDoc:
		return "doc"
	case MimeSheet:
		return "sheet"
	case MimeSlides:
		return "slides"
	case MimeDrawing:
		return "drawing"
	case MimeForm:
		return "form"
	case MimeFolder:
		return "folder"
	case MimeShortcut:
		return "shortcut"
	case MimePDF:
		return "pdf"
	}
	switch {
	case strings.HasPrefix(mime, googleAppsPrefix):
		return strings.TrimPrefix(mime, googleAppsPrefix)
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case isTextMime(mime):
		return "text"
	}
	return "file"
}

// IsGoogleNative reports whether mime is a Google Workspace native type
// (Docs, Sheets, Slides, ...), whose content must be exported.
func IsGoogleNative(mime string) bool {
	return strings.HasPrefix(mime, googleAppsPrefix)
}

// textMimes are non-text/* MIME types whose content is readable text.
var textMimes = map[string]bool{
	"application/json":       true,
	"application/xml":        true,
	"application/x-yaml":     true,
	"application/yaml":       true,
	"application/javascript": true,
	"application/x-sh":       true,
	"application/sql":        true,
	"application/toml":       true,
	"application/x-ndjson":   true,
	"application/csv":        true,
	"application/markdown":   true,
}

// isTextMime reports whether a regular (non Google-native) file of this
// MIME type can be returned as text.
func isTextMime(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return strings.HasPrefix(mime, "text/") || textMimes[mime] ||
		strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

// ExportFormats maps the --export-format names accepted by Download to the
// export MIME type requested from Drive.
var ExportFormats = map[string]string{
	"pdf":  "application/pdf",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"odt":  "application/vnd.oasis.opendocument.text",
	"ods":  "application/vnd.oasis.opendocument.spreadsheet",
	"odp":  "application/vnd.oasis.opendocument.presentation",
	"txt":  "text/plain",
	"csv":  "text/csv",
	"md":   "text/markdown",
	"html": "text/html",
	"png":  "image/png",
	"svg":  "image/svg+xml",
}

// ExportFormatNames returns the accepted export format names, sorted.
func ExportFormatNames() []string {
	out := make([]string, 0, len(ExportFormats))
	for k := range ExportFormats {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DefaultExportFormat returns the export format Download uses for a
// Google-native type when none is given: docx for Docs, xlsx for Sheets,
// pptx for Slides and pdf for Drawings (editable Office formats keep the
// most fidelity; Drawings have no Office equivalent). It returns "" for
// types that cannot be exported (Forms, folders, shortcuts, ...).
func DefaultExportFormat(mime string) string {
	switch mime {
	case MimeDoc:
		return "docx"
	case MimeSheet:
		return "xlsx"
	case MimeSlides:
		return "pptx"
	case MimeDrawing:
		return "pdf"
	}
	return ""
}

// formatForMime returns the export format name for an export MIME type,
// or the MIME type itself when it has no short name.
func formatForMime(mime string) string {
	for k, v := range ExportFormats {
		if v == mime {
			return k
		}
	}
	return mime
}
