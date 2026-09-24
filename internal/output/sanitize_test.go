package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const (
	osc52 = "\x1b]52;c;ZWNobyBwd25lZA==\x07" // clipboard write
	csi   = "\x1b[2J\x1b[31m"                // clear screen, red
)

func TestSanitize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "Hello, señor 👋", "Hello, señor 👋"},
		{"newline and tab kept", "a\tb\nc", "a\tb\nc"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"lone cr", "safe\rEVIL", "safe\uFFFDEVIL"},
		{"trailing cr", "line\r", "line"},
		{"osc 52", "x" + osc52 + "y", "x\uFFFD]52;c;ZWNobyBwd25lZA==\uFFFDy"},
		{"csi", csi + "text", "\uFFFD[2J\uFFFD[31mtext"},
		{"c1 csi", "a\u009b31mb", "a\uFFFD31mb"},
		{"del and nul", "a\x7fb\x00c", "a\uFFFDb\uFFFDc"},
		{"bidi override", "invoice\u202Efdp.exe", "invoice\uFFFDfdp.exe"},
		{"bidi isolate", "\u2066x\u2069", "\uFFFDx\uFFFD"},
		{"invalid utf-8", "a\x9bb", "a\uFFFDb"},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("%s: Sanitize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestSanitizingWriterSplitWrites(t *testing.T) {
	in := "é\r\n" + osc52 + "→\u202E|\r"
	want := Sanitize(in)
	// Feed the input one byte at a time: multi-byte runes and CRLF pairs
	// span writes.
	var b bytes.Buffer
	sw := NewSanitizingWriter(&b)
	for i := range len(in) {
		if n, err := sw.Write([]byte{in[i]}); n != 1 || err != nil {
			t.Fatalf("write: %d, %v", n, err)
		}
	}
	if err := sw.Flush(); err != nil {
		t.Fatal(err)
	}
	if b.String() != want {
		t.Errorf("split writes = %q, want %q", b.String(), want)
	}

	b.Reset()
	sw = NewSanitizingWriter(&b)
	_, _ = sw.Write([]byte("x\xe2\x80")) // truncated rune at the end
	_ = sw.Flush()
	if b.String() != "x\uFFFD" {
		t.Errorf("truncated rune = %q", b.String())
	}
}

func TestPrintTextSanitizes(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, FormatText)
	subject := "Hi" + osc52 + csi
	err := p.Print(nil, func(w io.Writer) error {
		if err := Table(w, []string{"SUBJECT"}, [][]string{{subject}}); err != nil {
			return err
		}
		if err := KeyValues(w, "Subject", subject); err != nil {
			return err
		}
		_, err := io.WriteString(w, "body line 1\n"+csi+"line 2\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("escape sequence reached the output: %q", out)
	}
	if !strings.Contains(out, "body line 1\n\uFFFD[2J") {
		t.Errorf("body newlines must survive: %q", out)
	}

	// Table and KeyValues sanitize on their own, even without Printer.
	b.Reset()
	_ = KeyValues(&b, "From", "a\x1b]0;title\x07b")
	if strings.ContainsAny(b.String(), "\x1b\x07") {
		t.Errorf("KeyValues leaked controls: %q", b.String())
	}

	// JSON output is unchanged: encoding/json escapes controls itself.
	b.Reset()
	if err := New(&b, FormatJSON).Print(map[string]string{"s": subject}, nil); err != nil {
		t.Fatal(err)
	}
	var back map[string]string
	if err := json.Unmarshal(b.Bytes(), &back); err != nil || back["s"] != subject {
		t.Errorf("json round trip = %q, %v", back["s"], err)
	}
	if strings.ContainsRune(b.String(), '\x1b') {
		t.Errorf("raw ESC in JSON: %q", b.String())
	}
}
