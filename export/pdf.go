package export

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strconv"
	"strings"

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
	w, h := snap.PageWidth, snap.PageHeight
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pxToPt(w), H: pxToPt(h)}})
	if err := registerPDFFonts(&pdf); err != nil {
		return nil, err
	}
	if snap.Title != "" {
		pdf.SetInfo(gopdf.PdfInfo{Title: snap.Title}) // a reader shows and announces the title, not the file name
	}
	pdf.SetMargins(pxToPt(snap.MarginLeft), pxToPt(snap.MarginTop), pxToPt(snap.MarginRight), pxToPt(snap.MarginBottom))
	pdf.AddPage()

	contentWidth := pxToPt(w) - pxToPt(snap.MarginLeft) - pxToPt(snap.MarginRight)

	for i := 0; i < len(snap.Blocks); i++ {
		b := snap.Blocks[i]
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
						return nil, err
					}
					i += n - 1
				} else {
					tokens := []pdfToken{{text: "[Table]", format: Format{Italic: true}}}
					wrapped, err := wrapPDFTokens(&pdf, tokens, contentWidth, false, false, true, pdfBaseFontSize)
					if err != nil {
						return nil, err
					}
					if err := drawPDFWrappedBlock(&pdf, nil, wrapped, pxToPt(snap.MarginLeft), pdfBaseFontSize, false, false, true, "", 0, pdfTag{kind: tagP, id: i}); err != nil {
						return nil, err
					}
				}
			}
			continue
		}
		if err := renderPDFBlock(&pdf, snap, b, i, contentWidth); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if _, err := pdf.WriteTo(&buf); err != nil {
		return nil, err
	}
	return tagPDF(buf.Bytes(), snap)
}

// registerPDFFonts loads the Go font family's four style variants
// (Regular/Bold/Italic/BoldItalic, all embedded as Go byte slices via
// golang.org/x/image/font/gofont -- no font file on disk needed) under
// one family name gopdf.SetFont switches between via its own style
// string, plus a separate monospace family for code blocks.
func registerPDFFonts(pdf *gopdf.GoPdf) error {
	variants := []struct {
		data  []byte
		style int
	}{
		{goregular.TTF, gopdf.Regular},
		{gobold.TTF, gopdf.Bold},
		{goitalic.TTF, gopdf.Italic},
		{gobolditalic.TTF, gopdf.Bold | gopdf.Italic},
	}
	for _, v := range variants {
		if err := pdf.AddTTFFontDataWithOption(pdfFontFamily, v.data, gopdf.TtfOption{Style: v.style}); err != nil {
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
	return pdf.SetFont(pdfFontFamily, style, size)
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
	for li, tokens := range tokenLines {
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
