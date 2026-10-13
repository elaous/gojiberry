package export

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/lang"
	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	pdfFontFamily     = "go"
	pdfMonoFontFamily = "gomono"

	// Font sizes deliberately match RTF's own \fsNN convention (in
	// half-points there -- \fs24/\fs36/\fs32/\fs28 -- so 12/18/16/14pt
	// here) so the two formats read as the same document at a glance
	// rather than each inventing its own type scale.
	pdfBaseFontSize   = 12.0
	pdfHeading1Size   = 18.0
	pdfHeading2Size   = 16.0
	pdfHeading3Size   = 14.0
	pdfLineHeightMult = 1.35

	// pdfListIndentPerLevel/pdfQuoteIndent also mirror RTF's own
	// numbers (360/720 twips, 20 twips per point, is 18pt/36pt).
	pdfListIndentPerLevel = 18.0
	pdfQuoteIndent        = 36.0

	pdfUnderlineOffset     = 0.85 // * fontSize, below the line's own top
	pdfStrikethroughOffset = 0.5
	pdfLineStrokeWidth     = 0.6

	// pdfTableCellPadding is the inset (points) between a table cell's
	// own grid-line border and its wrapped text, on all four sides --
	// renderPDFTable's own doc comment has the rest of the table
	// layout's shape.
	pdfTableCellPadding = 4.0
)

// pxToPt converts one of editor's own CSS-px geometry values
// (Snapshot's PageWidth/Margin* etc.) to PDF points at the same 96dpi
// CSS convention gopdf's own conversionUnitPX constant assumes (72/96
// = 0.75) -- so Letter's 816px page width becomes exactly 612pt
// (8.5in), matching a real Letter page, not an approximation.
func pxToPt(px float64) float64 {
	return px * 0.75
}

// PDF renders a Snapshot to a paginated PDF document -- the first
// format that genuinely needs a third-party library: PDF is a real
// fixed-layout page-description format (fonts must be embedded as
// real glyph programs, text is drawn at explicit coordinates,
// pagination is the exporter's own job) with nothing in the standard
// library covering it, unlike RTF and EPUB which turned out to be
// plain-text/zip-of-small-files formats stdlib already covers. Uses
// github.com/signintech/gopdf (confirmed to build under both native
// and GOOS=js GOARCH=wasm), plus the Go project's own "Go" TrueType
// family (golang.org/x/image/font/gofont/...) embedded directly as Go
// byte slices -- gopdf always needs a real TTF registered (it has no
// built-in standard-14 font glyphs the way some PDF libraries do), and
// there is no font file anywhere in this repo to point it at, so this
// sidesteps needing one entirely.
//
// Unlike the canvas-rendered editor view, this does its own
// word-wrapping and pagination from scratch: the editor's own
// line-breaking is fundamentally tied to a real DOM Context2D's own
// MeasureText, not reusable against gopdf's own TTF-glyph metrics.
// The wrapping/drawing pipeline here (pdfTokenize/wrapPDFTokens/
// drawPDFWrappedBlock) is deliberately simple -- greedy space-
// delimited wrap, no hyphenation, no justification -- proportionate to
// every other format's own "content over presentation, not a
// layout-engine reimplementation" scope. Multi-column blocks render
// as plain single-column text: real multi-column PDF layout is a
// meaningfully separate feature, not attempted here.
//
// Inline fidelity is intentionally narrower than Markdown/HTML/RTF's
// own: Bold/Italic (via the four registered "go" style variants),
// Underline/Strikethrough (drawn as a manual Line() under/through each
// run's own measured span, since gopdf's Text/Cell draw glyphs only,
// no built-in decoration), and Link (a real clickable
// AddExternalLink annotation over the run's own bounding box) are all
// supported; Superscript/Subscript render as plain same-size, same-
// baseline text -- real superscript/subscript need accurate font-
// metric-based baseline shifting this package doesn't attempt to
// compute, so both are dropped rather than approximated badly, the
// same "flag what's simplified" posture RTF's own image-placeholder
// tradeoff already takes.
type PDF struct{}

func (PDF) Export(snap Snapshot) ([]byte, error) {
	var outline []OutlineEntry
	if snap.TOC {
		outline = snap.Outline()
	}
	// A table of contents needs the page of each heading, which is known only once the document is laid
	// out, and the contents themselves take pages. So the document is drawn with placeholder numbers, the
	// pages of the headings are read off, and it is drawn again with them: every entry is one line either
	// way, so the pages do not move (a third pass settles the rare case where they do).
	var pages map[int]int
	for pass := 0; ; pass++ {
		out, got, err := renderPDF(snap, outline, pages)
		if err != nil {
			return nil, err
		}
		if len(outline) == 0 || (pages != nil && maps.Equal(pages, got)) || pass >= 3 {
			return out, nil
		}
		pages = got
	}
}

