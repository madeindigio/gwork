package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// DefaultMaxBytes is the default content limit of Read (5 MB).
const DefaultMaxBytes int64 = 5 << 20

// Errors returned by Read and Download. Test with errors.Is; the concrete
// messages include an actionable suggestion.
var (
	// ErrUnsupported means the file type cannot be read as text or
	// exported in the requested format.
	ErrUnsupported = errors.New("unsupported file type")
	// ErrBinary means the file is binary and must be downloaded instead.
	ErrBinary = errors.New("binary file")
	// ErrExportTooLarge means Google refused the export because the
	// exported content exceeds its export size limit (about 10 MB).
	ErrExportTooLarge = errors.New("export size limit exceeded")
)

// ReadOptions configure Read.
type ReadOptions struct {
	// MaxBytes limits the returned content (default DefaultMaxBytes).
	MaxBytes int64
	// Format selects the export format of Google-native files: md or txt
	// for Docs (default md), csv for Sheets, txt for Slides. Empty picks
	// the default; it is ignored for regular text files.
	Format string
}

// Content is the text content of a file returned by Read.
type Content struct {
	File FileSummary `json:"file"`
	// ContentMimeType is the MIME type of Text (the export format for
	// Google-native files, the file's type otherwise).
	ContentMimeType string `json:"content_mime_type"`
	// Exported reports whether the content was exported from a
	// Google-native file.
	Exported bool   `json:"exported"`
	Text     string `json:"text"`
	// Bytes is the size of Text in bytes.
	Bytes int `json:"bytes"`
	// Truncated reports whether the content was cut at MaxBytes.
	Truncated bool  `json:"truncated"`
	MaxBytes  int64 `json:"max_bytes"`
	// Notes are caveats about the content (fallbacks, first sheet only...).
	Notes []string `json:"notes"`
}

