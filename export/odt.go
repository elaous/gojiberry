package export

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"sort"
	"strconv"
	"strings"
)

// ODT renders a Snapshot to an OpenDocument text file (ODF 1.2), the format of LibreOffice, Collabora and
// many public offices. Like DOCX it is written directly: real hyperlinks, embedded pictures with their
// alternative text, super and subscript, nested bullet and numbered lists, headings, quotes, code, tables
// with spans and shading, real footnotes, the language of the document and of a run, and the running
// header and footer of the print layout with page-number fields.
type ODT struct{}

const odfNS = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" xmlns:number="urn:oasis:names:tc:opendocument:xmlns:datastyle:1.0"`

type odtBuilder struct {
	body   strings.Builder
	styles map[string]string // automatic text style key -> name
	order  []string          // the keys in the order they were made
	cells  map[string]string // table cell and paragraph styles by key
	corder []string
	media  map[string][]byte
	imgs   int
	notes  map[int]string // footnote text by number
	// open lists, outermost first: true is a numbered list
	open []bool
	// cur is the index of the block being written; marks names the bookmark of each heading the table of
	// contents links to.
	cur   int
	marks map[int]string
}

func (b *odtBuilder) textStyle(f Format) string {
	var props []string
	if f.Bold {
		props = append(props, `fo:font-weight="bold"`)
	}
	if f.Italic {
		props = append(props, `fo:font-style="italic"`)
	}
	if f.Underline {
		props = append(props, `style:text-underline-style="solid" style:text-underline-width="auto" style:text-underline-color="font-color"`)
	}
	if f.Strikethrough {
		props = append(props, `style:text-line-through-style="solid"`)
	}
	switch {
	case f.Superscript:
		props = append(props, `style:text-position="super 58%"`)
	case f.Subscript:
		props = append(props, `style:text-position="sub 58%"`)
	}
	if f.Lang != "" {
		l, c, _ := strings.Cut(f.Lang, "-")
		props = append(props, `fo:language="`+esc(l)+`"`)
		if c != "" {
			props = append(props, `fo:country="`+esc(strings.ToUpper(c))+`"`)
		}
	}
	if len(props) == 0 {
		return ""
	}
	key := strings.Join(props, " ")
	if n, ok := b.styles[key]; ok {
		return n
	}
	n := "T" + strconv.Itoa(len(b.styles)+1)
	b.styles[key] = n
	b.order = append(b.order, key)
	return n
}

// odtText writes text with the spaces, tabs and line ends ODF wants as elements.
func odtText(s string) string {
	var sb strings.Builder
	spaces := 0
	flush := func() {
		switch spaces {
		case 0:
		case 1:
			sb.WriteByte(' ')
		default:
			sb.WriteString(` <text:s text:c="` + strconv.Itoa(spaces-1) + `"/>`)
		}
		spaces = 0
	}
	for _, r := range s {
		switch r {
		case ' ':
			spaces++
			continue
		case '\t':
			flush()
			sb.WriteString(`<text:tab/>`)
		case '\n':
			flush()
			sb.WriteString(`<text:line-break/>`)
		default:
			flush()
			sb.WriteString(esc(string(r)))
		}
	}
	flush()
	return sb.String()
}

func (b *odtBuilder) inline(segs []Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		if s.SoftBreak {
			sb.WriteString(`<text:line-break/>`)
			continue
		}
		f := s.Format
		run := odtText(s.Text)
		if name := b.textStyle(f); name != "" {
			run = `<text:span text:style-name="` + name + `">` + run + `</text:span>`
		}
		if f.Href != "" {
			run = `<text:a xlink:type="simple" xlink:href="` + esc(f.Href) + `" text:style-name="Internet_20_link" text:visited-style-name="Internet_20_link">` + run + `</text:a>`
		}
		if f.NoteRef > 0 {
			if text, ok := b.notes[f.NoteRef]; ok {
				n := strconv.Itoa(f.NoteRef)
				run = `<text:note text:id="ftn` + n + `" text:note-class="footnote"><text:note-citation>` + n + `</text:note-citation><text:note-body><text:p text:style-name="Footnote">` + esc(text) + `</text:p></text:note-body></text:note>`
			}
		}
		sb.WriteString(run)
	}
	return sb.String()
}