// renderPDF draws the document once. outline is the table of contents (none if empty) and tocPages the
// page of each heading by block index from the pass before (placeholders when nil); the pages the
// headings are on in this pass are returned.
func renderPDF(snap Snapshot, outline []OutlineEntry, tocPages map[int]int) ([]byte, map[int]int, error) {
	headingPages := map[int]int{}
	w, h := snap.PageWidth, snap.PageHeight
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pxToPt(w), H: pxToPt(h)}})
	if err := registerPDFFonts(&pdf); err != nil {
		return nil, nil, err
	}
	if snap.Title != "" {
		pdf.SetInfo(gopdf.PdfInfo{Title: snap.Title}) // a reader shows and announces the title, not the file name
	}
	flow := &pdfFlowState{pageH: pxToPt(h)}
	pdfFlows.Store(&pdf, flow)
	defer pdfFlows.Delete(&pdf)
	pdf.SetMargins(pxToPt(snap.MarginLeft), pxToPt(snap.MarginTop), pxToPt(snap.MarginRight), pxToPt(snap.MarginBottom))
	pdf.AddPage()

	contentWidth := pxToPt(w) - pxToPt(snap.MarginLeft) - pxToPt(snap.MarginRight)

	if len(outline) > 0 {
		if err := drawPDFContents(&pdf, snap, outline, tocPages, contentWidth); err != nil {
			return nil, nil, err
		}
	}

	for i := 0; i < len(snap.Blocks); i++ {
		b := snap.Blocks[i]
		if b.Kind >= Heading1 && b.Kind <= Heading3 {
			// The heading's page is the one its first line lands on, which may be the next page.
			pdf.SetNewY(pdf.GetY(), pdfHeading1Size*pdfLineHeightMult)
			headingPages[i] = pdf.GetNumberOfPages()
		}
		if b.Kind == TableCell {
			// See Markdown's own TableCell case (markdown.go) for the
			// same "read the whole table out of snap.Blocks[i:i+n] the
			// moment this loop reaches its own first cell" shape --
			// renderPDFTable's own doc comment has the PDF-table
			// specifics. Handled directly here (an indexed loop, so it
			// can skip past every cell it just consumed) rather than
			// through renderPDFBlock's own one-block-at-a-time contract,
			// which has no way to skip ahead.
			if b.TableRow == 0 && b.TableCol == 0 {
				n := b.TableRows * b.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					if err := renderPDFTable(&pdf, snap.Blocks[i:i+n], i, contentWidth, pxToPt(snap.MarginLeft)); err != nil {
						return nil, nil, err
					}
					i += n - 1
				} else {
					tokens := []pdfToken{{text: "[Table]", format: Format{Italic: true}}}
					wrapped, err := wrapPDFTokens(&pdf, tokens, contentWidth, false, false, true, pdfBaseFontSize)
					if err != nil {
						return nil, nil, err
					}
					if err := drawPDFWrappedBlock(&pdf, nil, wrapped, pxToPt(snap.MarginLeft), pdfBaseFontSize, false, false, true, "", 0, pdfTag{kind: tagP, id: i}); err != nil {
						return nil, nil, err
					}
				}
			}
			continue
		}
		// A heading's page is recorded before it is drawn, so it is never moved by the widow rule.
		flow.noWidow = b.Kind >= Heading1 && b.Kind <= Heading3
		err := renderPDFBlock(&pdf, snap, b, i, contentWidth)
		flow.noWidow = false
		if err != nil {
			return nil, nil, err
		}
	}

	if err := drawPDFHeadersAndFooters(&pdf, snap); err != nil {
		return nil, nil, err
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, nil, err
	}
	out, err := tagPDF(buf.Bytes(), snap)
	return out, headingPages, err
}

