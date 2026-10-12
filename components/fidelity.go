package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// FidelityLoss is one line of a fidelity report: a feature of the document, what the format does with it
// ("dropped" or "simplified"), a note on how, and in how many places the document uses it (0 when it is
// not a count).
type FidelityLoss struct {
	Feature, Treatment, Note string
	Count                    int
}

// FidelityReport is what a format loses of a document.
type FidelityReport struct {
	Losses []FidelityLoss
	// Known says the host knows what the format writes (or reads); false makes the panel say so instead
	// of "nothing is lost".
	Known bool
}

// FidelityPanelProps configures FidelityPanel.
type FidelityPanelProps struct {
	// Formats are the formats to export as, Format the one chosen; OnFormat chooses another.
	Formats  []SaverChoice
	Format   string
	OnFormat func(name string)
	// Export is what exporting the document as Format loses.
	Export FidelityReport
	// Import, if ImportName is set, is what the last file imported left out.
	Import     FidelityReport
	ImportName string
}

// fidelityFeature is the name of a feature in a report.
func fidelityFeature(id string) string {
	names := map[string]string{
		"bold":             tr("fidelity.f.bold", "Bold"),
		"italic":           tr("fidelity.f.italic", "Italic"),
		"underline":        tr("fidelity.f.underline", "Underline"),
		"strike":           tr("fidelity.f.strike", "Strike-through"),
		"superscript":      tr("fidelity.f.superscript", "Superscript"),
		"subscript":        tr("fidelity.f.subscript", "Subscript"),
		"link":             tr("fidelity.f.link", "Links"),
		"colour":           tr("fidelity.f.colour", "Text colour"),
		"highlight":        tr("fidelity.f.highlight", "Highlight colour"),
		"font-size":        tr("fidelity.f.fontSize", "Font size"),
		"font-family":      tr("fidelity.f.fontFamily", "Font"),
		"text-case":        tr("fidelity.f.textCase", "Capitals styling"),
		"run-language":     tr("fidelity.f.runLanguage", "Language of a passage"),
		"suggestion":       tr("fidelity.f.suggestion", "Suggested changes"),
		"comment":          tr("fidelity.f.comment", "Comments"),
		"bookmark":         tr("fidelity.f.bookmark", "Bookmarks and index entries"),
		"heading":          tr("fidelity.f.heading", "Headings"),
		"deep-heading":     tr("fidelity.f.deepHeading", "Headings below level 3"),
		"list":             tr("fidelity.f.list", "Lists"),
		"nested-list":      tr("fidelity.f.nestedList", "Nested lists"),
		"check-list":       tr("fidelity.f.checkList", "Check lists"),
		"quote":            tr("fidelity.f.quote", "Quotes"),
		"code":             tr("fidelity.f.code", "Code"),
		"alignment":        tr("fidelity.f.alignment", "Paragraph alignment"),
		"paragraph-layout": tr("fidelity.f.paragraphLayout", "Indents and spacing"),
		"paragraph-style":  tr("fidelity.f.paragraphStyle", "Named paragraph styles"),
		"columns":          tr("fidelity.f.columns", "Columns"),
		"page-break":       tr("fidelity.f.pageBreak", "Page breaks"),
		"rule":             tr("fidelity.f.rule", "Horizontal rules"),
		"footnote":         tr("fidelity.f.footnote", "Footnotes"),
		"contents":         tr("fidelity.f.contents", "Table of contents and index"),
		"image":            tr("fidelity.f.image", "Pictures"),
		"image-alt":        tr("fidelity.f.imageAlt", "Picture descriptions (alt text)"),
		"image-effects":    tr("fidelity.f.imageEffects", "Picture cropping and transparency"),
		"table":            tr("fidelity.f.table", "Tables"),
		"table-col-span":   tr("fidelity.f.colSpan", "Cells merged across columns"),
		"table-row-span":   tr("fidelity.f.rowSpan", "Cells merged down rows"),
		"table-shading":    tr("fidelity.f.cellShading", "Cell shading"),
		"table-align":      tr("fidelity.f.cellAlign", "Cell alignment"),
		"table-text-style": tr("fidelity.f.cellTextStyle", "Italic and colour in table cells"),
		"doc-language":     tr("fidelity.f.docLanguage", "Document language"),
		"title":            tr("fidelity.f.title", "Document title"),
		"history":          tr("fidelity.f.history", "Version history"),
		"header-footer":    tr("fidelity.f.headerFooter", "Headers and footers"),
		"text-box":         tr("fidelity.f.textBox", "Text boxes"),
		"drawing":          tr("fidelity.f.drawing", "Shapes, charts and diagrams"),
		"equation":         tr("fidelity.f.equation", "Equations"),
		"endnote":          tr("fidelity.f.endnote", "Endnotes"),
		"field":            tr("fidelity.f.field", "Fields (page numbers, cross references)"),
		"layout":           tr("fidelity.f.layout", "Fonts, styles, pictures, tables and structure"),
	}
	if n, ok := names[id]; ok {
		return n
	}
	return id
}

