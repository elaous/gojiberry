package odtimport

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func build(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	if _, ok := files["mimetype"]; !ok {
		files["mimetype"] = "application/vnd.oasis.opendocument.text"
	}
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return b.Bytes()
}

const ns = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0"`

func TestImportReadsLibreOfficeStyleMarkup(t *testing.T) {
	content := `<?xml version="1.0"?>
<office:document-content ` + ns + ` office:version="1.2">
<office:automatic-styles>
 <style:style style:name="T1" style:family="text"><style:text-properties fo:font-weight="bold"/></style:style>
 <style:style style:name="T2" style:family="text"><style:text-properties fo:font-style="italic" style:text-underline-style="solid"/></style:style>
 <style:style style:name="T3" style:family="text"><style:text-properties fo:language="fr" fo:country="FR"/></style:style>
 <style:style style:name="P1" style:family="paragraph" style:parent-style-name="Quotations"/>
 <style:style style:name="C1" style:family="table-cell"><style:table-cell-properties fo:background-color="#FFFF00"/></style:style>
 <text:list-style style:name="L1"><text:list-level-style-bullet text:level="1" text:bullet-char="•"/></text:list-style>
 <text:list-style style:name="L2"><text:list-level-style-number text:level="1" style:num-format="1"/></text:list-style>
</office:automatic-styles>
<office:body><office:text>
 <text:tracked-changes><text:changed-region/></text:tracked-changes>
 <text:h text:style-name="Heading_20_2" text:outline-level="2">Chapter</text:h>
 <text:p text:style-name="Standard">Plain <text:span text:style-name="T1">bold</text:span> and <text:span text:style-name="T2">it</text:span>   <text:s text:c="3"/>sp<text:tab/>tab<text:line-break/>next
  <text:a xlink:href="https://example.org/">site</text:a> <text:a xlink:href="javascript:alert(1)">bad</text:a> <text:span text:style-name="T3">bonjour</text:span>
  <text:date>1 Jan</text:date><text:bookmark text:name="b"/><office:annotation><text:p>comment</text:p></office:annotation>
  <text:note text:id="ftn1" text:note-class="footnote"><text:note-citation>1</text:note-citation><text:note-body><text:p>a note</text:p></text:note-body></text:note>
  <text:note text:id="edn1" text:note-class="endnote"><text:note-citation>i</text:note-citation><text:note-body><text:p>an end</text:p></text:note-body></text:note></text:p>
 <text:p text:style-name="P1">A quote</text:p>
 <text:list text:style-name="L1"><text:list-item><text:p>one</text:p><text:list><text:list-item><text:p>inner</text:p></text:list-item></text:list></text:list-item></text:list>
 <text:list text:style-name="L2"><text:list-item><text:p>first</text:p></text:list-item></text:list>
 <table:table><table:table-column table:number-columns-repeated="2"/>
  <table:table-row><table:table-cell table:style-name="C1" table:number-columns-spanned="2"><text:p>head</text:p></table:table-cell><table:covered-table-cell/></table:table-row>
  <table:table-row><table:table-cell><text:p>a</text:p></table:table-cell><table:table-cell><text:p>b</text:p></table:table-cell></table:table-row></table:table>
 <text:p><draw:frame svg:width="2in" svg:height="1in"><draw:image xlink:href="Pictures/p.png"/><svg:title>A picture</svg:title></draw:frame>
  <draw:frame><draw:text-box><text:p>boxed</text:p></draw:text-box></draw:frame></text:p>
</office:text></office:body></office:document-content>`
	styles := `<office:document-styles ` + ns + `><office:styles><style:default-style style:family="paragraph"><style:text-properties fo:language="en"/></style:default-style>
 <style:style style:name="Quotations" style:family="paragraph"><style:paragraph-properties fo:border-left="2pt solid #000"/></style:style></office:styles>
 <office:master-styles><style:master-page style:name="Standard"><style:header><text:p>Running</text:p></style:header></style:master-page></office:master-styles></office:document-styles>`
	meta := `<office:document-meta ` + ns + `><office:meta><dc:title> My doc </dc:title></office:meta></office:document-meta>`
	d, s, err := ImportReport(build(t, map[string]string{"content.xml": content, "styles.xml": styles, "meta.xml": meta, "Pictures/p.png": "\x89PNG"}))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "My doc" {
		t.Errorf("title %q", d.Title)
	}
	var txt strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			txt.WriteString(it.Chars.Text)
		}
	}
	got := txt.String()
	for _, want := range []string{"Chapter\n", "Plain bold and it", "sp\ttab\nnext", "site bad", "one\n", "inner\n", "first\n", "A quote"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "comment") || strings.Contains(got, "boxed") || strings.Contains(got, "an end") {
		t.Errorf("left-out content reached the text: %q", got)
	}
	find := func(sub string) *struct {
		bold, ital, under bool
		link, lang        string
	} {
		for _, it := range d.Content {
			if it.Chars != nil && strings.Contains(it.Chars.Text, sub) {
				f := it.Chars.Format
				return &struct {
					bold, ital, under bool
					link, lang        string
				}{f.FontWeight == 700, f.FontStyle == "italic", f.Underline, f.Link, f.Lang}
			}
		}
		return nil
	}
	if f := find("bold"); f == nil || !f.bold {
		t.Errorf("bold: %+v", f)
	}
	if f := find("it"); f == nil || !f.ital || !f.under {
		t.Errorf("italic underline: %+v", f)
	}
	if f := find("site"); f == nil || f.link != "https://example.org/" {
		t.Errorf("link: %+v", f)
	}
	if f := find("bad"); f == nil || f.link != "" {
		t.Errorf("a javascript: link loses its address: %+v", f)
	}
	if f := find("bonjour"); f == nil || f.lang != "fr-FR" {
		t.Errorf("language (the default is en): %+v", f)
	}
	if f := find("Chapter"); f == nil || !f.bold {
		t.Errorf("heading: %+v", f)
	}
	var kinds []int
	var quote, image, table bool
	for _, it := range d.Content {
		switch {
		case it.Para != nil && it.Para.List.Kind != 0:
			kinds = append(kinds, it.Para.List.Kind*10+it.Para.List.Level)
		case it.Para != nil && it.Para.QuoteBar != "":
			quote = true
		case it.Image != nil:
			image = it.Image.Alt == "A picture" && it.Image.Width == 192 && it.Image.Height == 96 && strings.HasPrefix(it.Image.URL, "data:image/png;base64,")
		case it.Table != nil:
			tb := it.Table
			table = len(tb.Rows) == 2 && tb.Rows[0].Cells[0] == "head" && tb.Rows[1].Cells[1] == "b" && len(tb.Merges) == 1 && tb.Merges[0].Cols == 2 && len(tb.Styles) == 1 && tb.Styles[0].Background == "#ffff00"
		}
	}
	if len(kinds) != 3 || kinds[0] != 10 || kinds[1] != 11 || kinds[2] != 20 {
		t.Errorf("lists (bullet level 0, bullet level 1, numbered): %v", kinds)
	}
	if !quote || !image || !table {
		t.Errorf("quote %v image %v table %v", quote, image, table)
	}
	if len(d.Footnotes) != 1 || d.Footnotes[0].Text != "a note" {
		t.Errorf("footnotes: %+v", d.Footnotes)
	}
	want := Skipped{Comments: 1, HeaderFoots: 1, TextBoxes: 1, TrackedChanges: 1, Endnotes: 1, Fields: 1, Bookmarks: 1, Links: 1}
	if s != want || s.Total() != 8 {
		t.Errorf("skipped %+v, want %+v", s, want)
	}
}

