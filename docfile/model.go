// Package docfile is the saved form of a canvas document: a Document
// model that converts to and from TextFlow content, and the encodings it
// can be written in (JSON, protobuf text, protobuf binary).
//
// The model mirrors the canvas types (Format, ParaFormat, StyleDef, ...)
// with explicit protobuf field numbers, so what is on disk stays readable
// when the canvas types change; a test checks the mirrors cover every
// field. The schema is available as .proto text (Schema).
package docfile

import (
	"fmt"
	"reflect"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
)

// Document is one saved document.
type Document struct {
	Title     string      `json:"title,omitempty" pb:"1"`
	Updated   int64       `json:"updated,omitempty" pb:"2"` // unix nanoseconds
	Content   []Item      `json:"content,omitempty" pb:"3"`
	Styles    []StyleDef  `json:"styles,omitempty" pb:"4"`
	Fonts     []FontRef   `json:"fonts,omitempty" pb:"5"`
	Meta      []MetaEntry `json:"meta,omitempty" pb:"6"`
	Comments  []Comment   `json:"comments,omitempty" pb:"7"`
	Footnotes []Footnote  `json:"footnotes,omitempty" pb:"8"`
}

// MetaEntry is a free-form key and value kept with the document.
type MetaEntry struct {
	Key   string `json:"key" pb:"1"`
	Value string `json:"value,omitempty" pb:"2"`
}

// Item is one element of the content: exactly one of its fields is set.
type Item struct {
	Chars   *Chars   `json:"chars,omitempty" pb:"1"`
	Columns *Columns `json:"columns,omitempty" pb:"2"`
	Break   *Break   `json:"break,omitempty" pb:"3"`
	Para    *Para    `json:"para,omitempty" pb:"4"`
	// Footnote is a footnote mark; TOC and Index are the generated blocks.
	Footnote *FootnoteMark `json:"footnote,omitempty" pb:"5"`
	TOC      *Block        `json:"toc,omitempty" pb:"6"`
	Index    *Block        `json:"index,omitempty" pb:"7"`
	// Rule is a horizontal rule; Table a table shown in the text.
	Rule  *RuleItem  `json:"rule,omitempty" pb:"8"`
	Table *TableItem `json:"table,omitempty" pb:"9"`
}

// RuleItem mirrors canvas.Rule.
type RuleItem struct {
	Color     string  `json:"color,omitempty" pb:"1"`
	Thickness float64 `json:"thickness,omitempty" pb:"2"`
}

// TableItem is the text of a table in the flow, row by row.
type TableItem struct {
	Rows []TableRow `json:"rows,omitempty" pb:"1"`
}

// TableRow is one row of a TableItem.
type TableRow struct {
	Cells []string `json:"cells,omitempty" pb:"1"`
}

// FootnoteMark is a footnote mark in the text; its text is in
// Document.Footnotes under ID.
type FootnoteMark struct {
	ID string `json:"id" pb:"1"`
}

// Block is a generated block (table of contents or index); Title is its
// heading, empty for the default.
type Block struct {
	Title string `json:"title,omitempty" pb:"1"`
}

// Footnote is a note's text.
type Footnote struct {
	ID   string `json:"id" pb:"1"`
	Text string `json:"text,omitempty" pb:"2"`
}

// Chars is a run of text with one format.
type Chars struct {
	Text   string `json:"text" pb:"1"`
	Format Format `json:"format" pb:"2"`
}

// Columns mirrors canvas.ColumnConfig.
type Columns struct {
	Widths []float64 `json:"widths,omitempty" pb:"1"`
	Count  int       `json:"count,omitempty" pb:"2"`
	Gap    float64   `json:"gap,omitempty" pb:"3"`
}

// Break mirrors canvas.Break; Kind is the canvas.BreakKind number.
type Break struct {
	Kind int `json:"kind,omitempty" pb:"1"`
}

// Format mirrors canvas.Format.
type Format struct {
	FontFamily  string  `json:"fontFamily,omitempty" pb:"1"`
	FontSize    float64 `json:"fontSize,omitempty" pb:"2"`
	Scale       float64 `json:"scale,omitempty" pb:"3"`
	FontWeight  int     `json:"fontWeight,omitempty" pb:"4"`
	FontStyle   string  `json:"fontStyle,omitempty" pb:"5"`
	Color       string  `json:"color,omitempty" pb:"6"`
	Underline   bool    `json:"underline,omitempty" pb:"7"`
	Strike      bool    `json:"strike,omitempty" pb:"8"`
	Superscript bool    `json:"superscript,omitempty" pb:"9"`
	Subscript   bool    `json:"subscript,omitempty" pb:"10"`
	Background  string  `json:"background,omitempty" pb:"11"`
	Case        int     `json:"case,omitempty" pb:"12"`
	Style       string  `json:"style,omitempty" pb:"13"`
	Link        string  `json:"link,omitempty" pb:"14"`
	Comment     string  `json:"comment,omitempty" pb:"15"`
	Bookmark    string  `json:"bookmark,omitempty" pb:"16"`
	Index       string  `json:"index,omitempty" pb:"17"`
}

