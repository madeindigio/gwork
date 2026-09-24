package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{"": FormatText, "text": FormatText, "JSON": FormatJSON, " json ": FormatJSON}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("expected error for yaml")
	}
}

type item struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, FormatJSON)
	called := false
	err := p.Print([]item{{ID: "1", Subject: "a<b>"}}, func(io.Writer) error { called = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("text func must not be called in JSON mode")
	}
	if !strings.Contains(buf.String(), `"subject": "a<b>"`) {
		t.Fatalf("unexpected JSON: %s", buf.String())
	}
	var back []item
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
}

func TestPrintText(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, FormatText)
	err := p.Print(nil, func(w io.Writer) error {
		_, err := fmt.Fprint(w, "hello\n")
		return err
	})
	if err != nil || buf.String() != "hello\n" {
		t.Fatalf("got %q, %v", buf.String(), err)
	}
	buf.Reset()
	if err := p.Print(42, nil); err != nil || buf.String() != "42\n" {
		t.Fatalf("got %q, %v", buf.String(), err)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	err := Table(&buf, []string{"ID", "SUBJECT"}, [][]string{
		{"1", "short"},
		{"12345", "with\ttab\nand newline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "ID     SUBJECT\n" +
		"1      short\n" +
		"12345  with tab and newline\n"
	if buf.String() != want {
		t.Fatalf("got:\n%q\nwant:\n%q", buf.String(), want)
	}
}

func TestKeyValues(t *testing.T) {
	var buf bytes.Buffer
	if err := KeyValues(&buf, "Account", "a@digio.es", "Empty", "", "Services", "gmail"); err != nil {
		t.Fatal(err)
	}
	want := "Account:   a@digio.es\nServices:  gmail\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestEllipsize(t *testing.T) {
	if got := Ellipsize("héllo world", 5); got != "héll…" {
		t.Fatalf("got %q", got)
	}
	if got := Ellipsize("hi", 5); got != "hi" {
		t.Fatalf("got %q", got)
	}
	if got := Ellipsize("hello", 0); got != "hello" {
		t.Fatalf("got %q", got)
	}
}
