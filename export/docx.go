package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"strconv"
	"strings"
)

// DOCX renders a Snapshot to a Word document. It writes the OOXML package
// directly (no third-party library), so that what the other formats keep is
// kept here too: real hyperlinks, embedded pictures (data: URLs), super and
// subscript, bullet and numbered lists that nest and restart, headings, quotes,
// code and tables.
type DOCX struct{}

const (
	docxNS      = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"`
	docxEMU     = 9525
	docxMaxEMU  = 6 * 914400 // six inches: a picture is scaled to fit the text width
	docxHeading = "Heading"
)

// docxBuilder collects the parts of the package while the body is written.
type docxBuilder struct {
	body    strings.Builder
	rels    []string // relationship elements
	media   map[string][]byte
	nextRel int
	nextNum int // numbering instances for numbered lists (each list restarts)
	nums    []int
	links   map[string]string
	imgs    int
	docPr   int
}

func (b *docxBuilder) rel(typ, target string, external bool) string {
	b.nextRel++
	id := "rId" + strconv.Itoa(b.nextRel+10)
	mode := ""
	if external {
		mode = ` TargetMode="External"`
	}
	b.rels = append(b.rels, fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/%s" Target="%s"%s/>`, id, typ, esc(target), mode))
	return id
}

func esc(s string) string {
	var sb strings.Builder
	_ = xml.EscapeText(&sb, []byte(s))
	return sb.String()
}

func (DOCX) Export(snap Snapshot) ([]byte, error) {
	b := &docxBuilder{media: map[string][]byte{}, links: map[string]string{}}
	for i := 0; i < len(snap.Blocks); i++ {
		blk := snap.Blocks[i]
		if blk.Kind == TableCell {
			if blk.TableRow == 0 && blk.TableCol == 0 {
				n := blk.TableRows * blk.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					b.table(snap.Blocks[i : i+n])
					i += n - 1
				}
			}
			continue
		}
		b.block(blk)
	}
	if b.body.Len() == 0 {
		b.body.WriteString(`<w:p/>`)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}
	const header = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	files := []struct{ name, content string }{
		{"[Content_Types].xml", header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/><Default Extension="jpeg" ContentType="image/jpeg"/><Default Extension="gif" ContentType="image/gif"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>
<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`},
		{"_rels/.rels", header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
</Relationships>`},
		{"docProps/core.xml", header + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc(snap.Title) + `</dc:title></cp:coreProperties>`},
		{"word/document.xml", header + `<w:document ` + docxNS + `><w:body>` + b.body.String() + docxSection(snap) + `</w:body></w:document>`},
		{"word/styles.xml", header + docxStyles},
		{"word/numbering.xml", header + b.numbering()},
		{"word/_rels/document.xml.rels", header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` + strings.Join(b.rels, "") + `</Relationships>`},
	}
	for _, f := range files {
		if err := add(f.name, f.content); err != nil {
			return nil, err
		}
	}
	for name, data := range b.media {
		w, err := zw.Create("word/media/" + name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func docxSection(snap Snapshot) string {
	px := func(v float64) int { return int(v * 15) } // twips
	return fmt.Sprintf(`<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`,
		px(snap.PageWidth), px(snap.PageHeight), px(snap.MarginTop), px(snap.MarginRight), px(snap.MarginBottom), px(snap.MarginLeft))
}

// numbering is numbering.xml: abstract 0 is bullets, abstract 1 numbers
// (decimal, letters, roman, by level); num 1 uses the bullets and each
// numbered list gets its own num, restarting at 1.
func (b *docxBuilder) numbering() string {
	var sb strings.Builder
	sb.WriteString(`<w:numbering ` + docxNS + `>`)
	bullets := []string{"•", "◦", "▪"}
	sb.WriteString(`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for l := 0; l < 9; l++ {
		fmt.Fprintf(&sb, `<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="%s"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`, l, bullets[l%3], 720*(l+1))
	}
	sb.WriteString(`</w:abstractNum><w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="hybridMultilevel"/>`)
	fmts := []string{"decimal", "lowerLetter", "lowerRoman"}
	for l := 0; l < 9; l++ {
		fmt.Fprintf(&sb, `<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/><w:lvlText w:val="%%%d."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`, l, fmts[l%3], l+1, 720*(l+1))
	}
	sb.WriteString(`</w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for _, n := range b.nums {
		fmt.Fprintf(&sb, `<w:num w:numId="%d"><w:abstractNumId w:val="1"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`, n)
	}
	sb.WriteString(`</w:numbering>`)
	return sb.String()
}

const docxStyles = `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="360" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="40"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="280" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:pPr><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="9CA3AF"/></w:pBdr><w:ind w:left="567"/></w:pPr><w:rPr><w:i/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Source Code"/><w:basedOn w:val="Normal"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F3F4F6"/><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="40"/><w:contextualSpacing/></w:pPr></w:style>
<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/></w:tblBorders></w:tblPr></w:style>
</w:styles>`

func (b *docxBuilder) block(blk Block) {
	w := &b.body
	switch blk.Kind {
	case Image:
		if p := b.picture(blk); p != "" {
			w.WriteString(`<w:p>` + p + `</w:p>`)
		} else {
			w.WriteString(`<w:p><w:r><w:rPr><w:i/></w:rPr><w:t>[Image]</w:t></w:r></w:p>`)
		}
		return
	case Code:
		var sb strings.Builder
		for i, line := range strings.Split(rawCodeText(blk.Segments), "\n") {
			if i > 0 {
				sb.WriteString(`<w:r><w:br/></w:r>`)
			}
			sb.WriteString(`<w:r><w:t xml:space="preserve">` + esc(line) + `</w:t></w:r>`)
		}
		w.WriteString(`<w:p><w:pPr><w:pStyle w:val="Code"/></w:pPr>` + sb.String() + `</w:p>`)
		return
	}
	ppr := ""
	switch blk.Kind {
	case Heading1, Heading2, Heading3:
		ppr = `<w:pStyle w:val="` + docxHeading + strconv.Itoa(int(blk.Kind-Heading1)+1) + `"/>`
	case Quote:
		ppr = `<w:pStyle w:val="Quote"/>`
	case BulletItem:
		ppr = `<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="` + strconv.Itoa(min(blk.Level, 8)) + `"/><w:numId w:val="1"/></w:numPr>`
	case NumberedItem:
		if (blk.Level == 0 && blk.NumberedIndex <= 1) || len(b.nums) == 0 { // a new list restarts at 1; a nested item stays in its list
			b.nextNum++
			b.nums = append(b.nums, b.nextNum+1) // numId 1 is the bullets
		}
		ppr = `<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="` + strconv.Itoa(min(blk.Level, 8)) + `"/><w:numId w:val="` + strconv.Itoa(b.nums[len(b.nums)-1]) + `"/></w:numPr>`
	}
	w.WriteString(`<w:p>`)
	if ppr != "" {
		w.WriteString(`<w:pPr>` + ppr + `</w:pPr>`)
	}
	w.WriteString(b.inline(blk.Segments))
	w.WriteString(`</w:p>`)
}

func (b *docxBuilder) inline(segs []Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		if s.SoftBreak {
			sb.WriteString(`<w:r><w:br/></w:r>`)
			continue
		}
		f := s.Format
		// Children in the order the schema lists them (Word is strict about it).
		var rpr strings.Builder
		if f.Bold {
			rpr.WriteString(`<w:b/>`)
		}
		if f.Italic {
			rpr.WriteString(`<w:i/>`)
		}
		if f.Strikethrough {
			rpr.WriteString(`<w:strike/>`)
		}
		if f.Href != "" {
			rpr.WriteString(`<w:color w:val="0563C1"/>`)
		}
		if f.Href != "" || f.Underline {
			rpr.WriteString(`<w:u w:val="single"/>`)
		}
		switch {
		case f.Superscript:
			rpr.WriteString(`<w:vertAlign w:val="superscript"/>`)
		case f.Subscript:
			rpr.WriteString(`<w:vertAlign w:val="subscript"/>`)
		}
		run := `<w:r>`
		if rpr.Len() > 0 {
			run += `<w:rPr>` + rpr.String() + `</w:rPr>`
		}
		run += `<w:t xml:space="preserve">` + esc(s.Text) + `</w:t></w:r>`
		if f.Href != "" {
			id, ok := b.links[f.Href]
			if !ok {
				id = b.rel("hyperlink", f.Href, true)
				b.links[f.Href] = id
			}
			run = `<w:hyperlink r:id="` + id + `">` + run + `</w:hyperlink>`
		}
		sb.WriteString(run)
	}
	return sb.String()
}

func (b *docxBuilder) table(cells []Block) {
	rows, cols := cells[0].TableRows, cells[0].TableCols
	w := &b.body
	w.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/></w:tblPr><w:tblGrid>`)
	for c := 0; c < cols; c++ {
		w.WriteString(`<w:gridCol w:w="2000"/>`)
	}
	w.WriteString(`</w:tblGrid>`)
	// A cell a merge covers from above continues its vertical merge; one it
	// covers from the left is not written at all (the owner's gridSpan holds it).
	for r := 0; r < rows; r++ {
		w.WriteString(`<w:tr>`)
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			if cell.TableCovered {
				// Beside its owner: the owner's gridSpan holds it, so nothing is written.
				// Below its owner, in the owner's column: continue the vertical merge,
				// as wide as the owner; elsewhere under the span: nothing again.
				owner := ownerOf(cells, rows, cols, r, c)
				if owner.row != r && owner.col == c {
					span := cells[owner.row*cols+owner.col].TableColSpan
					w.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/>` + gridSpan(span) + `<w:vMerge/></w:tcPr><w:p/></w:tc>`)
				}
				continue
			}
			props := `<w:tcW w:w="0" w:type="auto"/>` + gridSpan(cell.TableColSpan)
			if cell.TableRowSpan > 1 {
				props += `<w:vMerge w:val="restart"/>`
			}
			w.WriteString(`<w:tc><w:tcPr>` + props + `</w:tcPr><w:p>` + b.inline(cell.Segments) + `</w:p></w:tc>`)
		}
		w.WriteString(`</w:tr>`)
	}
	w.WriteString(`</w:tbl><w:p/>`)
}

func gridSpan(n int) string {
	if n > 1 {
		return `<w:gridSpan w:val="` + strconv.Itoa(n) + `"/>`
	}
	return ""
}

type cellPos struct{ row, col int }

// ownerOf finds the merged cell that covers (r, c): the nearest owner above
// and to the left whose span reaches it.
func ownerOf(cells []Block, rows, cols, r, c int) cellPos {
	for or := r; or >= 0; or-- {
		for oc := c; oc >= 0; oc-- {
			o := cells[or*cols+oc]
			if !o.TableCovered && (o.TableRowSpan > 1 || o.TableColSpan > 1) && or+o.TableRowSpan > r && oc+o.TableColSpan > c {
				return cellPos{or, oc}
			}
		}
	}
	return cellPos{r, c}
}

// picture embeds a data: URL image; "" when it cannot be (not a data URL, or
// not a PNG, JPEG or GIF).
func (b *docxBuilder) picture(blk Block) string {
	head, b64, ok := strings.Cut(blk.ImageSrc, ",")
	if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
		return ""
	}
	ext := map[string]string{"data:image/png;base64": "png", "data:image/jpeg;base64": "jpeg", "data:image/jpg;base64": "jpeg", "data:image/gif;base64": "gif"}[head]
	if ext == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	wpx, hpx := blk.ImageW, blk.ImageH
	if wpx <= 0 || hpx <= 0 {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return ""
		}
		wpx, hpx = float64(cfg.Width), float64(cfg.Height)
	}
	cx, cy := int(wpx*docxEMU), int(hpx*docxEMU)
	if cx > docxMaxEMU {
		cy = cy * docxMaxEMU / cx
		cx = docxMaxEMU
	}
	b.imgs++
	name := "image" + strconv.Itoa(b.imgs) + "." + ext
	b.media[name] = raw
	id := b.rel("image", "media/"+name, false)
	b.docPr++
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Picture %d"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="%s"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		cx, cy, b.docPr, b.docPr, b.docPr, name, id, cx, cy)
}