// drawPDFContents draws the table of contents: its title, then one line per heading with a dotted leader
// to the page number, indented by level, and a page break after it. The entries are tagged as paragraphs
// (their ids come after those of the blocks). An entry too long for its line is cut with an ellipsis.
func drawPDFContents(pdf *gopdf.GoPdf, snap Snapshot, outline []OutlineEntry, pages map[int]int, contentWidth float64) error {
	x0 := pxToPt(snap.MarginLeft)
	right := x0 + contentWidth
	const titleSize = 18.0
	id := len(snap.Blocks) + 1
	if err := setPDFFont(pdf, "B", titleSize); err != nil {
		return err
	}
	lh := titleSize * pdfLineHeightMult
	pdf.SetNewY(pdf.GetY(), lh)
	y := pdf.GetY()
	pdf.SetXY(x0, y)
	pdfMarkBegin(pdf, pdfTag{kind: tagP, id: id})
	if err := pdf.Cell(&gopdf.Rect{W: contentWidth, H: lh}, snap.tocTitle()); err != nil {
		return err
	}
	pdfMarkEnd(pdf)
	pdf.SetY(y + lh*1.3)

	lh = pdfBaseFontSize * pdfLineHeightMult
	for _, e := range outline {
		id++
		num := "0"
		if p, ok := pages[e.Block]; ok {
			num = strconv.Itoa(p)
		}
		style := ""
		if e.Level == 1 {
			style = "B"
		}
		if err := setPDFFont(pdf, style, pdfBaseFontSize); err != nil {
			return err
		}
		nw, err := pdf.MeasureTextWidth(num)
		if err != nil {
			return err
		}
		indent := float64(e.Level-1) * pdfListIndentPerLevel
		avail := contentWidth - indent - nw - 12
		text, err := fitPDFText(pdf, e.Text, avail)
		if err != nil {
			return err
		}
		tw, err := pdf.MeasureTextWidth(text)
		if err != nil {
			return err
		}
		pdf.SetNewY(pdf.GetY(), lh)
		y := pdf.GetY()
		pdf.SetXY(x0+indent, y)
		pdfMarkBegin(pdf, pdfTag{kind: tagP, id: id})
		if err := pdf.Cell(&gopdf.Rect{W: tw, H: lh}, text); err != nil {
			return err
		}
		// The leader: dots from the end of the text to the page number, drawn from the right so they
		// line up from one entry to the next.
		if err := setPDFFont(pdf, "", pdfBaseFontSize); err != nil {
			return err
		}
		dw, err := pdf.MeasureTextWidth(". ")
		if err != nil {
			return err
		}
		if n := int((right - nw - 6 - (x0 + indent + tw + 6)) / dw); n > 0 {
			pdf.SetXY(right-nw-6-float64(n)*dw, y)
			if err := pdf.Cell(&gopdf.Rect{W: float64(n) * dw, H: lh}, strings.Repeat(". ", n)); err != nil {
				return err
			}
		}
		pdf.SetXY(right-nw, y)
		if err := pdf.Cell(&gopdf.Rect{W: nw, H: lh}, num); err != nil {
			return err
		}
		pdfMarkEnd(pdf)
		pdf.SetY(y + lh)
	}
	// The document starts on a page of its own after the contents.
	pdf.AddPage()
	return nil
}

// fitPDFText cuts s with an ellipsis until it is no wider than width in the current font.
func fitPDFText(pdf *gopdf.GoPdf, s string, width float64) (string, error) {
	r := []rune(s)
	for len(r) > 0 {
		cand := string(r)
		if len(r) < len([]rune(s)) {
			cand += "…"
		}
		w, err := pdf.MeasureTextWidth(cand)
		if err != nil {
			return "", err
		}
		if w <= width {
			return cand, nil
		}
		r = r[:len(r)-1]
	}
	return "", nil
}

// drawPDFHeadersAndFooters draws the running header in the top margin and the footer in the bottom
// margin of every page, once the page count is known. Both are artifacts (not part of the content a
// reader announces), in 9 point type: the left, centre and right parts of the text (tab separated) sit at
// the left edge, the middle and the right edge of the text area, and the fields {page}, {pages} and
// {title} are filled in for each page.
func drawPDFHeadersAndFooters(pdf *gopdf.GoPdf, snap Snapshot) error {
	if snap.Header == "" && snap.Footer == "" {
		return nil
	}
	const size = 9.0
	lh := size * pdfLineHeightMult
	left, right := pxToPt(snap.MarginLeft), pxToPt(snap.PageWidth)-pxToPt(snap.MarginRight)
	pageH := pxToPt(snap.PageHeight)
	n := pdf.GetNumberOfPages()
	for p := 1; p <= n; p++ {
		if err := pdf.SetPage(p); err != nil {
			return err
		}
		for _, side := range []struct {
			text string
			y    float64
		}{
			{snap.Header, math.Max(4, (pxToPt(snap.MarginTop)-lh)/2)},
			{snap.Footer, math.Min(pageH-lh-4, pageH-pxToPt(snap.MarginBottom)+(pxToPt(snap.MarginBottom)-lh)/2)},
		} {
			if side.text == "" {
				continue
			}
			l, c, r := decorParts(side.text)
			if err := setPDFFont(pdf, "", size); err != nil {
				return err
			}
			pdfMarkArtifact(pdf)
			for i, part := range []string{l, c, r} {
				part = FillFields(part, p, n, snap.Title)
				if part == "" {
					continue
				}
				w, err := pdf.MeasureTextWidth(part)
				if err != nil {
					return err
				}
				x := left
				switch i {
				case 1:
					x = (left+right)/2 - w/2
				case 2:
					x = right - w
				}
				pdf.SetXY(x, side.y)
				if err := pdf.Cell(&gopdf.Rect{W: w, H: lh}, part); err != nil {
					return err
				}
			}
			pdfMarkEnd(pdf)
		}
	}
	return nil
}

