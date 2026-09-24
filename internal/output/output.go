// Package output renders command results either as human readable text or
// as JSON. Every CLI command goes through a Printer so that --output json
// behaves the same everywhere.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format is an output format.
type Format string

// Supported formats.
const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ParseFormat validates s as an output format. The empty string means text.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case "", FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("invalid output format %q (want text or json)", s)
	}
}

// Printer writes results in the configured format.
type Printer struct {
	W      io.Writer
	Format Format
}

// New returns a Printer writing to w in format f.
func New(w io.Writer, f Format) *Printer {
	return &Printer{W: w, Format: f}
}

// Print renders v. In JSON mode v is encoded as indented JSON and textFn is
// ignored. In text mode textFn is called with a writer that sanitizes
// everything written to the destination (see Sanitize), so untrusted
// content cannot inject terminal escape sequences; when textFn is nil, v
// is printed with fmt's %v verb through the same filter.
func (p *Printer) Print(v any, textFn func(w io.Writer) error) error {
	if p.Format == FormatJSON {
		return WriteJSON(p.W, v)
	}
	sw := NewSanitizingWriter(p.W)
	var err error
	if textFn == nil {
		_, err = fmt.Fprintln(sw, v)
	} else {
		err = textFn(sw)
	}
	if ferr := sw.Flush(); err == nil {
		err = ferr
	}
	return err
}

// WriteJSON encodes v as indented JSON followed by a newline. HTML
// characters are not escaped so text content stays readable.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// NewTabWriter returns a tabwriter configured for aligned, space-padded
// columns. Callers must Flush it.
func NewTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// Table writes headers and rows as aligned columns. Tabs and newlines
// inside cells are replaced by spaces so they cannot break the layout, and
// cells are sanitized (see Sanitize).
// When headers is empty no header line is written.
func Table(w io.Writer, headers []string, rows [][]string) error {
	tw := NewTabWriter(w)
	if len(headers) > 0 {
		if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
			return err
		}
	}
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = cleanCell(c)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// KeyValues writes "key: value" pairs aligned on the colon. pairs must have
// an even length (key, value, key, value, ...); empty values are skipped.
// Values are flattened to one line and sanitized like Table cells.
func KeyValues(w io.Writer, pairs ...string) error {
	tw := NewTabWriter(w)
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		if _, err := fmt.Fprintf(tw, "%s:\t%s\n", pairs[i], cleanCell(pairs[i+1])); err != nil {
			return err
		}
	}
	return tw.Flush()
}

var cellReplacer = strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ")

// cleanCell flattens s to one sanitized line. Sanitizing here too keeps
// Table and KeyValues safe even on writers not wrapped by Printer.
func cleanCell(s string) string { return Sanitize(cellReplacer.Replace(s)) }

// Ellipsize shortens s to at most n runes, appending "…" when cut.
// n <= 0 returns s unchanged.
func Ellipsize(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