// List mirrors canvas.ListStyle.
type List struct {
	Kind    int    `json:"kind,omitempty" pb:"1"`
	Level   int    `json:"level,omitempty" pb:"2"`
	Marker  string `json:"marker,omitempty" pb:"3"`
	Number  int    `json:"number,omitempty" pb:"4"`
	Start   int    `json:"start,omitempty" pb:"5"`
	Checked bool   `json:"checked,omitempty" pb:"6"`
}

// Para mirrors canvas.ParaFormat.
type Para struct {
	Align       int     `json:"align,omitempty" pb:"1"`
	IndentLeft  float64 `json:"indentLeft,omitempty" pb:"2"`
	IndentRight float64 `json:"indentRight,omitempty" pb:"3"`
	FirstLine   float64 `json:"firstLine,omitempty" pb:"4"`
	SpaceBefore float64 `json:"spaceBefore,omitempty" pb:"5"`
	SpaceAfter  float64 `json:"spaceAfter,omitempty" pb:"6"`
	LineSpacing float64 `json:"lineSpacing,omitempty" pb:"7"`
	List        List    `json:"list" pb:"8"`
	QuoteBar    string  `json:"quoteBar,omitempty" pb:"9"`
	Style       string  `json:"style,omitempty" pb:"10"`
	DropCap     int     `json:"dropCap,omitempty" pb:"11"`
}

// StyleDef mirrors canvas.StyleDef.
type StyleDef struct {
	Name      string `json:"name" pb:"1"`
	Format    Format `json:"format" pb:"2"`
	Para      Para   `json:"para" pb:"3"`
	Paragraph bool   `json:"paragraph,omitempty" pb:"4"`
}

// Comment mirrors canvas.Comment; Created is unix nanoseconds.
type Comment struct {
	ID       string  `json:"id" pb:"1"`
	Author   string  `json:"author,omitempty" pb:"2"`
	Text     string  `json:"text,omitempty" pb:"3"`
	Created  int64   `json:"created,omitempty" pb:"4"`
	Resolved bool    `json:"resolved,omitempty" pb:"5"`
	Replies  []Reply `json:"replies,omitempty" pb:"6"`
}

// Reply mirrors canvas.Reply.
type Reply struct {
	Author  string `json:"author,omitempty" pb:"1"`
	Text    string `json:"text,omitempty" pb:"2"`
	Created int64  `json:"created,omitempty" pb:"3"`
}

// FromComments converts a comment set (nil gives none).
func FromComments(set *canvas.CommentSet) []Comment {
	var out []Comment
	for _, c := range set.List() {
		m := Comment{ID: c.ID, Author: c.Author, Text: c.Text, Resolved: c.Resolved}
		if !c.Created.IsZero() {
			m.Created = c.Created.UnixNano()
		}
		for _, r := range c.Replies {
			rr := Reply{Author: r.Author, Text: r.Text}
			if !r.Created.IsZero() {
				rr.Created = r.Created.UnixNano()
			}
			m.Replies = append(m.Replies, rr)
		}
		out = append(out, m)
	}
	return out
}

// ToComments converts saved comments back to a set.
func ToComments(list []Comment) *canvas.CommentSet {
	set := canvas.NewCommentSet()
	for _, m := range list {
		c := canvas.Comment{ID: m.ID, Author: m.Author, Text: m.Text, Resolved: m.Resolved}
		if m.Created != 0 {
			c.Created = time.Unix(0, m.Created)
		}
		for _, r := range m.Replies {
			rr := canvas.Reply{Author: r.Author, Text: r.Text}
			if r.Created != 0 {
				rr.Created = time.Unix(0, r.Created)
			}
			c.Replies = append(c.Replies, rr)
		}
		set.Add(c)
	}
	return set
}

// FontRef is a font the document uses (or was given from the font
// selector) and where its files are, so another machine can fetch them.
type FontRef struct {
	Family   string        `json:"family" pb:"1"`
	Source   string        `json:"source,omitempty" pb:"2"`
	Category string        `json:"category,omitempty" pb:"3"`
	Variants []FontVariant `json:"variants,omitempty" pb:"4"`
}