// fidelityNote says how a feature is written when it is not as it is ("" for no note).
func fidelityNote(id string) string {
	switch id {
	case "text":
		return tr("fidelity.n.text", "Only the plain text is kept.")
	case "placeholder":
		return tr("fidelity.n.placeholder", "An [Image] marker stands in its place.")
	case "accepted":
		return tr("fidelity.n.accepted", "Suggested changes are written as if accepted, so text proposed to be removed is not in the file.")
	case "content-lost":
		return tr("fidelity.n.contentLost", "The content itself is left out, not only its formatting.")
	case "marks":
		return tr("fidelity.n.marks", "Written with marks that only this editor's Markdown reads back; other programs show them as characters.")
	case "folded":
		return tr("fidelity.n.folded", "Written as a level 3 heading.")
	case "glyph":
		return tr("fidelity.n.glyph", "Written as a character (a box) in the text.")
	case "rule-line":
		return tr("fidelity.n.ruleLine", "Written as a line of dash characters.")
	case "notes":
		return tr("fidelity.n.notes", "Written as numbers, with the notes in a list at the end.")
	case "bullets":
		return tr("fidelity.n.bullets", "List markers are written as characters, not as a real list.")
	}
	return ""
}

func fidelityItems(rep FidelityReport) []xango.Node {
	var items []xango.Node
	for _, l := range rep.Losses {
		word := tr("fidelity.dropped", "Left out")
		if l.Treatment == "simplified" {
			word = tr("fidelity.simplified", "Simplified")
		}
		text := fidelityFeature(l.Feature)
		if l.Count > 0 {
			text += " " + trn("fidelity.places", "(%d place)", "(%d places)", l.Count)
		}
		kids := []xango.Node{
			xango.El("strong", []xango.Attr{xango.A("class", "gj-fidelity-treatment")}, xango.Text(word+": ")),
			xango.El("span", []xango.Attr{xango.A("class", "gj-fidelity-feature")}, xango.Text(text)),
		}
		if n := fidelityNote(l.Note); n != "" {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-doc-meta gj-fidelity-note")}, xango.Text(n)))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-fidelity-item gj-fidelity-"+l.Feature),
			xango.A("data-feature", l.Feature), xango.A("data-treatment", l.Treatment)}, kids...))
	}
	return items
}

// FidelityPanel shows what a file format changes in the document before it is exported, and what the last
// file imported left out: each feature the document uses that the format drops or simplifies, with how it
// is written. The summary is a live status, so a reader hears it when the format is chosen. Nothing here
// blocks an export; it tells the person what the file will not hold.
func FidelityPanel(p FidelityPanelProps) xango.Node {
	var opts []xango.Node
	label := p.Format
	for _, c := range p.Formats {
		attrs := []xango.Attr{xango.A("value", c.Name)}
		if c.Name == p.Format {
			attrs = append(attrs, xango.A("selected", "selected"))
			label = c.Label
		}
		opts = append(opts, xango.El("option", attrs, xango.Text(c.Label)))
	}
	sel := []xango.Attr{xango.A("id", "gj-fidelity-format"), xango.A("class", "gj-saver-select"), xango.A("data-field", "fidelity-format")}
	if p.OnFormat != nil {
		sel = append(sel, xango.On("change", func(e xango.Event) { p.OnFormat(xango.EventValue[string](e)) }))
	}
	var summary string
	switch {
	case !p.Export.Known:
		summary = trf("fidelity.unknown", "%s: what this format keeps is not known for this document.", label)
	case len(p.Export.Losses) == 0:
		summary = trf("fidelity.none", "%s: nothing in this document is lost.", label)
	default:
		summary = trf("fidelity.summary", "%s: %s", label,
			trn("fidelity.count", "%d thing in this document is left out or simplified.", "%d things in this document are left out or simplified.", len(p.Export.Losses)))
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title"), xango.A("id", "gj-fidelity-title")}, xango.Text(tr("fidelity.title", "What a format changes"))),
		xango.El("label", []xango.Attr{xango.A("class", "gj-saver-field")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-label")}, xango.Text(tr("fidelity.format", "Export as"))),
			xango.El("select", sel, opts...)),
		xango.El("p", []xango.Attr{xango.A("id", "gj-fidelity-summary"), xango.A("role", "status")}, xango.Text(summary)),
	}
	if items := fidelityItems(p.Export); len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "gj-fidelity-list"), xango.A("class", "gj-fidelity-list"), xango.A("aria-label", tr("fidelity.listLabel", "Lost or simplified in this format"))}, items...))
	}
	if p.ImportName != "" {
		var line string
		switch {
		case !p.Import.Known:
			line = trf("fidelity.importUnknown", "%s was imported. What it held that was left out is not known for this kind of file.", p.ImportName)
		case len(p.Import.Losses) == 0:
			line = trf("fidelity.importNone", "%s was imported with nothing left out that this editor can tell.", p.ImportName)
		default:
			line = trf("fidelity.importSome", "%s was imported. %s", p.ImportName,
				trn("fidelity.importCount", "%d kind of content in the file was not brought over.", "%d kinds of content in the file were not brought over.", len(p.Import.Losses)))
		}
		nodes = append(nodes,
			xango.El("h5", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("fidelity.importTitle", "Last import"))),
			xango.El("p", []xango.Attr{xango.A("id", "gj-fidelity-import"), xango.A("role", "status")}, xango.Text(line)))
		if items := fidelityItems(p.Import); len(items) > 0 {
			nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "gj-fidelity-import-list"), xango.A("class", "gj-fidelity-list"), xango.A("aria-label", tr("fidelity.importListLabel", "Left out of the last import"))}, items...))
		}
	}
	return xango.El("section", []xango.Attr{xango.A("class", "gj-fidelity"), xango.A("aria-labelledby", "gj-fidelity-title"), xango.A("data-losses", strconv.Itoa(len(p.Export.Losses)))}, nodes...)
}
