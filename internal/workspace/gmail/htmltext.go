package gmail

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTMLToText converts an HTML email body to readable plain text: script,
// style and head content are dropped, block elements become line breaks,
// entities are decoded, whitespace is collapsed and links are kept as
// "text (url)".
func HTMLToText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		// html.Parse only fails on reader errors; keep the input readable.
		return normalizeText(s)
	}
	var w textWriter
	w.node(doc)
	return w.result()
}

// blankLineElements are separated from their surroundings by a blank line.
var blankLineElements = map[atom.Atom]bool{
	atom.P: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true,
	atom.H5: true, atom.H6: true, atom.Blockquote: true, atom.Table: true,
	atom.Ul: true, atom.Ol: true, atom.Pre: true, atom.Hr: true,
}

// lineElements start and end on their own line.
var lineElements = map[atom.Atom]bool{
	atom.Div: true, atom.Li: true, atom.Tr: true, atom.Section: true,
	atom.Article: true, atom.Header: true, atom.Footer: true, atom.Nav: true,
	atom.Aside: true, atom.Main: true, atom.Dl: true, atom.Dt: true,
	atom.Dd: true, atom.Figure: true, atom.Figcaption: true, atom.Address: true,
	atom.Form: true, atom.Fieldset: true, atom.Center: true, atom.Caption: true,
	atom.Tbody: true, atom.Thead: true, atom.Tfoot: true,
}

// skippedElements are not rendered at all.
var skippedElements = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Title: true,
	atom.Noscript: true, atom.Template: true, atom.Svg: true, atom.Object: true,
	atom.Iframe: true,
}

// invisibleRunes are zero-width characters that newsletters use as padding.
var invisibleRunes = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "", "\u034f", "", "\u00ad", "",
)

// manyNewlines matches runs of blank lines to collapse.
var manyNewlines = regexp.MustCompile(`\n{3,}`)

// textWriter accumulates text, deferring separators so that consecutive
// block boundaries collapse into at most one blank line.
type textWriter struct {
	b          strings.Builder
	newlines   int  // pending line breaks (0..2)
	space      bool // pending space
	preDepth   int  // > 0 inside <pre>
	lineLength int  // runes written on the current line
}

func (w *textWriter) breakLines(n int) {
	w.newlines = min(max(w.newlines, n), 2)
}

func (w *textWriter) flush() {
	if w.b.Len() == 0 {
		w.newlines, w.space = 0, false
		return
	}
	if w.newlines > 0 {
		w.b.WriteString(strings.Repeat("\n", w.newlines))
		w.newlines, w.space, w.lineLength = 0, false, 0
		return
	}
	if w.space && w.lineLength > 0 {
		w.b.WriteByte(' ')
		w.lineLength++
	}
	w.space = false
}

func (w *textWriter) text(s string) {
	s = invisibleRunes.Replace(s)
	if w.preDepth > 0 {
		if s == "" {
			return
		}
		w.flush()
		w.b.WriteString(s)
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			w.lineLength = len(s) - i - 1
		} else {
			w.lineLength += len(s)
		}
		return
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		if s != "" {
			w.space = true
		}
		return
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		w.space = true
	}
	for i, word := range words {
		if i > 0 {
			w.space = true
		}
		w.flush()
		w.b.WriteString(word)
		w.lineLength += len(word)
	}
	if r, _ := utf8.DecodeLastRuneInString(s); unicode.IsSpace(r) {
		w.space = true
	}
}

func (w *textWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
	case html.DocumentNode:
		w.children(n)
		return
	default:
		return
	}

	a := n.DataAtom
	switch {
	case skippedElements[a]:
		return
	case a == atom.Br:
		w.newlines = min(w.newlines+1, 2)
		return
	case a == atom.Hr:
		w.breakLines(2)
		return
	case a == atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			w.text(" " + alt + " ")
		}
		return
	case a == atom.A:
		w.children(n)
		href := strings.TrimSpace(attr(n, "href"))
		label := strings.Join(strings.Fields(textContent(n)), " ")
		if keepLink(href, label) {
			w.text(" (" + href + ")")
		}
		return
	case a == atom.Td || a == atom.Th:
		w.space = true
		w.children(n)
		w.space = true
		return
	case a == atom.Li:
		w.breakLines(1)
		w.flush()
		w.b.WriteString("- ")
		w.lineLength += 2
		w.children(n)
		w.breakLines(1)
		return
	case a == atom.Pre:
		w.breakLines(2)
		w.preDepth++
		w.children(n)
		w.preDepth--
		w.breakLines(2)
		return
	case blankLineElements[a]:
		w.breakLines(2)
		w.children(n)
		w.breakLines(2)
		return
	case lineElements[a]:
		w.breakLines(1)
		w.children(n)
		w.breakLines(1)
		return
	}
	w.children(n)
}

func (w *textWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

func (w *textWriter) result() string {
	s := manyNewlines.ReplaceAllString(w.b.String(), "\n\n")
	return strings.TrimSpace(s)
}

// keepLink reports whether href is worth printing after the link text.
func keepLink(href, label string) bool {
	lower := strings.ToLower(href)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "mailto:") {
		return false
	}
	if label == "" {
		return true
	}
	return href != label && strings.TrimPrefix(href, "mailto:") != label &&
		strings.TrimSuffix(href, "/") != strings.TrimSuffix(label, "/")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		if n.Type == html.ElementNode && skippedElements[n.DataAtom] {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return invisibleRunes.Replace(b.String())
}
