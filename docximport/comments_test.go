package docximport

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

const w14NS = `xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml" xmlns:w15="http://schemas.microsoft.com/office/word/2012/wordml"`

func commented(doc docfile.Document, id string) string {
	var sb strings.Builder
	for _, it := range doc.Content {
		if it.Chars != nil && it.Chars.Format.Comment == id {
			sb.WriteString(it.Chars.Text)
		}
	}
	return sb.String()
}

func TestCommentsAreImportedWithTheirRange(t *testing.T) {
	data := docxParts(t, map[string]string{
		"word/document.xml": `<w:document ` + wNS + `><w:body>` +
			`<w:p><w:r><w:t>Before </w:t></w:r><w:commentRangeStart w:id="0"/><w:r><w:t>the claim</w:t></w:r><w:commentRangeEnd w:id="0"/><w:r><w:commentReference w:id="0"/></w:r><w:r><w:t> after.</w:t></w:r></w:p>` +
			`<w:p><w:commentRangeStart w:id="1"/><w:r><w:t>spans </w:t></w:r></w:p><w:p><w:r><w:t>two paragraphs</w:t></w:r><w:commentRangeEnd w:id="1"/></w:p>` +
			`</w:body></w:document>`,
		"word/comments.xml": `<w:comments ` + wNS + ` ` + w14NS + `>` +
			`<w:comment w:id="0" w:author="Ann" w:date="2024-05-01T10:00:00Z"><w:p w14:paraId="AAA"><w:r><w:t>Source?</w:t></w:r></w:p></w:comment>` +
			`<w:comment w:id="1" w:author="Bo" w:date="2024-05-02T10:00:00Z"><w:p w14:paraId="BBB"><w:r><w:t>First line</w:t></w:r></w:p><w:p w14:paraId="BB2"><w:r><w:t>Second line</w:t></w:r></w:p></w:comment>` +
			`<w:comment w:id="2" w:author="Cy" w:date="2024-05-03T10:00:00Z"><w:p w14:paraId="CCC"><w:r><w:t>Here: a link</w:t></w:r></w:p></w:comment>` +
			`</w:comments>`,
		"word/commentsExtended.xml": `<w15:commentsEx ` + w14NS + `><w15:commentEx w15:paraId="AAA" w15:done="1"/><w15:commentEx w15:paraId="CCC" w15:paraIdParent="AAA" w15:done="0"/><w15:commentEx w15:paraId="BB2" w15:done="0"/></w15:commentsEx>`,
	})
	doc, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Comments) != 2 {
		t.Fatalf("%d top-level comments: %+v", len(doc.Comments), doc.Comments)
	}
	a, b := doc.Comments[0], doc.Comments[1]
	if a.ID != "w0" || a.Author != "Ann" || a.Text != "Source?" || !a.Resolved || a.Created == 0 {
		t.Errorf("first: %+v", a)
	}
	if len(a.Replies) != 1 || a.Replies[0].Author != "Cy" || a.Replies[0].Text != "Here: a link" {
		t.Errorf("the reply joins the comment it answers: %+v", a.Replies)
	}
	if b.ID != "w1" || b.Text != "First line\nSecond line" || b.Resolved {
		t.Errorf("second: %+v", b)
	}
	if got := commented(doc, "w0"); got != "the claim" {
		t.Errorf("comment 0 is on %q", got)
	}
	if got := commented(doc, "w1"); !strings.Contains(got, "spans ") || !strings.Contains(got, "two paragraphs") {
		t.Errorf("a range across paragraphs: %q", got)
	}
	if s, _ := Inspect(data); s.Total() != 0 {
		t.Errorf("Inspect: %+v", s)
	}
	set := docfile.ToComments(doc.Comments)
	if set.Len() != 2 {
		t.Errorf("%d comments in the set", set.Len())
	}
}

func TestOverlappingCommentsAndOnesWithoutTextAreKept(t *testing.T) {
	data := docxParts(t, map[string]string{
		"word/document.xml": `<w:document ` + wNS + `><w:body><w:p>` +
			`<w:commentRangeStart w:id="0"/><w:r><w:t>outer </w:t></w:r><w:commentRangeStart w:id="1"/><w:r><w:t>inner</w:t></w:r><w:commentRangeEnd w:id="1"/><w:r><w:t> tail</w:t></w:r><w:commentRangeEnd w:id="0"/>` +
			`<w:commentRangeStart w:id="2"/><w:commentRangeEnd w:id="2"/><w:r><w:t>.</w:t></w:r></w:p></w:body></w:document>`,
		"word/comments.xml": `<w:comments ` + wNS + `><w:comment w:id="0" w:author="A"><w:p><w:r><w:t>o</w:t></w:r></w:p></w:comment><w:comment w:id="1" w:author="B"><w:p><w:r><w:t>i</w:t></w:r></w:p></w:comment><w:comment w:id="2" w:author="C"><w:p><w:r><w:t>empty</w:t></w:r></w:p></w:comment><w:comment w:id="9" w:author="D"><w:p><w:r><w:t>no range at all</w:t></w:r></w:p></w:comment></w:comments>`,
	})
	doc, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Comments) != 4 {
		t.Fatalf("every comment is listed, anchored or not: %+v", doc.Comments)
	}
	if commented(doc, "w1") != "inner" || commented(doc, "w0") != "outer  tail" || commented(doc, "w2") != "" {
		t.Errorf("w0 %q w1 %q w2 %q", commented(doc, "w0"), commented(doc, "w1"), commented(doc, "w2"))
	}
	data = docxParts(t, map[string]string{
		"word/document.xml": `<w:document ` + wNS + `><w:body><w:p><w:commentRangeStart w:id="7"/><w:r><w:t>x</w:t></w:r><w:commentRangeEnd w:id="7"/></w:p></w:body></w:document>`,
	})
	doc, err = Import(data)
	if err != nil || len(doc.Comments) != 0 || commented(doc, "w7") != "" {
		t.Errorf("%v %+v", err, doc.Comments)
	}
}
