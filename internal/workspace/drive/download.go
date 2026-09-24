package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/api/option"
)

// ErrExists is returned by Download when the output file exists and Force
// is not set.
var ErrExists = errors.New("output file already exists")

// DownloadOptions configure Download.
type DownloadOptions struct {
	// Out is the destination path (required).
	Out string
	// ExportFormat is the export format of Google-native files (see
	// ExportFormats). Empty picks DefaultExportFormat for the file type.
	// It must be empty for regular files.
	ExportFormat string
	// Force allows overwriting an existing file.
	Force bool
}

// DownloadResult describes a completed download.
type DownloadResult struct {
	File FileSummary `json:"file"`
	// Path is the written file.
	Path string `json:"path"`
	// Bytes is the number of bytes written.
	Bytes int64 `json:"bytes"`
	// Exported reports whether the file was exported from a Google-native
	// format; ExportFormat and ContentMimeType describe the export.
	Exported        bool   `json:"exported"`
	ExportFormat    string `json:"export_format,omitempty"`
	ContentMimeType string `json:"content_mime_type"`
}

// Download streams the content of a file to opts.Out. Regular files are
// downloaded as-is; Google-native files are exported with
// opts.ExportFormat (default: docx for Docs, xlsx for Sheets, pptx for
// Slides, pdf for Drawings). The content is written to a temporary file in
// the destination directory and renamed into place once complete, so a
// failed download never leaves a partial file. Existing files are only
// replaced with opts.Force. The file is created with mode 0600 because
// Drive content is often private.
func Download(ctx context.Context, fileID string, opts DownloadOptions, clientOpts ...option.ClientOption) (*DownloadResult, error) {
	if opts.Out == "" {
		return nil, fmt.Errorf("an output path is required")
	}
	if err := checkOut(opts.Out, opts.Force); err != nil {
		return nil, err
	}
	format := strings.ToLower(strings.TrimSpace(opts.ExportFormat))
	if format != "" {
		if _, ok := ExportFormats[format]; !ok {
			return nil, fmt.Errorf("%w: unknown export format %q (valid: %s)", ErrUnsupported, opts.ExportFormat, strings.Join(ExportFormatNames(), ", "))
		}
	}

	svc, err := New(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	f, err := getAPIFile(ctx, svc, fileID)
	if err != nil {
		return nil, err
	}
	res := &DownloadResult{File: summaryFromAPI(f), Path: opts.Out}

	var resp *http.Response
	switch {
	case f.MimeType == MimeFolder:
		return nil, fmt.Errorf("%w: %q is a folder and cannot be downloaded", ErrUnsupported, f.Name)
	case f.MimeType == MimeShortcut:
		target := ""
		if f.ShortcutDetails != nil {
			target = f.ShortcutDetails.TargetId
		}
		return nil, fmt.Errorf("%w: %q is a shortcut; download its target instead: gwork drive download %s --out %s", ErrUnsupported, f.Name, target, opts.Out)
	case IsGoogleNative(f.MimeType):
		if format == "" {
			format = DefaultExportFormat(f.MimeType)
		}
		if format == "" {
			return nil, fmt.Errorf("%w: a Google %s cannot be exported; open it in the browser: %s", ErrUnsupported, TypeLabel(f.MimeType), f.WebViewLink)
		}
		mime := ExportFormats[format]
		if len(f.ExportLinks) > 0 {
			if _, ok := f.ExportLinks[mime]; !ok {
				return nil, fmt.Errorf("%w: a Google %s cannot be exported as %s (available: %s)", ErrUnsupported, TypeLabel(f.MimeType), format, availableFormats(f.ExportLinks))
			}
		}
		res.Exported, res.ExportFormat, res.ContentMimeType = true, format, mime
		resp, err = exportFile(ctx, svc, f, mime)
	default:
		if format != "" {
			return nil, fmt.Errorf("%w: --export-format only applies to Google Docs, Sheets, Slides and Drawings; %q (%s) is downloaded as-is", ErrUnsupported, f.Name, f.MimeType)
		}
		res.ContentMimeType = f.MimeType
		resp, err = downloadFile(ctx, svc, f)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	n, err := writeAtomic(opts.Out, resp.Body, opts.Force)
	if err != nil {
		return nil, err
	}
	res.Bytes = n
	return res, nil
}

// checkOut fails when out is a directory, or exists and force is false.
func checkOut(out string, force bool) error {
	st, err := os.Stat(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("check output %s: %w", out, err)
	case st.IsDir():
		return fmt.Errorf("output %s is a directory; pass a file path", out)
	case !force:
		return fmt.Errorf("%w: %s (use --force to overwrite)", ErrExists, out)
	}
	return nil
}

// writeAtomic copies r to a temporary file next to out and renames it to
// out. The temporary file is removed on any failure.
func writeAtomic(out string, r io.Reader, force bool) (n int64, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(out), "."+filepath.Base(out)+".gwork-*")
	if err != nil {
		return 0, fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if n, err = io.Copy(tmp, r); err != nil {
		return 0, fmt.Errorf("write %s: %w", out, err)
	}
	if err = tmp.Close(); err != nil {
		return 0, fmt.Errorf("write %s: %w", out, err)
	}
	// Re-check right before the rename: the file may have appeared while
	// downloading.
	if err = checkOut(out, force); err != nil {
		return 0, err
	}
	if err = os.Rename(tmp.Name(), out); err != nil {
		return 0, fmt.Errorf("rename to %s: %w", out, err)
	}
	return n, nil
}

// availableFormats returns the short names (or MIME types) of the export
// links of a file, sorted.
func availableFormats(links map[string]string) string {
	names := make([]string, 0, len(links))
	for mime := range links {
		names = append(names, formatForMime(mime))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