func (b *odtBuilder) closeLists(to int) {
	for len(b.open) > to {
		b.body.WriteString(`</text:list-item></text:list>`)
		b.open = b.open[:len(b.open)-1]
	}
}

func listStyle(numbered bool) string {
	if numbered {
		return "LN"
	}
	return "LB"
}

// item opens the list item for a block at its level, closing and opening lists as needed.
func (b *odtBuilder) item(level int, numbered bool) {
	level = min(level, 8)
	b.closeLists(level + 1)
	if len(b.open) == level+1 {
		b.body.WriteString(`</text:list-item>`)
		if b.open[level] != numbered {
			b.body.WriteString(`</text:list>`)
			b.open = b.open[:level]
		}
	}
	for len(b.open) < level+1 {
		if len(b.open) == 0 {
			b.body.WriteString(`<text:list text:style-name="` + listStyle(numbered) + `">`)
		} else {
			b.body.WriteString(`<text:list>`)
		}
		b.open = append(b.open, numbered)
		if len(b.open) < level+1 {
			b.body.WriteString(`<text:list-item>`) // a level skipped: an item to hold the deeper list
		}
	}
	b.body.WriteString(`<text:list-item>`)
}

func (b *odtBuilder) cellStyle(fill string, align int, keep bool) (cell, para string) {
	if f, ok := hexFill(fill); ok {
		key := "c|" + f
		if _, ok := b.cells[key]; !ok {
			b.cells[key] = "C" + strconv.Itoa(len(b.cells)+1)
			b.corder = append(b.corder, key)
		}
		cell = b.cells[key]
	}
	if align == 1 || align == 2 || keep {
		if align != 1 && align != 2 {
			align = 0
		}
		key := "p|" + strconv.Itoa(align)
		if keep {
			key += "|keep"
		}
		if _, ok := b.cells[key]; !ok {
			b.cells[key] = "P" + strconv.Itoa(len(b.cells)+1)
			b.corder = append(b.corder, key)
		}
		para = b.cells[key]
	}
	return
}

// contents writes the table of contents that starts the document (when the snapshot asks for one and has
// headings) as a real ODF index: an entry per heading linked to a bookmark on it, with the tab and page number
// the entries have in LibreOffice. The page numbers are placeholders until the person updates the index
// (Tools > Update > All Indexes), which makes LibreOffice work them out; only it knows its pages.
func (b *odtBuilder) contents(snap Snapshot) {
	if !snap.TOC {
		return
	}
	entries := snap.Outline()
	if len(entries) == 0 {
		return
	}
	b.closeLists(0) // contents in the middle of a list end it
	title := esc(snap.tocTitle())
	var sb strings.Builder
	depth, leaderChar := 3, "."
	if snap.TOCDepth > 0 {
		depth = min(snap.TOCDepth, 3)
	}
	if snap.TOCStyle == "plain" {
		leaderChar = " "
	}
	sb.WriteString(`<text:table-of-content text:protected="false" text:name="Table_20_of_20_Contents1"><text:table-of-content-source text:outline-level="` + strconv.Itoa(depth) + `"><text:index-title-template text:style-name="Contents_20_Heading">` + title + `</text:index-title-template>`)
	for l := 1; l <= depth; l++ {
		n := strconv.Itoa(l)
		sb.WriteString(`<text:table-of-content-entry-template text:outline-level="` + n + `" text:style-name="Contents_20_` + n + `"><text:index-entry-link-start/><text:index-entry-text/><text:index-entry-tab-stop style:type="right" style:leader-char="` + leaderChar + `"/><text:index-entry-page-number/><text:index-entry-link-end/></text:table-of-content-entry-template>`)
	}
	sb.WriteString(`</text:table-of-content-source><text:index-body><text:index-title text:name="Table_20_of_20_Contents1_Head"><text:p text:style-name="Contents_20_Heading">` + title + `</text:p></text:index-title>`)
	for _, e := range entries {
		name := b.marks[e.Block]
		sb.WriteString(`<text:p text:style-name="Contents_20_` + strconv.Itoa(e.Level) + `"><text:a xlink:type="simple" xlink:href="#` + name + `">` + esc(e.Text) + `<text:tab/>–</text:a></text:p>`)
	}
	sb.WriteString(`</text:index-body></text:table-of-content>`)
	b.body.WriteString(sb.String())
	b.body.WriteString(`<text:p text:style-name="Page_20_Break"/>`)
}