// registerPDFFonts loads the Go font family's four style variants
// (Regular/Bold/Italic/BoldItalic, all embedded as Go byte slices via
// golang.org/x/image/font/gofont -- no font file on disk needed) under
// one family name gopdf.SetFont switches between via its own style
// string, plus a separate monospace family for code blocks.
func registerPDFFonts(pdf *gopdf.GoPdf) error {
	// Each variant is a family of its own, so each embedded font has its own name in the file: with one
	// name for four different fonts, a checker (or a reader) that finds a font by its name cannot tell
	// which one a piece of text is set in.
	variants := []struct {
		family string
		data   []byte
	}{
		{pdfFontFamily, goregular.TTF},
		{pdfFontFamily + "-bold", gobold.TTF},
		{pdfFontFamily + "-italic", goitalic.TTF},
		{pdfFontFamily + "-bolditalic", gobolditalic.TTF},
	}
	for _, v := range variants {
		if err := pdf.AddTTFFontData(v.family, v.data); err != nil {
			return err
		}
	}
	return pdf.AddTTFFontData(pdfMonoFontFamily, gomono.TTF)
}

// pdfToken is one drawable unit of a wrapped line: either a run of
// text sharing one Format (further split on spaces from segments'
// own maximal-run grouping, since wrapping needs to break at word
// boundaries, not format-run boundaries), or a forced break standing
// in for a soft line break.
type pdfToken struct {
	text    string
	format  Format
	isBreak bool
}

// pdfTokenize splits segs' own text into word/whitespace-run tokens
// so wrapPDFTokens can break lines between them, turning each
// SoftBreak segment into a forced pdfToken break.
func pdfTokenize(segs []Segment) []pdfToken {
	var tokens []pdfToken
	for _, seg := range segs {
		if seg.SoftBreak {
			tokens = append(tokens, pdfToken{isBreak: true})
			continue
		}
		for _, word := range splitOnSpaceRuns(seg.Text) {
			tokens = append(tokens, pdfToken{text: word, format: seg.Format})
		}
	}
	return tokens
}

// splitOnSpaceRuns splits s into alternating word/whitespace tokens
// (e.g. "a bb  c" -> ["a", " ", "bb", "  ", "c"]), never splitting
// within a run of the same kind -- wrapPDFTokens breaks lines only
// between these tokens, keeping every word intact.
func splitOnSpaceRuns(s string) []string {
	if s == "" {
		return nil
	}
	var words []string
	var cur strings.Builder
	runes := []rune(s)
	inSpace := runes[0] == ' '
	for _, r := range runes {
		isSpace := r == ' '
		if isSpace != inSpace {
			words = append(words, cur.String())
			cur.Reset()
			inSpace = isSpace
		}
		cur.WriteRune(r)
	}
	words = append(words, cur.String())
	return words
}

// setPDFTokenFont selects f's own effective style -- forceBold/
// forceItalic apply a block-level style (e.g. a heading is always
// bold) on top of, not instead of, the run's own Bold/Italic, the same
// way RTF's paragraph-level `\b`/`\i` prefix and its own run-level
// wrapRTFRunFormatting compose. mono ignores all of that: a code
// block's text is always plain monospace, no inline formatting
// applied, matching every other format's own "code is verbatim
// content" treatment.
func setPDFTokenFont(pdf *gopdf.GoPdf, f Format, mono, forceBold, forceItalic bool, size float64) error {
	if mono {
		return pdf.SetFont(pdfMonoFontFamily, "", size)
	}
	style := ""
	if forceBold || f.Bold {
		style += "B"
	}
	if forceItalic || f.Italic {
		style += "I"
	}
	return setPDFFont(pdf, style, size)
}

// setPDFFont selects the Go font variant for a style made of "B" and "I".
func setPDFFont(pdf *gopdf.GoPdf, style string, size float64) error {
	family := pdfFontFamily
	bold, italic := strings.Contains(style, "B"), strings.Contains(style, "I")
	switch {
	case bold && italic:
		family += "-bolditalic"
	case bold:
		family += "-bold"
	case italic:
		family += "-italic"
	}
	return pdf.SetFont(family, "", size)
}

// wrapPDFTokens greedily packs tokens into lines no wider than width,
// measuring each token with its own real font (setPDFTokenFont+
// MeasureTextWidth) rather than a single block-wide font, so a bold or
// italic run's own (typically wider) glyphs are measured accurately
// instead of assuming the block's plain-text metrics -- unlike
// gopdf's own SplitTextWithWordWrap, which only ever wraps a single
// plain string against one already-set font. A leading whitespace
// token on a new line is dropped (matching normal word-processor
// wrap); a trailing one is trimmed off the line just closed. isBreak
// tokens force a line break regardless of remaining width. An empty
// token stream still produces one (empty) line, so an empty block
// still consumes its own blank line -- the same "every block gets a
// line" invariant every other format's own block loop keeps.
func wrapPDFTokens(pdf *gopdf.GoPdf, tokens []pdfToken, width float64, mono, forceBold, forceItalic bool, size float64) ([][]pdfToken, error) {
	var lines [][]pdfToken
	var cur []pdfToken
	var curWidth float64
	for _, tok := range tokens {
		if tok.isBreak {
			lines = append(lines, trimTrailingSpaceToken(cur))
			cur, curWidth = nil, 0
			continue
		}
		if err := setPDFTokenFont(pdf, tok.format, mono, forceBold, forceItalic, size); err != nil {
			return nil, err
		}
		w, err := pdf.MeasureTextWidth(tok.text)
		if err != nil {
			return nil, err
		}
		if curWidth+w > width && len(cur) > 0 {
			lines = append(lines, trimTrailingSpaceToken(cur))
			cur, curWidth = nil, 0
			if strings.TrimSpace(tok.text) == "" {
				continue
			}
		}
		cur = append(cur, tok)
		curWidth += w
	}
	lines = append(lines, trimTrailingSpaceToken(cur))
	return lines, nil
}

