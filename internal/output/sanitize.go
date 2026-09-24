package output

import (
	"io"
	"strings"
	"unicode/utf8"
)

// replacement is written in place of characters that must not reach a
// terminal.
const replacement = "\uFFFD"

// unsafeRune reports whether r could drive or spoof a terminal: C0 controls
// (except newline and tab), DEL, C1 controls (U+0080-U+009F, e.g. the
// single-character CSI and OSC) and the bidirectional override and isolate
// characters (U+202A-U+202E, U+2066-U+2069). Carriage returns are handled
// by the callers.
func unsafeRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// Sanitize makes untrusted text (mail subjects and bodies, file names, chat
// messages...) safe to print on a terminal. Escape sequences such as CSI
// ("\x1b[2J") or OSC 52 ("\x1b]52;c;...\x07", clipboard write) are
// neutralized by replacing every control character except newline and tab,
// every bidi override/isolate character and every invalid UTF-8 byte with
// U+FFFD. CRLF becomes LF and a carriage return at the end is dropped;
// any other carriage return (which could overwrite a line) is replaced.
//
// JSON output does not need this: encoding/json escapes control characters.
func Sanitize(s string) string {
	if isSafe(s) {
		return s
	}
	var b strings.Builder
	sw := NewSanitizingWriter(&b)
	_, _ = sw.Write([]byte(s))
	_ = sw.Flush()
	return b.String()
}

// isSafe is the fast path of Sanitize.
func isSafe(s string) bool {
	for _, r := range s {
		if r == '\r' || r == utf8.RuneError || unsafeRune(r) {
			return false
		}
	}
	return true
}

// SanitizingWriter applies Sanitize to a byte stream. It handles UTF-8
// sequences and CRLF pairs split across writes; call Flush after the last
// Write.
type SanitizingWriter struct {
	w       io.Writer
	pending []byte
}

// NewSanitizingWriter returns a SanitizingWriter writing to w.
func NewSanitizingWriter(w io.Writer) *SanitizingWriter {
	return &SanitizingWriter{w: w}
}

// Write implements io.Writer. It reports len(p) on success, even though the
// bytes written to the underlying writer may differ.
func (s *SanitizingWriter) Write(p []byte) (int, error) {
	data := p
	if len(s.pending) > 0 {
		data = append(s.pending, p...)
		s.pending = nil
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		c := data[i]
		switch {
		case c == '\r':
			if i+1 == len(data) {
				s.pending = append(s.pending, c)
				i++
				continue
			}
			if data[i+1] != '\n' {
				out = append(out, replacement...)
			}
			i++
		case c < utf8.RuneSelf:
			if unsafeRune(rune(c)) {
				out = append(out, replacement...)
			} else {
				out = append(out, c)
			}
			i++
		case !utf8.FullRune(data[i:]):
			s.pending = append(s.pending, data[i:]...)
			i = len(data)
		default:
			r, size := utf8.DecodeRune(data[i:])
			if (r == utf8.RuneError && size == 1) || unsafeRune(r) {
				out = append(out, replacement...)
			} else {
				out = append(out, data[i:i+size]...)
			}
			i += size
		}
	}
	if _, err := s.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes what is left of an incomplete trailing sequence: a dangling
// carriage return is dropped and a truncated UTF-8 sequence becomes U+FFFD.
func (s *SanitizingWriter) Flush() error {
	rest := s.pending
	s.pending = nil
	if len(rest) == 0 || (len(rest) == 1 && rest[0] == '\r') {
		return nil
	}
	_, err := io.WriteString(s.w, replacement)
	return err
}
