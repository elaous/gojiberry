// Package pdfua is a small checker for the document-level requirements of PDF/UA (ISO 14289-1) that
// can be read from a PDF's catalog: it marks the file as tagged, names a language, has a structure tree
// whose elements use standard types, and asks the viewer to show the title. It is a gate for the
// exporter in this repository, not a full validator: it also checks that every link annotation is
// referenced by an /OBJR in the tree and that every /Lang is a well-formed language tag, but not that
// every piece of page content is tagged or artifacted, reading order, or fonts. Use veraPDF or PAC for a full audit.
package pdfua

import (
	"bytes"
	"fmt"

	"github.com/dslipak/pdf"
	"github.com/elaous/gojiberry/lang"
)

// Finding is one thing a PDF/UA reader would object to.
type Finding struct {
	Rule    string // the short name of the rule, such as "MarkInfo"
	Message string
}

func (f Finding) String() string { return f.Rule + ": " + f.Message }

// standard is the set of standard structure types (ISO 32000-1 table 333 to 338) that PDF/UA accepts
// without a role map.
var standard = map[string]bool{
	"Document": true, "Part": true, "Art": true, "Sect": true, "Div": true, "BlockQuote": true, "Caption": true,
	"TOC": true, "TOCI": true, "Index": true, "NonStruct": true, "Private": true, "P": true, "H": true,
	"H1": true, "H2": true, "H3": true, "H4": true, "H5": true, "H6": true, "L": true, "LI": true, "Lbl": true,
	"LBody": true, "Table": true, "TR": true, "TH": true, "TD": true, "THead": true, "TBody": true, "TFoot": true,
	"Span": true, "Quote": true, "Note": true, "Reference": true, "BibEntry": true, "Code": true, "Link": true,
	"Annot": true, "Figure": true, "Formula": true, "Form": true, "Ruby": true, "Warichu": true,
}

// maxNodes bounds the walk of the structure tree, so a hostile file cannot make it run for ever.
const maxNodes = 200000

// Check reads a PDF and returns what is wrong; an empty list means the checks pass. An error means the
// file could not be read at all.
func Check(data []byte) ([]Finding, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("pdfua: %w", err)
	}
	var out []Finding
	add := func(rule, msg string) { out = append(out, Finding{rule, msg}) }
	root := r.Trailer().Key("Root")
	if root.IsNull() {
		return nil, fmt.Errorf("pdfua: no document catalog")
	}
	if mi := root.Key("MarkInfo"); mi.IsNull() || !mi.Key("Marked").Bool() {
		add("MarkInfo", "the catalog does not say /MarkInfo << /Marked true >>: the file is not tagged")
	}
	if root.Key("Lang").Kind() != pdf.String || root.Key("Lang").Text() == "" {
		add("Lang", "the catalog has no /Lang: a screen reader cannot choose a voice")
	} else if l := root.Key("Lang").Text(); !lang.ValidTag(l) {
		add("Lang", fmt.Sprintf("the catalog /Lang %q is not a valid language tag", l))
	}
	if vp := root.Key("ViewerPreferences"); vp.IsNull() || !vp.Key("DisplayDocTitle").Bool() {
		add("DisplayDocTitle", "/ViewerPreferences /DisplayDocTitle is not true: a viewer shows the file name, not the title")
	}
	if r.Trailer().Key("Info").Key("Title").Text() == "" {
		add("Title", "the document information has no /Title")
	}
	str := root.Key("StructTreeRoot")
	if str.IsNull() {
		add("StructTreeRoot", "there is no structure tree")
		return out, nil
	}
	roleMap := str.Key("RoleMap")
	count, bad := 0, map[string]bool{}
	badLang := map[string]bool{}
	objrKeys := map[int64][4]float64{} // /StructParent of each annotation an OBJR points at, with its /Rect
	var walk func(v pdf.Value, depth int)
	walk = func(v pdf.Value, depth int) {
		if v.IsNull() || depth > 200 || count > maxNodes {
			return
		}
		switch v.Kind() {
		case pdf.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), depth+1)
			}
		case pdf.Dict:
			if v.Key("Type").Kind() == pdf.Name && v.Key("Type").Name() == "OBJR" {
				if obj := v.Key("Obj"); obj.Key("StructParent").Kind() == pdf.Integer {
					objrKeys[obj.Key("StructParent").Int64()] = rectOf(obj)
				}
			}
			if l := v.Key("Lang"); l.Kind() == pdf.String && !lang.ValidTag(l.Text()) {
				badLang[l.Text()] = true
			}
			if s := v.Key("S"); s.Kind() == pdf.Name {
				count++
				if name := s.Name(); !standard[name] && roleMap.Key(name).IsNull() {
					bad[name] = true
				}
			}
			walk(v.Key("K"), depth+1)
		}
	}
	walk(str.Key("K"), 0)
	if count == 0 {
		add("StructTreeRoot", "the structure tree has no elements")
	}
	for name := range bad {
		add("StructureType", fmt.Sprintf("/%s is neither a standard type nor mapped by /RoleMap", name))
	}
	for l := range badLang {
		add("Lang", fmt.Sprintf("a structure element has /Lang %q, which is not a valid language tag", l))
	}
	// Every link annotation must be in the tree: an /OBJR whose object has the annotation's /StructParent
	// and /Rect (ISO 14289-1 7.18.5; the reader library does not expose object numbers, so the key is
	// the identity).
	for pn := 1; pn <= r.NumPage(); pn++ {
		annots := r.Page(pn).V.Key("Annots")
		for i := 0; i < annots.Len() && i < maxNodes; i++ {
			a := annots.Index(i)
			if a.Key("Subtype").Kind() != pdf.Name || a.Key("Subtype").Name() != "Link" {
				continue
			}
			sp := a.Key("StructParent")
			if sp.Kind() != pdf.Integer {
				add("LinkAnnotation", fmt.Sprintf("a link annotation on page %d has no /StructParent: it is not in the structure tree", pn))
				continue
			}
			if rect, ok := objrKeys[sp.Int64()]; !ok || rect != rectOf(a) {
				add("LinkAnnotation", fmt.Sprintf("a link annotation on page %d (/StructParent %d) is not referenced by an /OBJR of a structure element", pn, sp.Int64()))
			}
		}
	}
	return out, nil
}

// rectOf reads a dictionary's /Rect as four numbers (zeros when it has none).
func rectOf(v pdf.Value) [4]float64 {
	var r [4]float64
	a := v.Key("Rect")
	for i := 0; i < 4 && i < a.Len(); i++ {
		r[i] = a.Index(i).Float64()
	}
	return r
}