// FontVariant is one cut of a FontRef. URL is where it came from; Key is
// where the downloaded copy is in the font store ("" until downloaded).
type FontVariant struct {
	Weight int    `json:"weight,omitempty" pb:"1"`
	Italic bool   `json:"italic,omitempty" pb:"2"`
	Style  string `json:"style,omitempty" pb:"3"`
	URL    string `json:"url,omitempty" pb:"4"`
	Key    string `json:"key,omitempty" pb:"5"`
}

// copyFields copies the fields of src that dst has by name, converting
// between the canvas types and their plain mirrors (enums are ints here).
// Fields dst lacks are ignored; use checkMirror to be sure none are.
func copyFields(dst, src any) {
	d, s := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src)
	if s.Kind() == reflect.Pointer {
		s = s.Elem()
	}
	for i := range d.NumField() {
		sf := s.FieldByName(d.Type().Field(i).Name)
		if !sf.IsValid() {
			continue
		}
		df := d.Field(i)
		if df.Kind() == reflect.Struct && sf.Kind() == reflect.Struct {
			copyFields(df.Addr().Interface(), sf.Interface())
			continue
		}
		if sf.Type().ConvertibleTo(df.Type()) {
			df.Set(sf.Convert(df.Type()))
		}
	}
}

// mirrorMissing lists the fields of the canvas struct src that the
// mirror dst lacks (used by a test, so a new canvas field is noticed).
func mirrorMissing(dst, src reflect.Type) []string {
	var missing []string
	for i := range src.NumField() {
		f := src.Field(i)
		df, ok := dst.FieldByName(f.Name)
		switch {
		case !ok:
			missing = append(missing, src.Name()+"."+f.Name)
		case f.Type.Kind() == reflect.Struct && df.Type.Kind() == reflect.Struct:
			missing = append(missing, mirrorMissing(df.Type, f.Type)...)
		}
	}
	return missing
}

// FromItems converts TextFlow content to document items.
func FromItems(items []canvas.Item) []Item {
	var out []Item
	for _, it := range items {
		switch v := it.(type) {
		case canvas.Chars:
			c := &Chars{Text: v.Text}
			copyFields(&c.Format, v.Format)
			out = append(out, Item{Chars: c})
		case canvas.ColumnConfig:
			c := &Columns{}
			copyFields(c, v)
			out = append(out, Item{Columns: c})
		case canvas.Break:
			out = append(out, Item{Break: &Break{Kind: int(v.Kind)}})
		case canvas.ParaFormat:
			p := &Para{}
			copyFields(p, v)
			out = append(out, Item{Para: p})
		case canvas.Inline:
			switch o := v.Object.(type) {
			case *canvas.FootnoteRef:
				out = append(out, Item{Footnote: &FootnoteMark{ID: o.ID}})
			case *canvas.TOCObject:
				out = append(out, Item{TOC: &Block{Title: o.Title}})
			case *canvas.IndexObject:
				out = append(out, Item{Index: &Block{Title: o.Title}})
			case *canvas.Rule:
				out = append(out, Item{Rule: &RuleItem{Color: o.Color, Thickness: o.Thickness}})
			case *canvas.TableObject:
				t := &TableItem{}
				for _, row := range o.View.Table.Cells {
					t.Rows = append(t.Rows, TableRow{Cells: append([]string(nil), row...)})
				}
				out = append(out, Item{Table: t})
			}
		}
	}
	return out
}

// ToItems converts document items back to TextFlow content.
func ToItems(items []Item) []canvas.Item {
	var out []canvas.Item
	for _, it := range items {
		switch {
		case it.Chars != nil:
			c := canvas.Chars{Text: it.Chars.Text}
			copyFields(&c.Format, it.Chars.Format)
			out = append(out, c)
		case it.Columns != nil:
			var c canvas.ColumnConfig
			copyFields(&c, *it.Columns)
			out = append(out, c)
		case it.Break != nil:
			out = append(out, canvas.Break{Kind: canvas.BreakKind(it.Break.Kind)})
		case it.Para != nil:
			var p canvas.ParaFormat
			copyFields(&p, *it.Para)
			out = append(out, p)
		case it.Footnote != nil:
			out = append(out, canvas.Inline{Object: &canvas.FootnoteRef{ID: it.Footnote.ID}})
		case it.TOC != nil:
			out = append(out, canvas.Inline{Object: &canvas.TOCObject{Title: it.TOC.Title}, Block: true})
		case it.Index != nil:
			out = append(out, canvas.Inline{Object: &canvas.IndexObject{Title: it.Index.Title}, Block: true})
		case it.Rule != nil:
			out = append(out, canvas.Inline{Object: &canvas.Rule{Color: it.Rule.Color, Thickness: it.Rule.Thickness}, Block: true})
		case it.Table != nil:
			cells := make([][]string, len(it.Table.Rows))
			for i, r := range it.Table.Rows {
				cells[i] = append([]string(nil), r.Cells...)
			}
			cols := 0
			for _, r := range cells {
				cols = max(cols, len(r))
			}
			if cols == 0 {
				cells, cols = [][]string{{""}}, 1
			}
			for i, r := range cells { // a ragged or hand-edited table is made rectangular
				for len(r) < cols {
					r = append(r, "")
				}
				cells[i] = r
			}
			out = append(out, canvas.Inline{Object: canvas.NewTableObject(&canvas.Table{Cells: cells}), Block: true})
		}
	}
	return out
}

