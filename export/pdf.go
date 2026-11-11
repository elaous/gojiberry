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

	pdfBaseFontSize   = 12.0
	pdfHeading1Size   = 18.0
	pdfHeading2Size   = 16.0
	pdfHeading3Size   = 14.0
	pdfLineHeightMult = 1.35

	pdfListIndentPerLevel = 18.0
	pdfQuoteIndent        = 36.0

	pdfUnderlineOffset     = 0.85
	pdfStrikethroughOffset = 0.5
	pdfLineStrokeWidth     = 0.6

	pdfTableCellPadding = 4.0
)

func pxToPt(px float64) float64 {
	return px * 0.75
}

// PDF renders a Snapshot to a paginated PDF document.
type PDF struct{}

func (PDF) Export(snap Snapshot) ([]byte, error) {
	var outline []OutlineEntry
	if snap.TOC {
		outline = snap.Outline()
	}
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

func renderPDF(snap Snapshot, outline []OutlineEntry, tocPages map[int]int) ([]byte, map[int]int, error) {
	headingPages := map[int]int{}
	w, h := snap.PageWidth, snap.PageHeight
	var pdf gopdf.GoPdf
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pxToPt(w), H: pxToPt(h)}})
	if err := registerPDFFonts(&pdf); err != nil {
		return nil, nil, err
	}
	if snap.Title != "" {
		pdf.SetInfo(gopdf.PdfInfo{Title: snap.Title})
	}
	flow := &pdfFlowState{pageH: pxToPt(h)}
	pdfFlows.Store(&pdf, flow)
	defer pdfFlows.Delete(&pdf)
	pdf.SetMargins(pxToPt(snap.MarginLeft), pxToPt(snap.MarginTop), pxToPt(snap.MarginRight), pxToPt(snap.MarginBottom))
	pdfApplyTheme(&pdf, themeOf(snap), pxToPt(w), pxToPt(h))
	pdf.AddPage()

	contentWidth := pxToPt(w) - pxToPt(snap.MarginLeft) - pxToPt(snap.MarginRight)

	for i := 0; i < len(snap.Blocks); i++ {
		if len(outline) > 0 && snap.tocHere(i) {
			if err := drawPDFContents(&pdf, snap, outline, tocPages, contentWidth, true); err != nil {
				return nil, nil, err
			}
		}
		b := snap.Blocks[i]
		if b.Kind >= Heading1 && b.Kind <= Heading3 {
			pdf.SetNewY(pdf.GetY(), pdfHeading1Size*pdfLineHeightMult)
			headingPages[i] = pdf.GetNumberOfPages()
		}
		if b.Kind == TableCell {
			if b.TableRow == 0 && b.TableCol == 0 {
				n := b.TableRows * b.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					if err := renderPDFTable(&pdf, pdfRunLangs(snap), snap.Blocks[i:i+n], i, contentWidth, pxToPt(snap.MarginLeft)); err != nil {
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
		// recorded before drawing, so the widow rule never moves it
		flow.noWidow = b.Kind >= Heading1 && b.Kind <= Heading3
		err := renderPDFBlock(&pdf, snap, b, i, contentWidth)
		flow.noWidow = false
		if err != nil {
			return nil, nil, err
		}
	}

	if len(outline) > 0 && snap.tocEnd() {
		if err := drawPDFContents(&pdf, snap, outline, tocPages, contentWidth, false); err != nil {
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

func drawPDFContents(pdf *gopdf.GoPdf, snap Snapshot, outline []OutlineEntry, pages map[int]int, contentWidth float64, followed bool) error {
	if pdf.GetY() > pxToPt(snap.MarginTop)+1 {
		pdf.AddPage()
	}
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
		if err := setPDFFont(pdf, "", pdfBaseFontSize); err != nil {
			return err
		}
		dw, err := pdf.MeasureTextWidth(". ")
		if err != nil {
			return err
		}
		if n := int((right - nw - 6 - (x0 + indent + tw + 6)) / dw); n > 0 && snap.TOCStyle != "plain" {
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
	if followed {
		pdf.AddPage()
	}
	return nil
}

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

func drawPDFHeadersAndFooters(pdf *gopdf.GoPdf, snap Snapshot) error {
	if snap.Header == "" && snap.Footer == "" && snap.FirstHeader == "" && snap.FirstFooter == "" && snap.EvenHeader == "" && snap.EvenFooter == "" {
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
		head, foot := snap.Header, snap.Footer
		switch {
		case p == 1 && snap.FirstDiffers:
			head, foot = snap.FirstHeader, snap.FirstFooter
		case p%2 == 0 && snap.EvenDiffers:
			head, foot = snap.EvenHeader, snap.EvenFooter
		}
		for _, side := range []struct {
			text string
			y    float64
		}{
			{head, math.Max(4, (pxToPt(snap.MarginTop)-lh)/2)},
			{foot, math.Min(pageH-lh-4, pageH-pxToPt(snap.MarginBottom)+(pxToPt(snap.MarginBottom)-lh)/2)},
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

func registerPDFFonts(pdf *gopdf.GoPdf) error {
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

type pdfToken struct {
	text    string
	format  Format
	isBreak bool
}

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

func trimTrailingSpaceToken(line []pdfToken) []pdfToken {
	if n := len(line); n > 0 && strings.TrimSpace(line[n-1].text) == "" {
		return line[:n-1]
	}
	return line
}

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
		switch b.HeadingLevel() {
		case 4:
			tag.kind = tagH4
		case 5:
			tag.kind = tagH5
		case 6:
			tag.kind = tagH6
		}
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

	segs := b.Segments
	if b.Note > 0 {
		if v, ok := pdfFlows.Load(pdf); ok {
			v.(*pdfFlowState).anchor = "fn" + strconv.Itoa(b.Note)
		}
		segs = append(append([]Segment(nil), segs...), Segment{Text: " "}, Segment{Text: "\u2191", Format: Format{NoteBack: b.Note}})
	}
	tokens := pdfTokenize(segs)
	wrapped, err := wrapPDFTokens(pdf, tokens, contentWidth-indent, mono, forceBold, forceItalic, fontSize)
	if err != nil {
		return err
	}
	return drawPDFWrappedBlock(pdf, pdfRunLangs(snap), wrapped, pxToPt(snap.MarginLeft)+indent, fontSize, mono, forceBold, forceItalic, prefix, hang, tag)
}

func drawPDFWrappedBlock(pdf *gopdf.GoPdf, langs []string, tokenLines [][]pdfToken, x0, fontSize float64, mono, forceBold, forceItalic bool, prefix string, hang float64, tag pdfTag) error {
	lineHeight := fontSize * pdfLineHeightMult
	breakAt := pdfWidowBreak(pdf, len(tokenLines), lineHeight)
	for li, tokens := range tokenLines {
		if li == breakAt {
			pdf.SetNewY(pdf.GetY(), pdfPageHeight(pdf))
		}
		pdf.SetNewY(pdf.GetY(), lineHeight)
		y := pdf.GetY()
		if li == 0 {
			if v, ok := pdfFlows.Load(pdf); ok && v.(*pdfFlowState).anchor != "" {
				pdf.SetXY(x0, y)
				pdf.SetAnchor(v.(*pdfFlowState).anchor)
				v.(*pdfFlowState).anchor = ""
			}
		}

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
			if !mono && tok.format.NoteRef > 0 {
				pdf.SetAnchor("fnref" + strconv.Itoa(tok.format.NoteRef))
				pdf.AddInternalLink("fn"+strconv.Itoa(tok.format.NoteRef), x, y, w, lineHeight)
				run.leave()
			}
			if !mono && tok.format.NoteBack > 0 {
				pdf.AddInternalLink("fnref"+strconv.Itoa(tok.format.NoteBack), x, y, w, lineHeight)
				run.leave()
			}
			x += w
		}
		run.finish()
		pdf.SetY(y + lineHeight)
	}
	return nil
}

func renderPDFTable(pdf *gopdf.GoPdf, langs []string, cells []Block, base int, contentWidth, x0 float64) error {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	owners := tableMergeGrid(cells)
	colWidths := tableColumnWidths(cells, contentWidth, 0.75)
	colX := make([]float64, cols+1)
	for c, w := range colWidths {
		colX[c+1] = colX[c] + w
	}
	lineHeight := pdfBaseFontSize * pdfLineHeightMult

	type cellDraw struct {
		x, w    float64
		wrapped [][]pdfToken // nil means "no box at all" (skipCell)
	}

	allDraws := make([][]cellDraw, rows)
	heights := make([]float64, rows)
	for r := 0; r < rows; r++ {
		draws := make([]cellDraw, cols)
		rowHeight := max(lineHeight+2*pdfTableCellPadding, cells[r*cols].TableRowH*0.75)
		for c := 0; c < cols; c++ {
			i := r*cols + c
			cell := cells[i]
			own := owners[i]
			skipCell := cell.TableCovered && own.r0 == r
			if skipCell {
				continue
			}
			w := colWidths[c]
			if !cell.TableCovered && own.colSpan > 1 {
				w = colX[min(c+own.colSpan, cols)] - colX[c]
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
			draws[c] = cellDraw{x: x0 + colX[c], w: w, wrapped: wrapped}
		}

		allDraws[r], heights[r] = draws, rowHeight
	}

	for r := 0; r < rows; r++ {
		draws, rowHeight := allDraws[r], heights[r]
		// widow and orphan rule: header and last rows never stand alone
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
			ctag := pdfTag{kind: kind, id: base + r*cols + c, a: base, b: r<<16 | c}
			ty := y + pdfTableCellPadding
			for _, line := range d.wrapped {
				pdfMarkBegin(pdf, ctag)
				run := pdfRun{pdf: pdf, tag: ctag, langs: langs}
				tx := d.x + pdfTableCellPadding
				for _, tok := range line {
					run.enter(tok.format)
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
					if tok.format.Href != "" {
						pdf.SetLineWidth(pdfLineStrokeWidth)
						pdf.Line(tx, ty+pdfBaseFontSize*pdfUnderlineOffset, tx+tw, ty+pdfBaseFontSize*pdfUnderlineOffset)
						pdf.AddExternalLink(tok.format.Href, tx, ty, tw, lineHeight)
						run.leave()
					}
					tx += tw
				}
				run.finish()
				ty += lineHeight
			}
		}
		pdf.SetY(y + rowHeight)
	}
	return nil
}

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

type pdfRun struct {
	pdf   *gopdf.GoPdf
	tag   pdfTag
	langs []string
	mono  bool
	state int
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

func (r *pdfRun) enter(f Format) {
	link := !r.mono && (f.Href != "" || f.NoteRef > 0 || f.NoteBack > 0)
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

func (r *pdfRun) leave() {
	pdfMarkEnd(r.pdf)
	r.state = 3
}

func (r *pdfRun) finish() {
	if r.state != 3 {
		pdfMarkEnd(r.pdf)
		r.state = 3
	}
}

type pdfFlowState struct {
	pageH   float64
	noWidow bool
	anchor  string
}

var pdfFlows sync.Map

func pdfPageHeight(pdf *gopdf.GoPdf) float64 {
	if v, ok := pdfFlows.Load(pdf); ok {
		return v.(*pdfFlowState).pageH
	}
	return 0
}

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
		return -1
	}
	return brk
}

func pdfApplyTheme(pdf *gopdf.GoPdf, th docTheme, pageW, pageH float64) {
	if th.background != "" {
		r, g, b := rgb(th.background)
		pdf.AddHeader(func() {
			pdf.SetFillColor(r, g, b)
			pdf.RectFromUpperLeftWithStyle(0, 0, pageW, pageH, "F")
		})
	}
	if th.text != "" {
		r, g, b := rgb(th.text)
		pdf.SetTextColor(r, g, b)
	}
}