func (b *odtBuilder) block(blk Block) {
	if blk.Kind != BulletItem && blk.Kind != NumberedItem {
		b.closeLists(0)
	}
	w := &b.body
	switch blk.Kind {
	case Image:
		if p := b.picture(blk); p != "" {
			w.WriteString(`<text:p text:style-name="Standard">` + p + `</text:p>`)
		} else {
			w.WriteString(`<text:p text:style-name="Standard"><text:span text:style-name="Placeholder">[Image]</text:span></text:p>`)
		}
	case Code:
		w.WriteString(`<text:p text:style-name="Preformatted_20_Text">` + odtText(rawCodeText(blk.Segments)) + `</text:p>`)
	case Heading1, Heading2, Heading3:
		n := strconv.Itoa(int(blk.Kind-Heading1) + 1)
		inner := b.inline(blk.Segments)
		if name, ok := b.marks[b.cur]; ok {
			inner = `<text:bookmark-start text:name="` + name + `"/>` + inner + `<text:bookmark-end text:name="` + name + `"/>`
		}
		w.WriteString(`<text:h text:style-name="Heading_20_` + n + `" text:outline-level="` + n + `">` + inner + `</text:h>`)
	case Quote:
		w.WriteString(`<text:p text:style-name="Quotations">` + b.inline(blk.Segments) + `</text:p>`)
	case BulletItem, NumberedItem:
		b.item(blk.Level, blk.Kind == NumberedItem)
		w.WriteString(`<text:p text:style-name="List_20_Paragraph">` + b.inline(blk.Segments) + `</text:p>`)
	default:
		w.WriteString(`<text:p text:style-name="Standard">` + b.inline(blk.Segments) + `</text:p>`)
	}
}

func (b *odtBuilder) table(cells []Block, id int) {
	b.closeLists(0)
	rows, cols := cells[0].TableRows, cells[0].TableCols
	w := &b.body
	n := strconv.Itoa(id)
	w.WriteString(`<table:table table:name="Table` + n + `" table:style-name="Table"><table:table-column table:style-name="TableCol" table:number-columns-repeated="` + strconv.Itoa(cols) + `"/>`)
	for r := 0; r < rows; r++ {
		w.WriteString(`<table:table-row table:style-name="TableRow">`)
		for c := 0; c < cols; c++ {
			cell := cells[r*cols+c]
			if cell.TableCovered {
				w.WriteString(`<table:covered-table-cell/>`)
				continue
			}
			cs, ps := b.cellStyle(cell.TableBackground, cell.TableAlign, keepRowWithNext(r, rows))
			style := "TableCell"
			if cs != "" {
				style = cs
			}
			attrs := ` table:style-name="` + style + `" office:value-type="string"`
			if cell.TableColSpan > 1 {
				attrs += ` table:number-columns-spanned="` + strconv.Itoa(cell.TableColSpan) + `"`
			}
			if cell.TableRowSpan > 1 {
				attrs += ` table:number-rows-spanned="` + strconv.Itoa(cell.TableRowSpan) + `"`
			}
			ps2 := "Table_20_Contents"
			if ps != "" {
				ps2 = ps
			}
			w.WriteString(`<table:table-cell` + attrs + `><text:p text:style-name="` + ps2 + `">` + b.inline(cell.Segments) + `</text:p></table:table-cell>`)
		}
		w.WriteString(`</table:table-row>`)
	}
	w.WriteString(`</table:table>`)
}

