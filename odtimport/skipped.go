package odtimport

// Skipped counts what an .odt file holds that Import does not bring into the document.
type Skipped struct {
	Comments       int // annotations (their text and authors are not read)
	HeaderFoots    int // headers and footers that hold text
	TextBoxes      int // text boxes: their text is not read
	Drawings       int // shapes, charts and other objects that are not pictures
	TrackedChanges int // tracked changes: the text is read as it stands
	Endnotes       int // endnotes (only footnotes are read)
	Fields         int // fields (date, page number...): their last shown text stays, the field is gone
	Bookmarks      int // bookmarks
	Links          int // links whose address is not http, https, mailto or in-page
	Columns        int // sections or pages laid out in columns
}

// Total is the number of skipped elements.
func (s Skipped) Total() int {
	return s.Comments + s.HeaderFoots + s.TextBoxes + s.Drawings + s.TrackedChanges + s.Endnotes + s.Fields + s.Bookmarks + s.Links + s.Columns
}

// countSkipped counts the parts of the package that Import has no place for, apart from those it meets
// while reading (links, fields, bookmarks, endnotes, text boxes and drawings, counted in place).
func (r *reader) countSkipped(content, styles *node) {
	content.walk(func(n *node) bool {
		switch n.name {
		case "annotation":
			r.skipped.Comments++
		case "changed-region":
			r.skipped.TrackedChanges++
		case "columns":
			if c := n.attr("column-count"); c != "" && c != "0" && c != "1" {
				r.skipped.Columns++
			}
		}
		return true
	})
	if styles != nil {
		styles.walk(func(n *node) bool {
			switch n.name {
			case "header", "footer", "header-left", "footer-left", "header-first", "footer-first":
				if len(n.kids) > 0 && trim(textOf(n)) != "" {
					r.skipped.HeaderFoots++
				}
			case "columns":
				if c := n.attr("column-count"); c != "" && c != "0" && c != "1" {
					r.skipped.Columns++
				}
			}
			return true
		})
	}
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