// trimTrailingSpaceToken drops a line's own trailing whitespace-only
// token (its width would otherwise count against nothing -- there's
// no glyph after it worth right-padding for), the same "trim, don't
// preserve, boundary whitespace" idea softLineBreak handling already
// takes in wrapPDFTokens.
func trimTrailingSpaceToken(line []pdfToken) []pdfToken {
	if n := len(line); n > 0 && strings.TrimSpace(line[n-1].text) == "" {
		return line[:n-1]
	}
	return line
}

// renderPDFBlock draws one block: resolving its own font size/style/
// indent/prefix by BlockKind (mirroring RTF's own per-Kind switch),
// tokenizing and wrapping its text, then drawing each wrapped line via
// drawPDFWrappedBlock. Image skips text wrapping entirely in favor of
// renderPDFImage. Never called with a TableCell block -- PDF.Export
// intercepts every one of those itself (renderPDFTable), the only
// Kind this function doesn't handle.
func renderPDFBlock(pdf *gopdf.GoPdf, snap Snapshot, b Block, idx int, contentWidth float64) error {
	if b.Kind == Image {
		return renderPDFImage(pdf, snap, b, idx, contentWidth)
	}

	fontSize := pdfBaseFontSize
	var forceBold, forceItalic, mono bool
	var indent, hang float64
	var prefix string
	tag := pdfTag{kind: tagP, id: idx}

	switch b.Kind {
	case Heading1:
		fontSize, forceBold, tag.kind = pdfHeading1Size, true, tagH1
	case Heading2:
		fontSize, forceBold, tag.kind = pdfHeading2Size, true, tagH2
	case Heading3:
		fontSize, forceBold, tag.kind = pdfHeading3Size, true, tagH3
	case Quote:
		forceItalic, indent, tag.kind = true, pdfQuoteIndent, tagQuote
	case BulletItem:
		indent = pdfListIndentPerLevel * float64(b.Level+1)
		hang = pdfListIndentPerLevel
		prefix = "•"
		tag.kind, tag.b = tagLBody, b.Level
	case NumberedItem:
		indent = pdfListIndentPerLevel * float64(b.Level+1)
		hang = pdfListIndentPerLevel
		prefix = strconv.Itoa(b.NumberedIndex) + "."
		tag.kind, tag.a, tag.b = tagLBody, 1, b.Level
	case Code:
		mono = true
	}

	tokens := pdfTokenize(b.Segments)
	wrapped, err := wrapPDFTokens(pdf, tokens, contentWidth-indent, mono, forceBold, forceItalic, fontSize)
	if err != nil {
		return err
	}
	return drawPDFWrappedBlock(pdf, pdfRunLangs(snap), wrapped, pxToPt(snap.MarginLeft)+indent, fontSize, mono, forceBold, forceItalic, prefix, hang, tag)
}