func TestNotAnODTAndLimits(t *testing.T) {
	for name, data := range map[string][]byte{
		"not a zip":     []byte("hello"),
		"wrong type":    build(t, map[string]string{"mimetype": "application/zip", "content.xml": "<a/>"}),
		"no content":    build(t, map[string]string{}),
		"wrong content": build(t, map[string]string{"content.xml": "<x/>"}),
	} {
		if _, err := Import(data); err != ErrNotODT {
			t.Errorf("%s: %v", name, err)
		}
	}
	deep := strings.Repeat("<text:section>", 600) + "<text:p>x</text:p>" + strings.Repeat("</text:section>", 600)
	d, err := Import(build(t, map[string]string{"content.xml": `<office:document-content ` + ns + `><office:body><office:text>` + deep + `</office:text></office:body></office:document-content>`}))
	if err == nil || len(d.Content) != 0 {
		t.Errorf("deep nesting must be refused: %v %v", d, err)
	}
	d, err = Import(build(t, map[string]string{"content.xml": `<office:document-content ` + ns + `><office:body><office:text><text:p><draw:frame><draw:image xlink:href="../../etc/passwd.png"/></draw:frame></text:p></office:text></office:body></office:document-content>`}))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range d.Content {
		if it.Image != nil {
			t.Error("a path outside the package must not be read")
		}
	}
}
