package gmail

import "testing"

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain text", "hello world", "hello world"},
		{"collapse whitespace", "<p>  hello \n\n   world  </p>", "hello world"},
		{"paragraphs", "<p>one</p><p>two</p>", "one\n\ntwo"},
		{"divs", "<div>one</div><div>two</div>", "one\ntwo"},
		{"br", "a<br>b<br/><br>c", "a\nb\n\nc"},
		{"entities", "<p>Tom &amp; Jerry &lt;3 &eacute;&#233;</p>", "Tom & Jerry <3 éé"},
		{"script and style dropped", "<style>.x{}</style><script>var a=1;</script><p>ok</p>", "ok"},
		{"link kept", `<a href="https://x.test/a">click</a>`, "click (https://x.test/a)"},
		{"link same as text", `<a href="https://x.test/">https://x.test</a>`, "https://x.test"},
		{"mailto same as text", `<a href="mailto:a@b.c">a@b.c</a>`, "a@b.c"},
		{"javascript link dropped", `<a href="javascript:void(0)">x</a>`, "x"},
		{"image link", `<a href="https://x.test/"><img src="i.png"></a>`, "(https://x.test/)"},
		{"image alt", `<p>Logo: <img alt="ACME"></p>`, "Logo: ACME"},
		{"list", "<ul><li>a</li><li>b</li></ul>", "- a\n- b"},
		{"table", "<table><tr><td>a</td><td>b</td></tr><tr><td>c</td><td>d</td></tr></table>", "a b\nc d"},
		{"pre keeps layout", "<pre>  x\n    y</pre>", "x\n    y"},
		{"inline elements", "<p>a <b>bold</b><i>it</i> word</p>", "a boldit word"},
		{"nbsp", "a&nbsp;&nbsp;b", "a b"},
		{"zero width", "a\u200c\u200bb", "ab"},
		{"blank lines collapsed", "<p>a</p><br><br><br><p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTMLToText(tt.in); got != tt.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