// drawPDFWrappedBlock draws tokenLines starting at x0 (the block's own
// left edge, past any list/quote indent), page-breaking before each
// line via SetNewY exactly like gopdf's own README documents (checked
// once per line, not once per block, so a block taller than a full
// page still splits across pages rather than overflowing one). Cell's
// own "current x,y is upper-left corner" semantics (unlike Text's
// baseline-relative ones) let each token's own y stay the line's plain
// top coordinate, no font-ascent estimate needed.
func drawPDFWrappedBlock(pdf *gopdf.GoPdf, langs []string, tokenLines [][]pdfToken, x0, fontSize float64, mono, forceBold, forceItalic bool, prefix string, hang float64, tag pdfTag) error {
	lineHeight := fontSize * pdfLineHeightMult
	breakAt := pdfWidowBreak(pdf, len(tokenLines), lineHeight)
	for li, tokens := range tokenLines {
		if li == breakAt {
			// Widow and orphan control: the page ends here so that no page holds a lone first or last line.
			pdf.SetNewY(pdf.GetY(), pdfPageHeight(pdf))
		}
		pdf.SetNewY(pdf.GetY(), lineHeight)
		y := pdf.GetY()

		if li == 0 && prefix != "" {
			if err := setPDFTokenFont(pdf, Format{}, mono, forceBold, forceItalic, fontSize); err != nil {
				return err
			}
			pw, err := pdf.MeasureTextWidth(prefix)
			if err != nil {
				return err
			}
			pdf.SetXY(x0-hang, y)
			pdfMarkBegin(pdf, pdfTag{kind: tagLbl, id: tag.id, a: tag.a, b: tag.b})
			if err := pdf.Cell(&gopdf.Rect{W: pw, H: lineHeight}, prefix); err != nil {
				return err
			}
			pdfMarkEnd(pdf)
		}
		pdfMarkBegin(pdf, tag)

		// What is open in the content stream: the block's own piece, a language span (it stays open
		// over the tokens that share the language) or a link (one token); see pdfRun.
		run := pdfRun{pdf: pdf, tag: tag, langs: langs, mono: mono}
		x := x0
		for _, tok := range tokens {
			run.enter(tok.format)
			if err := setPDFTokenFont(pdf, tok.format, mono, forceBold, forceItalic, fontSize); err != nil {
				return err
			}
			w, err := pdf.MeasureTextWidth(tok.text)
			if err != nil {
				return err
			}
			pdf.SetXY(x, y)
			if err := pdf.Cell(&gopdf.Rect{W: w, H: lineHeight}, tok.text); err != nil {
				return err
			}
			isLink := tok.format.Href != ""
			if !mono && (tok.format.Underline || isLink) {
				// A link gets the same underline treatment even when
				// its own run isn't explicitly underlined: HTML's <a>/
				// RTF's HYPERLINK field both read as links because the
				// browser/Word styles that tag/field automatically --
				// a PDF link annotation has no such reader-side
				// convention, so without this a clickable region would
				// otherwise be visually indistinguishable from plain
				// text.
				pdf.SetLineWidth(pdfLineStrokeWidth)
				pdf.Line(x, y+fontSize*pdfUnderlineOffset, x+w, y+fontSize*pdfUnderlineOffset)
			}
			if !mono && tok.format.Strikethrough {
				pdf.SetLineWidth(pdfLineStrokeWidth)
				pdf.Line(x, y+fontSize*pdfStrikethroughOffset, x+w, y+fontSize*pdfStrikethroughOffset)
			}
			if !mono && isLink {
				pdf.AddExternalLink(tok.format.Href, x, y, w, lineHeight)
				run.leave()
			}
			x += w
		}
		run.finish()
		pdf.SetY(y + lineHeight)
	}
	return nil
}

// renderPDFTable draws a real bordered grid for cells -- one table's
// own full row-major TableRows*TableCols cell slice -- one row at a
// time: columns split contentWidth evenly (Snapshot carries no
// per-column width fractions to preserve instead, the same
// equal-split simplification renderPlainTextTable/renderRTFTable's
// own doc comments already take), row 0 rendered bold (mirroring
// model.TableCellColors' own "row 0 is always the header" convention,
// same as HTML's own <th>).
//
// Column merges (colspan) render for real: an owner cell with
// tableCellOwner.colSpan > 1 draws one wide box spanning every column
// it covers, and the cells it covers in *that same row* draw no box
// of their own at all (the owner's own wide box already fills that
// space) -- skipCell below is exactly "covered, and this is the
// owner's own row". Row merges (rowspan) are NOT attempted: unlike
// RTF's real \clvmgf/\clvmrg, safely reserving one shared height
// across multiple rows -- while each row can still independently
// need to page-break (SetNewY below, checked once per *row*, mirroring
// drawPDFWrappedBlock's own once-per-*line* check) -- is real
// complexity this function doesn't take on, so a cell covered by a
// rowspan in a *later* row than its owner still draws its own
// ordinary, empty box rather than visually joining the owner's above
// it: a disclosed degradation, not a silently wrong render.
//
// Two passes per row, the same shape TableRowHeightsAt's own wrap
// pre-pass (model/layoutmetrics.go) takes for the live canvas's
// analogous problem: first wrap every cell actually drawn in this row
// to learn the row's own shared height (the tallest cell's own
// wrapped line count) *before* deciding whether the row fits on the
// current page, then draw every box/text using that now-known height.
func renderPDFTable(pdf *gopdf.GoPdf, cells []Block, base int, contentWidth, x0 float64) error {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	owners := tableMergeGrid(cells)
	colWidth := contentWidth / float64(cols)
	lineHeight := pdfBaseFontSize * pdfLineHeightMult

	type cellDraw struct {
		x, w    float64
		wrapped [][]pdfToken // nil means "no box at all" (skipCell)
	}

	allDraws := make([][]cellDraw, rows)
	heights := make([]float64, rows)
	for r := 0; r < rows; r++ {
		draws := make([]cellDraw, cols)
		rowHeight := lineHeight + 2*pdfTableCellPadding
		for c := 0; c < cols; c++ {
			i := r*cols + c
			cell := cells[i]
			own := owners[i]
			skipCell := cell.TableCovered && own.r0 == r
			if skipCell {
				continue
			}
			w := colWidth
			if !cell.TableCovered && own.colSpan > 1 {
				w = colWidth * float64(own.colSpan)
			}
			var tokens []pdfToken
			if !cell.TableCovered {
				tokens = pdfTokenize(cell.Segments)
			}
			wrapped, err := wrapPDFTokens(pdf, tokens, w-2*pdfTableCellPadding, false, r == 0, false, pdfBaseFontSize)
			if err != nil {
				return err
			}
			if h := float64(len(wrapped))*lineHeight + 2*pdfTableCellPadding; h > rowHeight {
				rowHeight = h
			}
			draws[c] = cellDraw{x: x0 + float64(c)*colWidth, w: w, wrapped: wrapped}
		}

		allDraws[r], heights[r] = draws, rowHeight
	}

	for r := 0; r < rows; r++ {
		draws, rowHeight := allDraws[r], heights[r]
		// Widow and orphan control for rows: the header row is never alone at the foot of a page, and the
		// last row is never alone at the head of one (the row before it goes along).
		if r < rows-1 && (r == 0 || r == rows-2) {
			pdfKeepTogether(pdf, rowHeight+heights[r+1])
		}

		pdf.SetNewY(pdf.GetY(), rowHeight)
		y := pdf.GetY()
		pdf.SetLineWidth(pdfLineStrokeWidth)

		for c := 0; c < cols; c++ {
			d := draws[c]
			if d.w <= 0 {
				continue
			}
			pdfMarkArtifact(pdf)
			pdf.RectFromUpperLeftWithStyle(d.x, y, d.w, rowHeight, "D")
			pdfMarkEnd(pdf)
			kind := tagTD
			if r == 0 {
				kind = tagTH
			}
			pdfMarkBegin(pdf, pdfTag{kind: kind, id: base + r*cols + c, a: base, b: r<<16 | c})
			ty := y + pdfTableCellPadding
			for _, line := range d.wrapped {
				tx := d.x + pdfTableCellPadding
				for _, tok := range line {
					if err := setPDFTokenFont(pdf, tok.format, false, r == 0, false, pdfBaseFontSize); err != nil {
						return err
					}
					tw, err := pdf.MeasureTextWidth(tok.text)
					if err != nil {
						return err
					}
					pdf.SetXY(tx, ty)
					if err := pdf.Cell(&gopdf.Rect{W: tw, H: lineHeight}, tok.text); err != nil {
						return err
					}
					tx += tw
				}
				ty += lineHeight
			}
			pdfMarkEnd(pdf)
		}
		pdf.SetY(y + rowHeight)
	}
	return nil
}

