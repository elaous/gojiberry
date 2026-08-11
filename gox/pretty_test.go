package gox

import (
	"strings"
	"testing"
)

// TestRenderIndentedBreaksBetweenBlockLevelSiblingsOnly proves the
// core case: consecutive block-level element children (div, ul/li)
// get a newline+indent between them, recursed at every depth.
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

// TestRenderIndentedBreaksNextToASingleInlineSibling proves that an
// inline-level child (span) flanked by block-level siblings still
// gets its own boundaries broken -- canBreakBoundary only needs *one*
// side of a gap to be block-level, matching the real CSS rule that a
// block-level box's edge always forces a line boundary regardless of
// what's on the other side.
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

// TestRenderIndentedNeverBreaksBetweenTwoInlineSiblings proves the
// one case RenderIndented has to stay conservative about: the sibling
// boundary between two adjacent inline-level elements (button, button
// -- exactly the shape a row of toolbar buttons has, each with an
// icon span and a text label the same way ui/toolbar.gox's own
// iconBtn builds one) with nothing block-level anywhere between them
// never gets a newline inserted, since that whitespace would sit
// mid-line and render as a real, visible gap. Each button itself
// still renders as one compact unit (its own icon-span-plus-text-
// label content is exactly the "mixes in real text" case
// RenderIndented always leaves untouched) -- unlike the trailing
// <select>, whose own single non-text <option> child still gets
// indented onto its own line the same safe way
// TestRenderIndentedBreaksNextToASingleInlineSibling's lone <span>
// does (a parent/child boundary, not a sibling one, so
// canBreakBoundary never even applies there); this test only asserts
// the two sibling boundaries that actually matter for a toolbar's own
// giant line stay glued, not that the whole subtree is newline-free.
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

// TestRenderIndentedMatchesRenderByteForByte proves RenderIndented
// only ever *adds* whitespace: stripping every newline and the
// indentation immediately following it must reproduce RenderString's
// own plain single-line output exactly, for a mix of breakable and
// unbreakable trees.
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

// stripIndentation removes every newline and the run of spaces
// immediately following it, mirroring exactly what RenderIndented
// itself inserts (newline() in pretty.go), so the result is directly
// comparable to RenderString's own output.
func stripIndentation(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(strings.TrimLeft(line, " "))
	}
	return b.String()
}

// TestRenderIndentedPassesMultilineRawThroughVerbatim proves a Raw
// child (dangerouslySetInnerHTML-equivalent, e.g. editor/ui/shell.gox's
// own bootScript, whose <script> content is real multi-line JavaScript
// with genuine leading-space indentation of its own) is emitted
// completely untouched -- RenderIndented must never insert, remove, or
// reindent so much as one byte inside it, since a naive "strip
// whitespace and compare" test (the mistake this test itself guards
// against) could easily eat real content that merely happens to look
// like the indentation this package adds.
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

// TestRenderIndentedLeavesTextMixedContainersAlone proves a container
// that mixes text and element children (e.g. inline formatting around
// a <b>) never gets a newline inserted anywhere inside it, since that
// whitespace would be visible -- while an *outer* container around it
// still breaks normally.
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