func (b *odtBuilder) picture(blk Block) string {
	head, b64, ok := strings.Cut(blk.ImageSrc, ",")
	if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
		return ""
	}
	ext := map[string]string{"data:image/png;base64": "png", "data:image/jpeg;base64": "jpg", "data:image/jpg;base64": "jpg", "data:image/gif;base64": "gif"}[head]
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
	const maxIn = 6.0
	win, hin := wpx/96, hpx/96
	if win > maxIn {
		hin, win = hin*maxIn/win, maxIn
	}
	b.imgs++
	name := "Pictures/image" + strconv.Itoa(b.imgs) + "." + ext
	b.media[name] = raw
	title := ""
	if blk.ImageAlt != "" {
		title = `<svg:title>` + esc(blk.ImageAlt) + `</svg:title>`
	}
	return fmt.Sprintf(`<draw:frame draw:name="Image%d" text:anchor-type="as-char" svg:width="%.3fin" svg:height="%.3fin"><draw:image xlink:href="%s" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/>%s</draw:frame>`,
		b.imgs, win, hin, name, title)
}

func (ODT) Export(snap Snapshot) ([]byte, error) {
	b := &odtBuilder{styles: map[string]string{}, cells: map[string]string{}, media: map[string][]byte{}, notes: map[int]string{}}
	for _, blk := range snap.Blocks {
		if blk.Note > 0 {
			var sb strings.Builder
			for _, s := range blk.Segments {
				if s.SoftBreak {
					sb.WriteByte(' ')
				} else {
					sb.WriteString(s.Text)
				}
			}
			b.notes[blk.Note] = strings.TrimSpace(sb.String())
		}
	}
	b.marks = tocMarks(snap, func(block int) string { return "__Toc_" + strconv.Itoa(block) })
	tables := 0
	for i := 0; i < len(snap.Blocks); i++ {
		if snap.tocHere(i) {
			b.contents(snap)
		}
		blk := snap.Blocks[i]
		b.cur = i
		if blk.Note > 0 {
			continue // written as a footnote where its mark is
		}
		if blk.Kind == TableCell {
			if blk.TableRow == 0 && blk.TableCol == 0 {
				if n := blk.TableRows * blk.TableCols; n > 0 && i+n <= len(snap.Blocks) {
					tables++
					b.table(snap.Blocks[i:i+n], tables)
					i += n - 1
					continue
				}
			}
			b.block(Block{Kind: Paragraph, Segments: blk.Segments})
			continue
		}
		b.block(blk)
	}
	b.closeLists(0)
	if snap.tocEnd() {
		b.contents(snap)
	}
	if b.body.Len() == 0 {
		b.body.WriteString(`<text:p text:style-name="Standard"/>`)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// The mimetype comes first and is stored, so a reader can tell the type from the first bytes.
	if err := zipMimetype(zw, "application/vnd.oasis.opendocument.text"); err != nil {
		return nil, err
	}
	const header = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
	manifest := `<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2"><manifest:file-entry manifest:full-path="/" manifest:version="1.2" manifest:media-type="application/vnd.oasis.opendocument.text"/>` +
		`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/><manifest:file-entry manifest:full-path="styles.xml" manifest:media-type="text/xml"/><manifest:file-entry manifest:full-path="meta.xml" manifest:media-type="text/xml"/>`
	names := make([]string, 0, len(b.media))
	for n := range b.media {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mt := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif"}[n[strings.LastIndexByte(n, '.'):]]
		manifest += `<manifest:file-entry manifest:full-path="` + n + `" manifest:media-type="` + mt + `"/>`
	}
	manifest += `</manifest:manifest>`
	lang := ""
	if snap.Lang != "" {
		lang = `<dc:language>` + esc(snap.Lang) + `</dc:language>`
	}
	files := []struct{ name, content string }{
		{"META-INF/manifest.xml", header + manifest},
		{"meta.xml", header + `<office:document-meta ` + odfNS + ` office:version="1.2"><office:meta><dc:title>` + esc(snap.Title) + `</dc:title>` + lang + `</office:meta></office:document-meta>`},
		{"styles.xml", header + odtStyles(snap)},
		{"content.xml", header + `<office:document-content ` + odfNS + ` office:version="1.2">` + b.automaticStyles() + `<office:body><office:text>` + b.body.String() + `</office:text></office:body></office:document-content>`},
	}
	for _, f := range files {
		w, err := zipEntry(zw, f.name, zip.Deflate)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.content)); err != nil {
			return nil, err
		}
	}
	for _, n := range names {
		w, err := zipEntry(zw, n, zip.Deflate)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(b.media[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *odtBuilder) automaticStyles() string {
	var sb strings.Builder
	sb.WriteString(`<office:automatic-styles>`)
	for _, key := range b.order {
		sb.WriteString(`<style:style style:name="` + b.styles[key] + `" style:family="text"><style:text-properties ` + key + `/></style:style>`)
	}
	for _, key := range b.corder {
		name := b.cells[key]
		kind, val, _ := strings.Cut(key, "|")
		if kind == "c" {
			sb.WriteString(`<style:style style:name="` + name + `" style:family="table-cell"><style:table-cell-properties fo:background-color="#` + val + `" fo:padding="0.04in" fo:border="0.5pt solid #000000"/></style:style>`)
		} else {
			al, keep, _ := strings.Cut(val, "|")
			props := ""
			if align := map[string]string{"1": "center", "2": "end"}[al]; align != "" {
				props += ` fo:text-align="` + align + `"`
			}
			if keep == "keep" {
				props += ` fo:keep-with-next="always"`
			}
			sb.WriteString(`<style:style style:name="` + name + `" style:family="paragraph" style:parent-style-name="Table_20_Contents"><style:paragraph-properties` + props + `/></style:style>`)
		}
	}
	sb.WriteString(`<style:style style:name="Table" style:family="table"><style:table-properties style:width="6.5in" table:align="margins"/></style:style>`)
	sb.WriteString(`<style:style style:name="TableCol" style:family="table-column"/>`)
	sb.WriteString(`<style:style style:name="TableRow" style:family="table-row"><style:table-row-properties fo:keep-together="always"/></style:style>`)
	sb.WriteString(`<style:style style:name="TableCell" style:family="table-cell"><style:table-cell-properties fo:padding="0.04in" fo:border="0.5pt solid #000000"/></style:style>`)
	sb.WriteString(`<style:style style:name="Placeholder" style:family="text"><style:text-properties fo:font-style="italic"/></style:style>`)
	sb.WriteString(`<text:list-style style:name="LB">`)
	for l := 1; l <= 9; l++ {
		fmt.Fprintf(&sb, `<text:list-level-style-bullet text:level="%d" text:bullet-char="%s"><style:list-level-properties text:list-level-position-and-space-mode="label-alignment"><style:list-level-label-alignment text:label-followed-by="listtab" text:list-tab-stop-position="%.2fin" fo:text-indent="-0.25in" fo:margin-left="%.2fin"/></style:list-level-properties></text:list-level-style-bullet>`, l, []string{"•", "◦", "▪"}[(l-1)%3], 0.5*float64(l), 0.5*float64(l))
	}
	sb.WriteString(`</text:list-style><text:list-style style:name="LN">`)
	for l := 1; l <= 9; l++ {
		fmt.Fprintf(&sb, `<text:list-level-style-number text:level="%d" style:num-suffix="." style:num-format="%s"><style:list-level-properties text:list-level-position-and-space-mode="label-alignment"><style:list-level-label-alignment text:label-followed-by="listtab" text:list-tab-stop-position="%.2fin" fo:text-indent="-0.25in" fo:margin-left="%.2fin"/></style:list-level-properties></text:list-level-style-number>`, l, []string{"1", "a", "i"}[(l-1)%3], 0.5*float64(l), 0.5*float64(l))
	}
	sb.WriteString(`</text:list-style></office:automatic-styles>`)
	return sb.String()
}

// odtStyles is styles.xml: the named styles, the page and its running header and footer.
func odtStyles(snap Snapshot) string {
	in := func(px float64) string { return fmt.Sprintf("%.3fin", px/96) }
	th := themeOf(snap)
	lang := ""
	if l, c, _ := strings.Cut(snap.Lang, "-"); l != "" {
		lang = ` fo:language="` + esc(l) + `"`
		if c != "" {
			lang += ` fo:country="` + esc(strings.ToUpper(c)) + `"`
		}
	}
	var hf strings.Builder
	width := snap.PageWidth - snap.MarginLeft - snap.MarginRight
	for _, p := range []struct{ tag, text string }{{"header", snap.Header}, {"footer", snap.Footer}} {
		if p.text == "" {
			continue
		}
		l, c, r := decorParts(p.text)
		hf.WriteString(`<style:` + p.tag + `><text:p text:style-name="HeaderFooter">` + odtDecor(l, snap.Title) + `<text:tab/>` + odtDecor(c, snap.Title) + `<text:tab/>` + odtDecor(r, snap.Title) + `</text:p></style:` + p.tag + `>`)
	}
	pm := fmt.Sprintf(`<style:page-layout style:name="pm1"><style:page-layout-properties fo:page-width="%s" fo:page-height="%s" fo:margin-top="%s" fo:margin-bottom="%s" fo:margin-left="%s" fo:margin-right="%s" style:print-orientation="portrait"`+odtPageColour(th)+`/>`,
		in(snap.PageWidth), in(snap.PageHeight), in(snap.MarginTop), in(snap.MarginBottom), in(snap.MarginLeft), in(snap.MarginRight))
	if snap.Header != "" {
		pm += `<style:header-style/>`
	}
	if snap.Footer != "" {
		pm += `<style:footer-style/>`
	}
	pm += `</style:page-layout>`
	return `<office:document-styles ` + odfNS + ` office:version="1.2"><office:styles>` +
		`<style:default-style style:family="paragraph"><style:paragraph-properties fo:margin-bottom="0.08in" fo:line-height="115%"/><style:text-properties fo:font-size="` + odtBodySize(th) + `"` + odtTextColour(th) + lang + `/></style:default-style>` + odtLinkStyle(th) +
		`<style:style style:name="Standard" style:family="paragraph" style:class="text"/>` +
		`<style:style style:name="Heading" style:family="paragraph" style:class="text"><style:paragraph-properties fo:margin-top="0.17in" fo:margin-bottom="0.08in" fo:keep-with-next="always"/></style:style>` +
		`<style:style style:name="Heading_20_1" style:display-name="Heading 1" style:family="paragraph" style:parent-style-name="Heading" style:default-outline-level="1"><style:text-properties fo:font-size="20pt" fo:font-weight="bold"/></style:style>` +
		`<style:style style:name="Heading_20_2" style:display-name="Heading 2" style:family="paragraph" style:parent-style-name="Heading" style:default-outline-level="2"><style:text-properties fo:font-size="16pt" fo:font-weight="bold"/></style:style>` +
		`<style:style style:name="Heading_20_3" style:display-name="Heading 3" style:family="paragraph" style:parent-style-name="Heading" style:default-outline-level="3"><style:text-properties fo:font-size="13pt" fo:font-weight="bold"/></style:style>` +
		`<style:style style:name="Contents_20_Heading" style:display-name="Contents Heading" style:family="paragraph" style:parent-style-name="Heading"><style:text-properties fo:font-size="20pt" fo:font-weight="bold"/></style:style>` +
		contentsStyles(snap) +
		`<style:style style:name="Page_20_Break" style:display-name="Page Break" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties fo:break-after="page"/></style:style>` +
		`<style:style style:name="Quotations" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties fo:margin-left="0.4in" fo:border-left="2pt solid #9ca3af" fo:padding-left="0.1in"/><style:text-properties fo:font-style="italic"/></style:style>` +
		`<style:style style:name="Preformatted_20_Text" style:display-name="Preformatted Text" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties fo:background-color="#f3f4f6" fo:margin-bottom="0in"/><style:text-properties fo:font-family="monospace" fo:font-size="10pt"/></style:style>` +
		`<style:style style:name="List_20_Paragraph" style:display-name="List Paragraph" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties fo:margin-bottom="0.02in"/></style:style>` +
		`<style:style style:name="Table_20_Contents" style:display-name="Table Contents" style:family="paragraph" style:parent-style-name="Standard"/>` +
		`<style:style style:name="Footnote" style:family="paragraph" style:parent-style-name="Standard"><style:text-properties fo:font-size="10pt"/></style:style>` +
		`<style:style style:name="HeaderFooter" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties><style:tab-stops><style:tab-stop style:position="` + in(width/2) + `" style:type="center"/><style:tab-stop style:position="` + in(width) + `" style:type="right"/></style:tab-stops></style:paragraph-properties></style:style>` +
		`</office:styles><office:automatic-styles>` + pm + `</office:automatic-styles>` +
		`<office:master-styles><style:master-page style:name="Standard" style:page-layout-name="pm1">` + hf.String() + `</style:master-page></office:master-styles></office:document-styles>`
}

// odtDecor writes a header or footer part with the fields as ODF fields.
func odtDecor(text, title string) string {
	var sb strings.Builder
	lit := func(s string) {
		if s != "" {
			sb.WriteString(odtText(s))
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
			sb.WriteString(`<text:page-number text:select-page="current">1</text:page-number>`)
			rest = rest[len(fieldPage):]
		case strings.HasPrefix(rest, fieldPages):
			sb.WriteString(`<text:page-count>1</text:page-count>`)
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

// contentsStyles are the paragraph styles of the table of contents entries: indented by level, with a right tab
// stop at the edge of the text that has dots in front of it.
func contentsStyles(snap Snapshot) string {
	width := (snap.PageWidth - snap.MarginLeft - snap.MarginRight) / 96
	if width <= 0 {
		width = 6.5
	}
	var sb strings.Builder
	for l := 1; l <= 3; l++ {
		fmt.Fprintf(&sb, `<style:style style:name="Contents_20_%d" style:display-name="Contents %d" style:family="paragraph" style:parent-style-name="Standard"><style:paragraph-properties fo:margin-left="%.2fin" fo:margin-bottom="0.03in"><style:tab-stops><style:tab-stop style:position="%.3fin" style:type="right" style:leader-style="dotted" style:leader-text="."/></style:tab-stops></style:paragraph-properties></style:style>`,
			l, l, float64(l-1)*0.25, width-float64(l-1)*0.25)
	}
	return sb.String()
}

// odtBodySize is the default font size: 11pt, or the theme's body size.
func odtBodySize(th docTheme) string {
	if th.bodyPt > 0 {
		return strconv.FormatFloat(th.bodyPt, 'f', -1, 64) + "pt"
	}
	return "11pt"
}

// odtTextColour is the fo:color attribute of the default text, or nothing.
func odtTextColour(th docTheme) string {
	if th.text == "" {
		return ""
	}
	return ` fo:color="#` + strings.ToLower(th.text) + `"`
}

// odtPageColour is the page background attribute of the page layout, or nothing.
func odtPageColour(th docTheme) string {
	if th.background == "" {
		return ""
	}
	return ` fo:background-color="#` + strings.ToLower(th.background) + `"`
}

// odtLinkStyle is the character style links use ("Internet_20_link"), in the theme's link colour or the usual blue.
func odtLinkStyle(th docTheme) string {
	link := "0563c1" // the usual blue
	if th.link != "" {
		link = strings.ToLower(th.link)
	}
	return `<style:style style:name="Internet_20_link" style:display-name="Internet link" style:family="text"><style:text-properties fo:color="#` + link + `" style:text-underline-style="solid" style:text-underline-width="auto" style:text-underline-color="font-color"/></style:style>`
}
