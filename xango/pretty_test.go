package xango

import (
	"strings"
	"testing"
)

func TestRenderIndentedBreaksBetweenBlockLevelSiblingsOnly(t *testing.T) {
	n := El("div", []Attr{A("id", "root")},
		El("div", []Attr{A("id", "a")}, Text("one")),
		El("ul", nil,
			El("li", nil, Text("first")),
			El("li", nil, Text("second")),
		),
	)
	got, err := RenderIndented(n)
	if err != nil {
		t.Fatal(err)
	}
	want := "<div id=\"root\">\n" +
		"  <div id=\"a\">one</div>\n" +
		"  <ul>\n" +
		"    <li>first</li>\n" +
		"    <li>second</li>\n" +
		"  </ul>\n" +
		"</div>"
	if got != want {
		t.Errorf("RenderIndented:\n got:  %q\nwant: %q", got, want)
	}
}

func TestRenderIndentedBreaksNextToASingleInlineSibling(t *testing.T) {
	n := El("div", []Attr{A("id", "root")},
		El("div", nil, Text("one")),
		El("span", []Attr{A("id", "mid")}, Text("inline"), El("b", nil, Text("bit"))),
		El("div", nil, Text("three")),
	)
	got, err := RenderIndented(n)
	if err != nil {
		t.Fatal(err)
	}
	want := "<div id=\"root\">\n" +
		"  <div>one</div>\n" +
		"  <span id=\"mid\">inline<b>bit</b></span>\n" +
		"  <div>three</div>\n" +
		"</div>"
	if got != want {
		t.Errorf("RenderIndented:\n got:  %q\nwant: %q", got, want)
	}
}

func TestRenderIndentedNeverBreaksBetweenTwoInlineSiblings(t *testing.T) {
	n := El("div", []Attr{A("id", "toolbar")},
		El("button", nil, El("span", nil, Text("icon")), Text("A")),
		El("button", nil, El("span", nil, Text("icon")), Text("B")),
		El("select", nil, El("option", nil, Text("C"))),
	)
	got, err := RenderIndented(n)
	if err != nil {
		t.Fatal(err)
	}
	const wantGlued = `<button><span>icon</span>A</button><button><span>icon</span>B</button><select>`
	if !strings.Contains(got, wantGlued) {
		t.Errorf("expected the button-button and button-select sibling boundaries to stay glued (%q), got %q", wantGlued, got)
	}
}

func TestRenderIndentedMatchesRenderByteForByte(t *testing.T) {
	trees := []Node{
		El("div", []Attr{A("id", "root")},
			El("section", nil,
				El("div", nil, Text("leaf")),
				El("div", nil, El("span", nil, Text("nested inline"))),
			),
		),
		El("p", nil, Text("just text, no children to break")),
		Frag(El("div", nil, Text("a")), El("div", nil, Text("b"))),
		El("div", []Attr{A("id", "toolbar")},
			El("button", nil, Text("Bold")),
			El("div", nil, Text("group boundary")),
			El("select", nil, El("option", nil, Text("A"))),
		),
	}
	for i, n := range trees {
		indented, err := RenderIndented(n)
		if err != nil {
			t.Fatalf("tree %d: RenderIndented: %v", i, err)
		}
		plain, err := RenderString(n)
		if err != nil {
			t.Fatalf("tree %d: RenderString: %v", i, err)
		}
		if stripped := stripIndentation(indented); stripped != plain {
			t.Errorf("tree %d: RenderIndented with whitespace stripped = %q, want RenderString's plain output %q", i, stripped, plain)
		}
	}
}

func stripIndentation(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(strings.TrimLeft(line, " "))
	}
	return b.String()
}

func TestRenderIndentedPassesMultilineRawThroughVerbatim(t *testing.T) {
	const script = "\n    const go = new Go();\n    go.run(x);\n  "
	n := El("div", nil,
		El("div", nil, Text("before")),
		El("script", nil, Raw(script)),
	)
	got, err := RenderIndented(n)
	if err != nil {
		t.Fatal(err)
	}
	want := "<script>" + script + "</script>"
	if !strings.Contains(got, want) {
		t.Errorf("expected the <script>'s own Raw content passed through byte-for-byte, got %q (want it to contain %q)", got, want)
	}
}

func TestRenderIndentedLeavesTextMixedContainersAlone(t *testing.T) {
	n := El("div", nil,
		El("div", nil, Text("before")),
		El("p", nil, Text("some "), El("b", nil, Text("bold")), Text(" text")),
	)
	got, err := RenderIndented(n)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\n  <p") {
		t.Fatalf("expected the outer div to still break before its own <p> child, got %q", got)
	}
	if !strings.Contains(got, "some <b>bold</b> text") {
		t.Errorf("expected the <p>'s own mixed text/element content to stay on one line, got %q", got)
	}
}
