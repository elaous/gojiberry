package gox

import (
	"html"
	"strings"
)

// blockTags are tags whose own box is always block-level in ordinary
// HTML/CSS (independent of whatever stylesheet a given page applies)
// -- the set RenderIndented consults to decide whether a newline is
// safe to insert next to one of these, per its own doc comment.
var blockTags = map[string]bool{
	"div": true, "section": true, "header": true, "footer": true,
	"nav": true, "main": true, "article": true, "aside": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"form": true, "fieldset": true, "figure": true, "figcaption": true,
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "details": true, "summary": true,
}

// isBlockLevel reports whether n's own outermost box is block-level.
// Only KindElement nodes with a blockTags tag qualify -- a Fragment
// has no box of its own (see Frag's own doc comment), and Text/Raw
// nodes are always inline content, so both conservatively report
// false: the boundary logic in renderChildren below only ever needs
// to know "would a newline dropped right next to this specific node
// definitely collapse away", and for those two kinds it can't be
// sure, so it declines rather than guesses.
func isBlockLevel(n Node) bool {
	return n.Kind == KindElement && blockTags[n.Tag]
}

// canBreakBoundary reports whether a newline is safe to insert
// between two adjacent siblings a and b. Per the CSS white-space
// processing rules, a run of collapsible whitespace sitting at the
// edge of a line gets trimmed away entirely rather than rendered --
// and a block-level box on either side of that whitespace always
// forces a line boundary right there, regardless of the box on the
// *other* side or of the parent's own display mode (normal flow,
// flex, or grid: flex/grid only change how the surviving boxes are
// positioned, not this trimming rule). So it's enough for *one* of
// the two neighbors to be block-level; the risky case this stays
// conservative about is two adjacent *inline*-level siblings (say, an
// icon <span> immediately followed by a label <span>, or two <button>
// elements in a row) with nothing block-level directly next to the
// gap between them -- there, the inserted whitespace sits mid-line
// and a browser renders it as one visible space, actually changing
// layout (the classic "inline-block whitespace gap" -- exactly why
// e.g. two toolbar buttons can't just be pretty-printed apart without
// first being grouped under a real block-level wrapper, see
// editor/ui/toolbar.gox's own .toolbar-group divs).
func canBreakBoundary(a, b Node) bool {
	return isBlockLevel(a) || isBlockLevel(b)
}

// RenderIndented is Render's pretty-printing counterpart. It always
// recurses into every element/fragment's own children (so nesting
// depth alone never blocks formatting), but only ever inserts a
// newline+indentation at a sibling boundary where canBreakBoundary
// says it's safe, and only ever descends into a node's children at
// all when none of them is a Text or Raw node directly (an element
// mixing in real inline text -- an icon span glued to a text run
// inside one <button>, say -- is rendered whole, byte-for-byte
// identical to Render's own single-line output for that entire
// subtree, exactly like today).
func RenderIndented(n Node) (string, error) {
	var b strings.Builder
	pw := &prettyWriter{b: &b}
	pw.render(n, 0)
	return b.String(), pw.err
}

type prettyWriter struct {
	b   *strings.Builder
	err error
}

// hasTextOrRawChild reports whether any of children is a KindText or
// KindRaw node -- if so, the parent carries real inline content this
// package can't safely add whitespace around, so the whole parent
// renders as one Render-compatible unit instead of being recursed
// into further.
func hasTextOrRawChild(children []Node) bool {
	for _, c := range children {
		if c.Kind == KindText || c.Kind == KindRaw {
			return true
		}
	}
	return false
}

func (pw *prettyWriter) render(n Node, depth int) {
	if pw.err != nil {
		return
	}
	if (n.Kind != KindElement && n.Kind != KindFragment) ||
		len(n.Children) == 0 || hasTextOrRawChild(n.Children) {
		s, err := RenderString(n)
		if err != nil {
			pw.err = err
			return
		}
		pw.b.WriteString(s)
		return
	}
	if n.Kind == KindFragment {
		// A Fragment has no box or opening tag of its own, so unlike an
		// Element below there's nothing to put the first child on a new
		// line *after* -- whatever preceded this Fragment (its own
		// parent's open tag, or nothing at all for a bare top-level
		// RenderIndented(someFragment) call) already decided that.
		pw.renderChildren(n.Children, depth)
		return
	}
	pw.writeOpenTag(n)
	pw.newline(depth + 1)
	pw.renderChildren(n.Children, depth+1)
	pw.newline(depth)
	pw.b.WriteString("</")
	pw.b.WriteString(n.Tag)
	pw.b.WriteString(">")
}

// renderChildren renders each of children in order, inserting a
// newline+indentation before every one *after* the first whose
// boundary with the previous sibling canBreakBoundary allows (the
// first child's own leading newline, when it gets one, is its
// parent's responsibility -- see render above).
func (pw *prettyWriter) renderChildren(children []Node, depth int) {
	for i, c := range children {
		if i > 0 && canBreakBoundary(children[i-1], c) {
			pw.newline(depth)
		}
		pw.render(c, depth)
	}
}

func (pw *prettyWriter) newline(depth int) {
	pw.b.WriteString("\n")
	pw.b.WriteString(strings.Repeat("  ", depth))
}

// writeOpenTag duplicates renderElement's own attribute-writing logic
// (rather than reusing it directly) so RenderIndented can interleave
// its own newlines/indentation between children instead of the plain
// recursive call renderElement always makes -- see that function for
// the byte-for-byte format this must match, since
// TestRenderIndentedMatchesRenderByteForByte proves the two only ever
// differ by inserted whitespace.
func (pw *prettyWriter) writeOpenTag(n Node) {
	pw.b.WriteString("<")
	pw.b.WriteString(n.Tag)
	for _, a := range n.Attrs {
		if a.Handler != nil {
			continue
		}
		pw.b.WriteString(" ")
		pw.b.WriteString(html.EscapeString(a.Name))
		pw.b.WriteString(`="`)
		pw.b.WriteString(html.EscapeString(a.Value))
		pw.b.WriteString(`"`)
	}
	pw.b.WriteString(">")
}
