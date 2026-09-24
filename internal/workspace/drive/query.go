package drive

import (
	"fmt"
	"strings"
	"time"

	"github.com/digio/gwork-cli/internal/timeutil"
)

// SearchOptions are the filters of Search. All filters are ANDed; the query
// always excludes trashed files.
type SearchOptions struct {
	// Text matches the file name and content (fullText contains).
	Text string
	// Name matches part of the file name (name contains).
	Name string
	// Type is a shortcut for common MIME types: doc, sheet, slides, pdf,
	// folder, image, form, drawing. It cannot be combined with MimeType.
	Type string
	// MimeType is an exact MIME type.
	MimeType string
	// Owner is an owner email address ('x' in owners).
	Owner string
	// FolderID restricts results to direct children of a folder.
	FolderID string
	// ModifiedAfter is a timeutil expression (2026-09-01, 7d, RFC 3339...)
	// resolved against Now.
	ModifiedAfter string
	// Now is the reference time for ModifiedAfter; zero means time.Now().
	Now time.Time
	// RawQuery is a Drive query (q syntax) ANDed with the other filters,
	// passed through unmodified.
	RawQuery string
	// Max is the maximum number of results (default DefaultMaxResults).
	Max int
	// OrderBy is a Drive orderBy expression. When empty, results are sorted
	// by "modifiedTime desc", except for full-text searches (Text, or a
	// RawQuery using fullText), which Drive only returns by relevance.
	OrderBy string
}

// quote returns s as a Drive query string literal: wrapped in single
// quotes with backslashes and single quotes escaped.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// BuildQuery builds the Drive "q" parameter for opts. User values are
// quoted and escaped, so they cannot alter the query structure; RawQuery is
// the only way to pass query syntax and is wrapped in parentheses.
//
//	BuildQuery(SearchOptions{Name: "budget", Type: "sheet"})
//	// trashed = false and name contains 'budget' and mimeType = 'application/vnd.google-apps.spreadsheet'
func BuildQuery(opts SearchOptions) (string, error) {
	clauses := []string{"trashed = false"}
	if t := strings.TrimSpace(opts.Text); t != "" {
		clauses = append(clauses, "fullText contains "+quote(t))
	}
	if n := strings.TrimSpace(opts.Name); n != "" {
		clauses = append(clauses, "name contains "+quote(n))
	}
	typ := strings.ToLower(strings.TrimSpace(opts.Type))
	mime := strings.TrimSpace(opts.MimeType)
	if typ != "" && mime != "" {
		return "", fmt.Errorf("use either a type shortcut (%s) or an explicit MIME type, not both", typ)
	}
	if typ != "" {
		m, ok := typeShortcuts[typ]
		if !ok {
			return "", fmt.Errorf("unknown file type %q (valid: %s)", opts.Type, strings.Join(TypeShortcuts(), ", "))
		}
		if typ == "image" {
			clauses = append(clauses, "mimeType contains "+quote(m))
		} else {
			clauses = append(clauses, "mimeType = "+quote(m))
		}
	}
	if mime != "" {
		clauses = append(clauses, "mimeType = "+quote(mime))
	}
	if o := strings.TrimSpace(opts.Owner); o != "" {
		clauses = append(clauses, quote(o)+" in owners")
	}
	if f := strings.TrimSpace(opts.FolderID); f != "" {
		clauses = append(clauses, quote(f)+" in parents")
	}
	if m := strings.TrimSpace(opts.ModifiedAfter); m != "" {
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		t, err := timeutil.Parse(m, now)
		if err != nil {
			return "", fmt.Errorf("modified after %q: %w", m, err)
		}
		clauses = append(clauses, "modifiedTime > "+quote(t.UTC().Format(time.RFC3339)))
	}
	if r := strings.TrimSpace(opts.RawQuery); r != "" {
		clauses = append(clauses, "("+r+")")
	}
	return strings.Join(clauses, " and "), nil
}