// renderPDFImage draws an Image block's own decoded picture, scaled
// down (preserving aspect ratio) to fit contentWidth when it would
// otherwise overflow it -- gopdf's own ImageFrom takes a decoded
// image.Image directly, so the data: URL (already just base64 bytes)
// only needs an ordinary stdlib image.Decode, no third-party decoder.
// An undecodable image (a format image.Decode doesn't recognize, e.g.
// an SVG) falls back to the same "[Image]" placeholder PlainText/RTF
// already use rather than failing the whole export over one image.
func renderPDFImage(pdf *gopdf.GoPdf, snap Snapshot, b Block, idx int, contentWidth float64) error {
	img, err := decodeDataURLImage(b.ImageSrc)
	if err != nil {
		text := "[Image]"
		if b.ImageCaption != "" {
			text += " " + b.ImageCaption
		}
		wrapped, werr := wrapPDFTokens(pdf, pdfTokenize([]Segment{{Text: text}}), contentWidth, false, false, true, pdfBaseFontSize)
		if werr != nil {
			return werr
		}
		return drawPDFWrappedBlock(pdf, pdfRunLangs(snap), wrapped, pxToPt(snap.MarginLeft), pdfBaseFontSize, false, false, true, "", 0, pdfTag{kind: tagP, id: idx})
	}

	w, h := pxToPt(b.ImageW), pxToPt(b.ImageH)
	if w <= 0 || h <= 0 {
		bounds := img.Bounds()
		w, h = float64(bounds.Dx()), float64(bounds.Dy())
	}
	if w > contentWidth {
		h *= contentWidth / w
		w = contentWidth
	}

	pdf.SetNewY(pdf.GetY(), h)
	y := pdf.GetY()
	if b.ImageDecorative {
		pdfMarkArtifact(pdf)
	} else {
		pdfMarkBegin(pdf, pdfTag{kind: tagFigure, id: idx})
	}
	if err := pdf.ImageFrom(img, pxToPt(snap.MarginLeft), y, &gopdf.Rect{W: w, H: h}); err != nil {
		return err
	}
	pdfMarkEnd(pdf)
	pdf.SetY(y + h + pdfBaseFontSize*pdfLineHeightMult*0.3)

	if b.ImageCaption != "" {
		wrapped, werr := wrapPDFTokens(pdf, pdfTokenize([]Segment{{Text: b.ImageCaption}}), contentWidth, false, false, true, pdfBaseFontSize)
		if werr != nil {
			return werr
		}
		if err := drawPDFWrappedBlock(pdf, pdfRunLangs(snap), wrapped, pxToPt(snap.MarginLeft), pdfBaseFontSize, false, false, true, "", 0, pdfTag{kind: tagCaption, id: idx}); err != nil {
			return err
		}
	}
	return nil
}