// Read returns the text content of a file:
//
//   - Google Docs are exported as Markdown (falling back to plain text when
//     Markdown export is rejected), or plain text with Format "txt";
//   - Google Sheets are exported as CSV (Drive only exports the first sheet);
//   - Google Slides are exported as plain text;
//   - other Google-native types (Drawings, Forms, ...) are unsupported;
//   - regular text-like files (text/*, JSON, XML, YAML, ...) are downloaded;
//   - binary files (PDF, images, Office...) fail with ErrBinary, suggesting
//     "gwork drive download".
//
// Content longer than MaxBytes is cut (on a UTF-8 boundary) and flagged
// as truncated.
func Read(ctx context.Context, fileID string, opts ReadOptions, clientOpts ...option.ClientOption) (*Content, error) {
	svc, err := New(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	f, err := getAPIFile(ctx, svc, fileID)
	if err != nil {
		return nil, err
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	c := &Content{File: summaryFromAPI(f), MaxBytes: maxBytes, Notes: []string{}}

	var resp *http.Response
	switch f.MimeType {
	case MimeDoc:
		var mime string
		switch format {
		case "", "md":
			mime = "text/markdown"
		case "txt":
			mime = "text/plain"
		default:
			return nil, formatError(f, format, "md, txt")
		}
		resp, err = exportFile(ctx, svc, f, mime)
		if err != nil && mime == "text/markdown" && isExportFormatRejected(err) {
			c.Notes = append(c.Notes, "Markdown export was rejected; returned plain text instead")
			mime = "text/plain"
			resp, err = exportFile(ctx, svc, f, mime)
		}
		c.ContentMimeType = mime
	case MimeSheet:
		if format != "" && format != "csv" {
			return nil, formatError(f, format, "csv")
		}
		c.ContentMimeType = "text/csv"
		c.Notes = append(c.Notes, "only the first sheet is exported as CSV; use 'gwork drive download --export-format xlsx' for the whole spreadsheet")
		resp, err = exportFile(ctx, svc, f, c.ContentMimeType)
	case MimeSlides:
		if format != "" && format != "txt" {
			return nil, formatError(f, format, "txt")
		}
		c.ContentMimeType = "text/plain"
		resp, err = exportFile(ctx, svc, f, c.ContentMimeType)
	case MimeShortcut:
		target := ""
		if f.ShortcutDetails != nil {
			target = f.ShortcutDetails.TargetId
		}
		return nil, fmt.Errorf("%w: %q is a shortcut; read its target instead: gwork drive read %s", ErrUnsupported, f.Name, target)
	default:
		switch {
		case f.MimeType == MimeFolder:
			return nil, fmt.Errorf("%w: %q is a folder; list its files with: gwork drive search --folder %s", ErrUnsupported, f.Name, f.Id)
		case IsGoogleNative(f.MimeType):
			hint := "open it in the browser: " + f.WebViewLink
			if def := DefaultExportFormat(f.MimeType); def != "" {
				hint = fmt.Sprintf("download an export instead: gwork drive download %s --out <path> --export-format %s", f.Id, def)
			}
			return nil, fmt.Errorf("%w: %q is a Google %s, which cannot be read as text; %s", ErrUnsupported, f.Name, TypeLabel(f.MimeType), hint)
		case isTextMime(f.MimeType):
			c.ContentMimeType = f.MimeType
			resp, err = downloadFile(ctx, svc, f)
		default:
			return nil, fmt.Errorf("%w: %q (%s) is not a text file; download it with: gwork drive download %s --out <path>", ErrBinary, f.Name, f.MimeType, f.Id)
		}
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	c.Exported = IsGoogleNative(f.MimeType)

	text, truncated, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read content of %s: %w", f.Id, err)
	}
	c.Text, c.Bytes, c.Truncated = text, len(text), truncated
	return c, nil
}

// formatError reports a --format not supported for a Google-native type.
func formatError(f *driveapi.File, format, valid string) error {
	return fmt.Errorf("%w: format %q is not available for a Google %s (valid: %s)", ErrUnsupported, format, TypeLabel(f.MimeType), valid)
}

// exportFile exports a Google-native file to mime and returns the response
// (the caller closes the body).
func exportFile(ctx context.Context, svc *driveapi.Service, f *driveapi.File, mime string) (*http.Response, error) {
	resp, err := svc.Files.Export(f.Id, mime).Context(ctx).Download()
	if err != nil {
		if isExportTooLarge(err) {
			// Not wrapping err: the googleapi 403 would otherwise be
			// reported as a generic permission error.
			return nil, fmt.Errorf("%w: %q is too large to be exported as %s (Google limits exports to about 10 MB); open it in the browser instead: %s",
				ErrExportTooLarge, f.Name, mime, f.WebViewLink)
		}
		return nil, fmt.Errorf("export drive file %s as %s: %w", f.Id, mime, err)
	}
	return resp, nil
}

// downloadFile downloads the content of a regular file (the caller closes
// the body).
func downloadFile(ctx context.Context, svc *driveapi.Service, f *driveapi.File) (*http.Response, error) {
	resp, err := svc.Files.Get(f.Id).SupportsAllDrives(true).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("download drive file %s: %w", f.Id, err)
	}
	return resp, nil
}

// googleReasons returns the reasons of a googleapi.Error in err.
func googleReasons(err error) (*googleapi.Error, []string) {
	var ge *googleapi.Error
	if !errors.As(err, &ge) {
		return nil, nil
	}
	reasons := make([]string, 0, len(ge.Errors))
	for _, it := range ge.Errors {
		reasons = append(reasons, it.Reason)
	}
	return ge, reasons
}

// isExportTooLarge reports whether err is Drive's exportSizeLimitExceeded.
func isExportTooLarge(err error) bool {
	ge, reasons := googleReasons(err)
	if ge == nil {
		return false
	}
	for _, r := range reasons {
		if strings.EqualFold(r, "exportSizeLimitExceeded") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(ge.Message), "too large to be exported")
}

// isExportFormatRejected reports whether an export failed because the
// target format is not supported (HTTP 400 or an "unsupported" reason).
func isExportFormatRejected(err error) bool {
	ge, reasons := googleReasons(err)
	if ge == nil {
		return false
	}
	if ge.Code == http.StatusBadRequest {
		return true
	}
	for _, r := range reasons {
		if strings.Contains(strings.ToLower(r), "unsupported") || strings.Contains(strings.ToLower(r), "notsupported") {
			return true
		}
	}
	return false
}

// readLimited reads at most maxBytes from r. When more data is available
// it reports truncated and cuts the text on a UTF-8 boundary.
func readLimited(r io.Reader, maxBytes int64) (string, bool, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(b)) <= maxBytes {
		return string(b), false, nil
	}
	b = b[:maxBytes]
	// Drop a trailing incomplete UTF-8 sequence (at most 3 bytes).
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b), true, nil
}
