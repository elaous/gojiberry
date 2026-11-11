package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DOCX renders a Snapshot to a Word document.
type DOCX struct{}

const (
	docxNS      = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"`
	docxEMU     = 9525
	docxMaxEMU  = 6 * 914400
	docxHeading = "Heading"
)

type docxBuilder struct {
	body       strings.Builder
	rels       []string // relationship elements
	media      map[string][]byte
	nextRel    int
	nextNum    int // numbering instances for numbered lists (each list restarts)
	nums       []int
	links      map[string]string
	linkColour string
	imgs       int
	docPr      int
	revs       int // tracked changes written so far (their ids)
	cur        int
	marks      map[int]string
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
	th := themeOf(snap)
	b := &docxBuilder{media: map[string][]byte{}, links: map[string]string{}, marks: map[int]string{}, linkColour: "0563C1"}
	if th.link != "" {
		b.linkColour = th.link
	}
	b.marks = tocMarks(snap, func(block int) string { return "_Toc" + strconv.Itoa(100000+block) })
	for i := 0; i < len(snap.Blocks); i++ {
		if snap.tocHere(i) {
			b.tableOfContents(snap)
		}
		blk := snap.Blocks[i]
		b.cur = i
		if blk.Kind == TableCell {
			if blk.TableRow == 0 && blk.TableCol == 0 {
				n := blk.TableRows * blk.TableCols
				if n > 0 && i+n <= len(snap.Blocks) {
					b.table(snap.Blocks[i : i+n])
					i += n - 1
					continue
				}
			}
			b.block(Block{Kind: Paragraph, Segments: blk.Segments})
			continue
		}
		b.block(blk)
	}
	if snap.tocEnd() {
		b.tableOfContents(snap)
	}
	if b.body.Len() == 0 {
		b.body.WriteString(`<w:p/>`)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) error {
		w, err := zipEntry(zw, name, zip.Deflate)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}
	const header = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	var hfRels [6]string
	var hfTypes string
	var hfParts []struct{ name, content string }
	hfList := []struct{ kind, text, num string }{{"header", snap.Header, "1"}, {"footer", snap.Footer, "1"},
		{"header", "", "2"}, {"footer", "", "2"}, {"header", "", "3"}, {"footer", "", "3"}}
	if snap.FirstDiffers {
		hfList[2].text, hfList[3].text = snap.FirstHeader, snap.FirstFooter
	}
	if snap.EvenDiffers {
		hfList[4].text, hfList[5].text = snap.EvenHeader, snap.EvenFooter
	}
	for i, hf := range hfList {
		if hf.text == "" {
			continue
		}
		part := hf.kind + hf.num + ".xml"
		hfRels[i] = b.rel(hf.kind, part, false)
		hfTypes += `<Override PartName="/word/` + part + `" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.` + hf.kind + `+xml"/>`
		hfParts = append(hfParts, struct{ name, content string }{"word/" + part, header + docxHeaderFooter(snap, hf.kind, hf.text)})
	}
	files := []struct{ name, content string }{
		{"[Content_Types].xml", header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/><Default Extension="jpeg" ContentType="image/jpeg"/><Default Extension="gif" ContentType="image/gif"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>
<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` + hfTypes + `
</Types>`},
		{"_rels/.rels", header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
</Relationships>`},
		{"docProps/core.xml", header + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc(snap.Title) + `</dc:title>` + docxCoreLanguage(snap.Lang) + `</cp:coreProperties>`},
		{"word/document.xml", header + `<w:document ` + docxNS + `>` + docxBackground(th) + `<w:body>` + b.body.String() + docxSection(snap, hfRels) + `</w:body></w:document>`},
		{"word/styles.xml", header + docxStylesFor(snap.Lang, th)},
		{"word/numbering.xml", header + b.numbering()},
		{"word/_rels/document.xml.rels", header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` + strings.Join(b.rels, "") + `</Relationships>`},
	}
	if th.background != "" || snap.EvenDiffers {
		var settings string
		if th.background != "" {
			settings += `<w:displayBackgroundShape/>`
		}
		if snap.EvenDiffers {
			settings += `<w:evenAndOddHeaders/>`
		}
		files = append(files, struct{ name, content string }{"word/settings.xml", header + `<w:settings ` + docxNS + `>` + settings + `</w:settings>`})
		files[0].content = strings.Replace(files[0].content, `</Types>`, `<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/></Types>`, 1)
		for i := range files {
			if files[i].name == "word/_rels/document.xml.rels" {
				files[i].content = strings.Replace(files[i].content, `</Relationships>`, `<Relationship Id="rIdSettings" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/></Relationships>`, 1)
			}
		}
	}
	files = append(files, hfParts...)
	for _, f := range files {
		if err := add(f.name, f.content); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(b.media))
	for name := range b.media {
		names = append(names, name)
	}
	sort.Strings(names) // the same document gives the same bytes
	for _, name := range names {
		data := b.media[name]
		w, err := zipEntry(zw, "word/media/"+name, zip.Deflate)
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

func docxSection(snap Snapshot, hf [6]string) string {
	px := func(v float64) int { return int(v * 15) } // twips
	refs := ""
	if hf[0] != "" {
		refs += `<w:headerReference w:type="default" r:id="` + hf[0] + `"/>`
	}
	if hf[1] != "" {
		refs += `<w:footerReference w:type="default" r:id="` + hf[1] + `"/>`
	}
	if snap.FirstDiffers {
		if hf[2] != "" {
			refs += `<w:headerReference w:type="first" r:id="` + hf[2] + `"/>`
		}
		if hf[3] != "" {
			refs += `<w:footerReference w:type="first" r:id="` + hf[3] + `"/>`
		}
	}
	if snap.EvenDiffers {
		if hf[4] != "" {
			refs += `<w:headerReference w:type="even" r:id="` + hf[4] + `"/>`
		}
		if hf[5] != "" {
			refs += `<w:footerReference w:type="even" r:id="` + hf[5] + `"/>`
		}
	}
	title := ""
	if snap.FirstDiffers {
		title = `<w:titlePg/>`
	}
	if snap.Vertical {
		title += `<w:textDirection w:val="tbRl"/>`
	}
	return fmt.Sprintf(`<w:sectPr>%s<w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="720" w:footer="720" w:gutter="0"/>%s</w:sectPr>`,
		refs, px(snap.PageWidth), px(snap.PageHeight), px(snap.MarginTop), px(snap.MarginRight), px(snap.MarginBottom), px(snap.MarginLeft), title)
}

func docxField(instr, cached string) string {
	return `<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText xml:space="preserve"> ` + instr + ` </w:instrText></w:r>` +
		`<w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>` + cached + `</w:t></w:r><w:r><w:fldChar w:fldCharType="end"/></w:r>`
}

func docxDecorRuns(text, title string) string {
	var sb strings.Builder
	lit := func(s string) {
		if s != "" {
			sb.WriteString(`<w:r><w:t xml:space="preserve">` + esc(s) + `</w:t></w:r>`)
		}
	}
	for rest := text; rest != ""; {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			lit(rest)
			break
		}
		lit(rest[:i])
		rest = rest[i:]
		switch {
		case strings.HasPrefix(rest, fieldPage):
			sb.WriteString(docxField("PAGE", "1"))
			rest = rest[len(fieldPage):]
		case strings.HasPrefix(rest, fieldPages):
			sb.WriteString(docxField("NUMPAGES", "1"))
			rest = rest[len(fieldPages):]
		case strings.HasPrefix(rest, fieldTitle):
			lit(title)
			rest = rest[len(fieldTitle):]
		default:
			lit("{")
			rest = rest[1:]
		}
	}
	return sb.String()
}

func docxHeaderFooter(snap Snapshot, kind, text string) string {
	width := int((snap.PageWidth - snap.MarginLeft - snap.MarginRight) * 15)
	l, c, r := decorParts(text)
	runs := docxDecorRuns(l, snap.Title) + `<w:r><w:tab/></w:r>` + docxDecorRuns(c, snap.Title) + `<w:r><w:tab/></w:r>` + docxDecorRuns(r, snap.Title)
	root := "hdr"
	if kind == "footer" {
		root = "ftr"
	}
	return `<w:` + root + ` ` + docxNS + `><w:p><w:pPr><w:tabs><w:tab w:val="clear" w:pos="4513"/><w:tab w:val="clear" w:pos="9026"/>` +
		fmt.Sprintf(`<w:tab w:val="center" w:pos="%d"/><w:tab w:val="right" w:pos="%d"/>`, width/2, width) +
		`</w:tabs><w:spacing w:after="0"/></w:pPr>` + runs + `</w:p></w:` + root + `>`
}

func (b *docxBuilder) tableOfContents(snap Snapshot) {
	if !snap.TOC {
		return
	}
	entries := snap.Outline()
	if len(entries) == 0 {
		return
	}
	width := int((snap.PageWidth - snap.MarginLeft - snap.MarginRight) * 15)
	leader, depth := "dot", 3
	if snap.TOCStyle == "plain" {
		leader = "none"
	}
	if snap.TOCDepth > 0 {
		depth = min(snap.TOCDepth, 3)
	}
	w := &b.body
	w.WriteString(`<w:p><w:pPr><w:keepNext/><w:spacing w:before="0" w:after="240"/></w:pPr><w:r><w:rPr><w:b/><w:sz w:val="40"/></w:rPr><w:t>` + esc(snap.tocTitle()) + `</w:t></w:r></w:p>`)
	for i, e := range entries {
		name := b.marks[e.Block]
		w.WriteString(`<w:p><w:pPr><w:tabs><w:tab w:val="right" w:leader="` + leader + `" w:pos="` + strconv.Itoa(width) + `"/></w:tabs><w:spacing w:after="60"/><w:ind w:left="` + strconv.Itoa((e.Level-1)*360) + `"/></w:pPr>`)
		if i == 0 {
			w.WriteString(`<w:r><w:fldChar w:fldCharType="begin" w:dirty="true"/></w:r><w:r><w:instrText xml:space="preserve"> TOC \o "1-` + strconv.Itoa(depth) + `" \h \z \u </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r>`)
		}
		w.WriteString(`<w:hyperlink w:anchor="` + name + `" w:history="1"><w:r><w:t xml:space="preserve">` + esc(e.Text) + `</w:t></w:r><w:r><w:tab/></w:r>` +
			docxField(`PAGEREF `+name+` \h`, "–") + `</w:hyperlink>`)
		if i == len(entries)-1 {
			w.WriteString(`<w:r><w:fldChar w:fldCharType="end"/></w:r>`)
		}
		w.WriteString(`</w:p>`)
	}
	w.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
}

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
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="60"/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:b/><w:sz w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="60"/><w:outlineLvl w:val="4"/></w:pPr><w:rPr><w:b/><w:sz w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="60"/><w:outlineLvl w:val="5"/></w:pPr><w:rPr><w:b/><w:i/><w:sz w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:pPr><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="9CA3AF"/></w:pBdr><w:ind w:left="567"/></w:pPr><w:rPr><w:i/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Source Code"/><w:basedOn w:val="Normal"/><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F3F4F6"/><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="40"/><w:contextualSpacing/></w:pPr></w:style>
<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/></w:tblBorders></w:tblPr></w:style>
</w:styles>`

const (
	noteBookmarkBase = 1 << 20
	noteRefBookmarks = 1 << 19
)

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
	case Paragraph:
		if blk.Equation != "" {
			w.WriteString(`<w:p><m:oMathPara>` + OMML(blk.Equation) + `</m:oMathPara>` +
				`<w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">` + esc(plainTextInline(blk.Segments)) + `</w:t></w:r></w:p>`)
			return
		}
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
		ppr = `<w:pStyle w:val="` + docxHeading + strconv.Itoa(blk.HeadingLevel()) + `"/>`
	case Quote:
		ppr = `<w:pStyle w:val="Quote"/>`
	case BulletItem:
		ppr = `<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="` + strconv.Itoa(min(blk.Level, 8)) + `"/><w:numId w:val="1"/></w:numPr>`
	case NumberedItem:
		if (blk.Level == 0 && blk.NumberedIndex <= 1) || len(b.nums) == 0 { // A new list restarts at 1.
			b.nextNum++
			b.nums = append(b.nums, b.nextNum+1) // numId 1 is the bullets
		}
		ppr = `<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="` + strconv.Itoa(min(blk.Level, 8)) + `"/><w:numId w:val="` + strconv.Itoa(b.nums[len(b.nums)-1]) + `"/></w:numPr>`
	}
	w.WriteString(`<w:p>`)
	if ppr != "" {
		w.WriteString(`<w:pPr>` + ppr + `</w:pPr>`)
	}
	inner := b.inline(blk.Segments)
	if blk.Note > 0 {
		n := strconv.Itoa(blk.Note)
		inner = `<w:bookmarkStart w:id="` + strconv.Itoa(noteBookmarkBase+blk.Note) + `" w:name="_fn` + n + `"/>` + inner + `<w:bookmarkEnd w:id="` + strconv.Itoa(noteBookmarkBase+blk.Note) + `"/>` +
			`<w:r><w:t xml:space="preserve"> </w:t></w:r><w:hyperlink w:anchor="_fnref` + n + `" w:history="1"><w:r><w:rPr><w:color w:val="` + b.linkColour + `"/><w:u w:val="single"/></w:rPr><w:t>` + "\u21a9" + `</w:t></w:r></w:hyperlink>`
	}
	if name, ok := b.marks[b.cur]; ok {
		inner = `<w:bookmarkStart w:id="` + strconv.Itoa(b.cur) + `" w:name="` + name + `"/>` + inner + `<w:bookmarkEnd w:id="` + strconv.Itoa(b.cur) + `"/>`
	}
	w.WriteString(inner)
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
		if f.Formula != "" && f.Change == NoChange && f.Href == "" && f.NoteRef == 0 {
			sb.WriteString(OMML(f.Formula) + `<w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">` + esc(s.Text) + `</w:t></w:r>`)
			continue
		}
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
			rpr.WriteString(`<w:color w:val="` + b.linkColour + `"/>`)
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
		if f.Lang != "" { // after vertAlign in the schema's order
			rpr.WriteString(`<w:lang w:val="` + esc(f.Lang) + `"/>`)
		}
		run := `<w:r>`
		if rpr.Len() > 0 {
			run += `<w:rPr>` + rpr.String() + `</w:rPr>`
		}
		if f.Change == Deleted {
			run += `<w:delText xml:space="preserve">` + esc(s.Text) + `</w:delText></w:r>`
		} else {
			run += `<w:t xml:space="preserve">` + esc(s.Text) + `</w:t></w:r>`
		}
		if f.Change != NoChange {
			tag := "w:ins"
			if f.Change == Deleted {
				tag = "w:del"
			}
			author := f.Author
			if author == "" {
				author = "Unknown"
			}
			b.revs++
			run = `<` + tag + ` w:id="` + strconv.Itoa(b.revs) + `" w:author="` + esc(author) + `" w:date="` + time.Unix(f.Time, 0).UTC().Format("2006-01-02T15:04:05Z") + `">` + run + `</` + tag + `>`
		}
		if f.NoteRef > 0 {
			n := strconv.Itoa(f.NoteRef)
			run = `<w:bookmarkStart w:id="` + strconv.Itoa(noteBookmarkBase+noteRefBookmarks+f.NoteRef) + `" w:name="_fnref` + n + `"/>` +
				`<w:hyperlink w:anchor="_fn` + n + `" w:history="1">` + run + `</w:hyperlink><w:bookmarkEnd w:id="` + strconv.Itoa(noteBookmarkBase+noteRefBookmarks+f.NoteRef) + `"/>`
		}
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
		if px := cells[c].TableColW; px > 0 {
			w.WriteString(`<w:gridCol w:w="` + strconv.Itoa(int(px*15)) + `"/>`) // a CSS pixel is 15 twips
		} else {
			w.WriteString(`<w:gridCol w:w="2000"/>`)
		}
	}
	w.WriteString(`</w:tblGrid>`)
	for r := 0; r < rows; r++ {
		height := ""
		if px := cells[r*cols].TableRowH; px > 0 {
			height = `<w:trHeight w:val="` + strconv.Itoa(int(px*15)) + `" w:hRule="atLeast"/>`
		}
		w.WriteString(`<w:tr><w:trPr><w:cantSplit/>` + height + `</w:trPr>`)
		keep := ""
		if keepRowWithNext(r, rows) {
			keep = `<w:keepNext/>`
		}
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			if cell.TableCovered {
				owner := ownerOf(cells, rows, cols, r, c)
				if owner.row != r && owner.col == c {
					span := cells[owner.row*cols+owner.col].TableColSpan
					w.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/>` + gridSpan(span) + `<w:vMerge/></w:tcPr><w:p/></w:tc>`)
				}
				continue
			}
			props := `<w:tcW w:w="0" w:type="auto"/>` + gridSpan(cell.TableColSpan)
			if px := cell.TableColW; px > 0 && cell.TableColSpan <= 1 {
				props = `<w:tcW w:w="` + strconv.Itoa(int(px*15)) + `" w:type="dxa"/>` + gridSpan(cell.TableColSpan)
			}
			if cell.TableRowSpan > 1 {
				props += `<w:vMerge w:val="restart"/>`
			}
			if fill, ok := hexFill(cell.TableBackground); ok {
				props += `<w:shd w:val="clear" w:color="auto" w:fill="` + fill + `"/>`
			}
			para := `<w:p>`
			if keep != "" {
				para = `<w:p><w:pPr>` + keep + `</w:pPr>`
			}
			if jc := map[int]string{1: "center", 2: "right"}[cell.TableAlign]; jc != "" {
				para = `<w:p><w:pPr>` + keep + `<w:jc w:val="` + jc + `"/></w:pPr>`
			}
			w.WriteString(`<w:tc><w:tcPr>` + props + `</w:tcPr>` + para + b.inline(cell.Segments) + `</w:p></w:tc>`)
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
	descr := ""
	if blk.ImageAlt != "" {
		descr = ` descr="` + esc(blk.ImageAlt) + `"`
	}
	docTitle := ""
	if c := imageCredit(blk); c != "" {
		docTitle = ` title="` + esc(c) + `"`
	}
	return fmt.Sprintf(`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Picture %d"%s%s/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="%s"%s/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		cx, cy, b.docPr, b.docPr, descr, docTitle, b.docPr, name, descr, id, cx, cy)
}

func hexFill(c string) (string, bool) {
	c = strings.TrimPrefix(c, "#")
	if len(c) != 6 {
		return "", false
	}
	for _, r := range c {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return "", false
		}
	}
	return strings.ToLower(c), true
}

func docxCoreLanguage(lang string) string {
	if lang == "" {
		return ""
	}
	return "<dc:language>" + esc(lang) + "</dc:language>"
}

func docxStylesFor(lang string, th docTheme) string {
	out := docxStyles
	if th.text != "" || th.bodyPt > 0 {
		def := ""
		if th.text != "" {
			def += `<w:color w:val="` + th.text + `"/>`
		}
		size := `<w:sz w:val="22"/>`
		if th.bodyPt > 0 {
			size = `<w:sz w:val="` + strconv.Itoa(int(th.bodyPt*2+0.5)) + `"/>`
		}
		out = strings.Replace(out, `<w:sz w:val="22"/></w:rPr></w:rPrDefault>`, def+size+`</w:rPr></w:rPrDefault>`, 1)
	}
	if lang != "" {
		out = strings.Replace(out, `</w:rPr></w:rPrDefault>`, `<w:lang w:val="`+esc(lang)+`"/></w:rPr></w:rPrDefault>`, 1)
	}
	return out
}

func docxBackground(th docTheme) string {
	if th.background == "" {
		return ""
	}
	return `<w:background w:color="` + th.background + `"/>`
}