// decodeDataURLImage decodes b.ImageSrc's own base64 payload (the part
// after the data: URL's first comma) into an image.Image via the
// standard library's own format sniffing (image.Decode, backed by the
// blank-imported image/png, image/jpeg, and image/gif decoders).
func decodeDataURLImage(dataURL string) (image.Image, error) {
	_, b64, ok := strings.Cut(dataURL, ",")
	if !ok {
		b64 = dataURL
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// pdfRunLangs lists the distinct run languages of the snapshot, in a fixed order: a marker names a
// language by its position here plus one (0 means none), and tagPDF finds the tag again from the
// same list.
func pdfRunLangs(snap Snapshot) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range snap.Blocks {
		for _, s := range b.Segments {
			if l := s.Format.Lang; l != "" && !seen[l] && lang.ValidTag(l) {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	sort.Strings(out)
	return out
}

// pdfRun keeps the marked content of one line in step with the tokens drawn: tokens in the language of
// the document stay in the block's own piece; a stretch in another language becomes a Span element
// with /Lang, and each link token a Link element (with /Lang too when its run has one), so a reader
// can switch voice and a link annotation has a place in the tree. A closed piece is reopened only
// when more of the block's own text follows, so no empty piece is written at the end of a line.
type pdfRun struct {
	pdf   *gopdf.GoPdf
	tag   pdfTag
	langs []string
	mono  bool
	state int // 0 the block's own piece, 1 a language span, 2 a link, 3 nothing open
	lang  string
}

func (r *pdfRun) langIndex(l string) int {
	for i, x := range r.langs {
		if x == l {
			return i + 1
		}
	}
	return 0
}

// enter opens the piece the next token belongs in.
func (r *pdfRun) enter(f Format) {
	link := !r.mono && f.Href != ""
	span := !link && r.langIndex(f.Lang) > 0
	switch {
	case link:
		r.reopen(pdfTag{kind: tagLink, a: r.langIndex(f.Lang)}, 2, f.Lang)
	case span:
		if r.state == 1 && r.lang == f.Lang {
			return
		}
		r.reopen(pdfTag{kind: tagSpan, a: r.langIndex(f.Lang)}, 1, f.Lang)
	default:
		if r.state != 0 {
			r.reopen(r.tag, 0, "")
		}
	}
}

func (r *pdfRun) reopen(t pdfTag, state int, lang string) {
	if r.state != 3 {
		pdfMarkEnd(r.pdf)
	}
	pdfMarkBegin(r.pdf, t)
	r.state, r.lang = state, lang
}

// leave closes a link after its token.
func (r *pdfRun) leave() {
	pdfMarkEnd(r.pdf)
	r.state = 3
}

// finish closes whatever is open at the end of the line.
func (r *pdfRun) finish() {
	if r.state != 3 {
		pdfMarkEnd(r.pdf)
		r.state = 3
	}
}

// pdfFlowState is what a render knows about the sheet that gopdf does not expose: its height, which the
// widow and orphan control needs to count the lines left on a page.
type pdfFlowState struct {
	pageH   float64
	noWidow bool
}

// pdfFlows maps the document being drawn to its state; renderPDF adds and removes its entry.
var pdfFlows sync.Map

func pdfPageHeight(pdf *gopdf.GoPdf) float64 {
	if v, ok := pdfFlows.Load(pdf); ok {
		return v.(*pdfFlowState).pageH
	}
	return 0
}

// pdfKeepTogether starts a new page when h points do not fit on the rest of this one (and this one is not
// still empty), so that what follows is drawn together.
func pdfKeepTogether(pdf *gopdf.GoPdf, h float64) {
	v, ok := pdfFlows.Load(pdf)
	if !ok {
		return
	}
	room := v.(*pdfFlowState).pageH - pdf.MarginBottom() - pdf.GetY()
	if room >= h || pdf.GetY() <= pdf.MarginTop()+0.5 {
		return
	}
	pdf.AddPage()
}

// pdfWidowBreak says before which line of a paragraph of n lines the page must end so that neither
// page holds a single line of it, or -1 when it need not. A paragraph with fewer than four lines that
// would split moves whole to the next page; a longer one leaves at least two lines on each page.
func pdfWidowBreak(pdf *gopdf.GoPdf, n int, lineHeight float64) int {
	v, ok := pdfFlows.Load(pdf)
	if !ok || n < 2 {
		return -1
	}
	st := v.(*pdfFlowState)
	if st.noWidow {
		return -1
	}
	room := st.pageH - pdf.MarginBottom() - pdf.GetY()
	fit := int(room/lineHeight + 1e-9)
	if fit >= n {
		return -1
	}
	brk := fit
	if n-brk < 2 {
		brk = n - 2
	}
	if brk < 2 {
		brk = 0
	}
	if brk == 0 && pdf.GetY() <= pdf.MarginTop()+0.5 {
		return -1 // already at the top of a page: nothing to gain by moving
	}
	return brk
}