// FromStyles and ToStyles convert named styles.
func FromStyles(defs []canvas.StyleDef) []StyleDef {
	var out []StyleDef
	for _, d := range defs {
		var s StyleDef
		copyFields(&s, d)
		out = append(out, s)
	}
	return out
}

// ToStyles converts saved styles back to canvas styles.
func ToStyles(defs []StyleDef) []canvas.StyleDef {
	var out []canvas.StyleDef
	for _, d := range defs {
		var s canvas.StyleDef
		copyFields(&s, d)
		out = append(out, s)
	}
	return out
}

// FromFlow captures a TextFlow's content and styles as a Document with
// the given title.
func FromFlow(title string, f *canvas.TextFlow) Document {
	return Document{Title: title, Content: FromItems(f.ContentItems()), Styles: FromStyles(f.Styles), Comments: FromComments(f.Comments), Footnotes: FromFootnotes(f.Footnotes)}
}

// Apply puts the document's content and styles into f, replacing what
// it had.
func (d Document) Apply(f *canvas.TextFlow) {
	f.ResetContent() // forget edits and history first: it also clears Items
	f.Source, f.Transform, f.Text = "", nil, ""
	f.Items = ToItems(d.Content)
	f.Styles = ToStyles(d.Styles)
	f.Comments = ToComments(d.Comments)
	f.Footnotes = ToFootnotes(d.Footnotes)
}

// Get returns the value of a Meta key.
func (d Document) Get(key string) (string, bool) {
	for _, m := range d.Meta {
		if m.Key == key {
			return m.Value, true
		}
	}
	return "", false
}

// Set sets a Meta key.
func (d *Document) Set(key, value string) {
	for i := range d.Meta {
		if d.Meta[i].Key == key {
			d.Meta[i].Value = value
			return
		}
	}
	d.Meta = append(d.Meta, MetaEntry{Key: key, Value: value})
}

func (d Document) String() string {
	return fmt.Sprintf("document %q (%d items)", d.Title, len(d.Content))
}

// FromFonts converts a font library's fonts (fonts.Library.Added) to the
// references a document keeps.
func FromFonts(list []fonts.Font) []FontRef {
	var out []FontRef
	for _, f := range list {
		r := FontRef{Family: f.Family, Source: f.Source, Category: f.Category}
		for _, v := range f.Variants {
			r.Variants = append(r.Variants, FontVariant{Weight: v.Weight, Italic: v.Italic, Style: v.Style, URL: v.URL, Key: v.Key})
		}
		out = append(out, r)
	}
	return out
}

// ToFonts converts saved font references back to fonts, to Add to a
// fonts.Library when the document is opened.
func ToFonts(refs []FontRef) []fonts.Font {
	var out []fonts.Font
	for _, r := range refs {
		f := fonts.Font{Family: r.Family, Source: r.Source, Category: r.Category}
		for _, v := range r.Variants {
			f.Variants = append(f.Variants, fonts.Variant{Weight: v.Weight, Italic: v.Italic, Style: v.Style, URL: v.URL, Key: v.Key})
		}
		out = append(out, f)
	}
	return out
}

// FromFootnotes converts a footnote set (nil gives none); only notes
// that something refers to are worth saving, so callers may prune first.
func FromFootnotes(set *canvas.FootnoteSet) []Footnote {
	var out []Footnote
	for _, n := range set.List() {
		out = append(out, Footnote{ID: n.ID, Text: n.Text})
	}
	return out
}

// ToFootnotes converts saved footnotes back to a set.
func ToFootnotes(list []Footnote) *canvas.FootnoteSet {
	set := canvas.NewFootnoteSet()
	for _, n := range list {
		set.Add(canvas.Footnote{ID: n.ID, Text: n.Text})
	}
	return set
}
