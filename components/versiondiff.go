package components

import (
	"strconv"

	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/xango"
)

// VersionDiffProps configures VersionDiff.
type VersionDiffProps struct {
	// ID is the list's id; "version-diff" when empty.
	ID string
	// Label names the list; "Changes between the two versions" (translated) when empty.
	Label   string
	Changes []docdiff.Change
	// ShowUnchanged lists the blocks that did not change too (as context); by default a run of them is
	// one line saying how many there are.
	ShowUnchanged bool
}

// VersionDiff shows the difference between two versions of a document as a list, one item per changed
// block. It is not coloured text alone: each item starts with words a screen reader says ("Inserted
// paragraph", "Deleted heading 2", "Edited paragraph"), added words are <ins> and removed words are
// <del>, and the list opens with a count ("3 changes: 1 inserted, 1 deleted, 1 edited").
func VersionDiff(p VersionDiffProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "version-diff"
	}
	label := p.Label
	if label == "" {
		label = tr("diff.label", "Changes between the two versions")
	}
	sum := docdiff.Summarize(p.Changes)
	if !sum.Changed() {
		return xango.El("p", []xango.Attr{xango.A("class", "gj-diff-none"), xango.A("id", id)}, xango.Text(tr("diff.same", "The two versions are the same.")))
	}
	total := sum.Inserted + sum.Deleted + sum.Edited
	summary := trn("diff.count", "%d change", "%d changes", total) + ": " +
		trf("diff.parts", "%d inserted, %d deleted, %d edited", sum.Inserted, sum.Deleted, sum.Edited)
	var items []xango.Node
	flush := func(n int) {
		if n > 0 {
			items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-diff-same")}, xango.Text(trn("diff.unchanged", "%d block unchanged", "%d blocks unchanged", n))))
		}
	}
	same := 0
	for _, c := range p.Changes {
		if c.Op == docdiff.Equal {
			if p.ShowUnchanged {
				items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-diff-same")}, xango.Text(c.Text)))
			} else {
				same++
			}
			continue
		}
		flush(same)
		same = 0
		items = append(items, diffItem(c))
	}
	flush(same)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-diff"), xango.A("id", id)},
		xango.El("p", []xango.Attr{xango.A("class", "gj-diff-summary"), xango.A("role", "status")}, xango.Text(summary)),
		xango.El("ol", []xango.Attr{xango.A("class", "gj-diff-list"), xango.A("aria-label", label)}, items...))
}

func diffItem(c docdiff.Change) xango.Node {
	var content []xango.Node
	switch c.Op {
	case docdiff.Insert:
		content = []xango.Node{xango.El("ins", nil, xango.Text(c.Text))}
	case docdiff.Delete:
		content = []xango.Node{xango.El("del", nil, xango.Text(c.Text))}
	default:
		for _, r := range c.Runs {
			switch r.Op {
			case docdiff.Insert:
				content = append(content, xango.El("ins", nil, xango.Text(r.Text)))
			case docdiff.Delete:
				content = append(content, xango.El("del", nil, xango.Text(r.Text)))
			default:
				content = append(content, xango.Text(r.Text))
			}
		}
	}
	// What a screen reader hears first; the visible markup repeats it in colour and strike-through.
	lead := map[docdiff.Op]string{
		docdiff.Insert: tr("diff.inserted", "Inserted"),
		docdiff.Delete: tr("diff.deleted", "Deleted"),
		docdiff.Edit:   tr("diff.edited", "Edited"),
	}[c.Op]
	kind := diffKindName(c)
	attrs := []xango.Attr{xango.A("class", "gj-diff-item gj-diff-"+c.Op.String()), xango.A("data-op", c.Op.String())}
	if c.Level > 0 {
		attrs = append(attrs, xango.A("data-level", strconv.Itoa(c.Level)))
	}
	return xango.El("li", attrs,
		xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(lead+" "+kind+": ")),
		xango.El("span", []xango.Attr{xango.A("class", "gj-diff-text")}, content...))
}

// diffKindName is the name of the changed block's kind, in the interface language.
func diffKindName(c docdiff.Change) string {
	switch docdiff.KindName(c.Kind) {
	case "heading 1":
		return tr("diff.heading1", "heading 1")
	case "heading 2":
		return tr("diff.heading2", "heading 2")
	case "heading 3":
		return tr("diff.heading3", "heading 3")
	case "bullet item":
		return tr("diff.bullet", "bullet item")
	case "numbered item":
		return tr("diff.numbered", "numbered item")
	case "quote":
		return tr("diff.quote", "quote")
	case "code block":
		return tr("diff.code", "code block")
	case "picture":
		return tr("diff.picture", "picture")
	case "table cell":
		return tr("diff.cell", "table cell")
	}
	return tr("diff.paragraph", "paragraph")
}
