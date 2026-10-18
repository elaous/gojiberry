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
	"crypto/rand"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts/fontdata"
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
	// History holds the earlier versions of the document. It is only filled in a file
	// made to move a document with its history (the .open format); saving such a
	// document restores the versions in the store, and a stored document never has it.
	History []Revision `json:"history,omitempty" pb:"9"`
}

// Revision is one earlier version of a document: the document as it was, and when it was
// saved (unix nanoseconds).
type Revision struct {
	Saved int64     `json:"saved" pb:"1"`
	Doc   *Document `json:"doc,omitempty" pb:"2"`
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
	// Image is an inline image: where it came from and how it is shown.
	Image *ImageItem `json:"image,omitempty" pb:"10"`
	// Media is an embedded audio or video clip with its captions and transcript (roadmap 68). The editor
	// has no player: it opens a clip as text (MediaText) and the exports write it so.
	Media *MediaItem `json:"media,omitempty" pb:"11"`
	// Equation is a formula with its text alternative (roadmap 78).
	Equation *EquationItem `json:"equation,omitempty" pb:"12"`
	// Form is a form field: its properties and what was written in it.
	Form *FormFieldItem `json:"form,omitempty" pb:"13"`
}

// Words is what the exports and the search write for the field: its label and what is in it.
func (f FormFieldItem) Words() string {
	label := strings.TrimSpace(f.Label)
	if f.Kind == "checkbox" {
		box := "[ ]"
		if f.Value == "true" {
			box = "[x]"
		}
		return strings.TrimSpace(box + " " + label)
	}
	if label == "" {
		return f.Value
	}
	return label + ": " + f.Value
}

// FormFieldItem mirrors canvas.FormFieldObject: a field of a form, what it takes (Kind: text, textarea, email, number,
// phone, url, date, checkbox or select), how it is shown and checked, and the Value written in it. Locked keeps its
// properties from being changed.
type FormFieldItem struct {
	Kind        string   `json:"kind" pb:"1"`
	Name        string   `json:"name,omitempty" pb:"2"`
	Label       string   `json:"label,omitempty" pb:"3"`
	Placeholder string   `json:"placeholder,omitempty" pb:"4"`
	Pattern     string   `json:"pattern,omitempty" pb:"5"`
	Required    bool     `json:"required,omitempty" pb:"6"`
	Options     []string `json:"options,omitempty" pb:"7"`
	Value       string   `json:"value,omitempty" pb:"8"`
	Locked      bool     `json:"locked,omitempty" pb:"9"`
	// LockKey and LockSig are the seal of a lock (package formseal): the public key that locked the field and its
	// signature over the properties, both base64; empty for a lock without a key.
	LockKey string `json:"lockKey,omitempty" pb:"10"`
	LockSig string `json:"lockSig,omitempty" pb:"11"`
}

// ImageItem is an inline image. URL is what to fetch it from (a data: URL
// holds the bitmap itself); Width and Height are its displayed size.
type ImageItem struct {
	URL    string  `json:"url" pb:"1"`
	Width  float64 `json:"width,omitempty" pb:"2"`
	Height float64 `json:"height,omitempty" pb:"3"`
	Alpha  float64 `json:"alpha,omitempty" pb:"4"`
	// ClipLeft..ClipBottom are the pixels cropped from each side.
	ClipLeft   float64 `json:"clipLeft,omitempty" pb:"5"`
	ClipTop    float64 `json:"clipTop,omitempty" pb:"6"`
	ClipRight  float64 `json:"clipRight,omitempty" pb:"7"`
	ClipBottom float64 `json:"clipBottom,omitempty" pb:"8"`
	// Alt describes the image; Decorative says it needs no description.
	Alt        string `json:"alt,omitempty" pb:"9"`
	Decorative bool   `json:"decorative,omitempty" pb:"10"`
	// LongDesc is a longer description of a diagram or chart than Alt can hold (roadmap 78).
	LongDesc string `json:"longDesc,omitempty" pb:"11"`
	// Legend is the visible caption under the picture (canvas.Image.Legend).
	Legend string `json:"legend,omitempty" pb:"12"`
	// Credit and SourceURL are the author, licence and page of a picture found in an image repository (canvas.Image).
	Credit    string `json:"credit,omitempty" pb:"13"`
	SourceURL string `json:"sourceUrl,omitempty" pb:"14"`
	// Diagram is the kind of a picture drawn by the diagram add-on from the description in LongDesc ("" for another picture).
	Diagram string `json:"diagram,omitempty" pb:"15"`
}

// EquationItem is a formula in the linear notation of package export (x^2, frac{a}{b}, sqrt{x}) and its text
// alternative, what a person would read aloud. The alternative is required: a document with an equation
// that has none is a finding of package a11ycheck, and every export writes the alternative as the text of
// the equation (the HTML export also writes MathML).
type EquationItem struct {
	Formula string `json:"formula" pb:"1"`
	Alt     string `json:"alt,omitempty" pb:"2"`
	// Inline puts the formula in its line of text instead of on a line of its own.
	Inline bool `json:"inline,omitempty" pb:"3"`
}

// RuleItem mirrors canvas.Rule.
type RuleItem struct {
	Color     string  `json:"color,omitempty" pb:"1"`
	Thickness float64 `json:"thickness,omitempty" pb:"2"`
}

// TableItem is the text of a table in the flow, row by row.
type TableItem struct {
	Rows []TableRow `json:"rows,omitempty" pb:"1"`
	// Merges are blocks of cells shown as one; Styles are per-cell formatting.
	Merges []TableMerge     `json:"merges,omitempty" pb:"2"`
	Styles []TableCellStyle `json:"styles,omitempty" pb:"3"`
	// Fields are the cells that are form fields (build tag exp_forms in the showcase); the cell's row text is the
	// field's words, so a reader that does not know fields still sees them.
	Fields []TableCellField `json:"fields,omitempty" pb:"4"`
	// ColWidths and RowHeights are the sizes set by dragging a grip (0: the table's own).
	ColWidths  []float64 `json:"colWidths,omitempty" pb:"5"`
	RowHeights []float64 `json:"rowHeights,omitempty" pb:"6"`
}

// TableCellField is the form field in the cell at (Row, Col).
type TableCellField struct {
	Row   int            `json:"row" pb:"1"`
	Col   int            `json:"col" pb:"2"`
	Field *FormFieldItem `json:"field,omitempty" pb:"3"`
}

// TableMerge is a block of cells shown as one: the cell at (Row, Col) spans
// Rows x Cols cells (see canvas.Merge).
type TableMerge struct {
	Row  int `json:"row" pb:"1"`
	Col  int `json:"col" pb:"2"`
	Rows int `json:"rows" pb:"3"`
	Cols int `json:"cols" pb:"4"`
}

// TableCellStyle is the formatting of the cell at (Row, Col) (see
// canvas.CellStyle; Align is the canvas.Align number).
type TableCellStyle struct {
	Row        int    `json:"row" pb:"1"`
	Col        int    `json:"col" pb:"2"`
	Background string `json:"background,omitempty" pb:"3"`
	Color      string `json:"color,omitempty" pb:"4"`
	Bold       bool   `json:"bold,omitempty" pb:"5"`
	Italic     bool   `json:"italic,omitempty" pb:"6"`
	Align      int    `json:"align,omitempty" pb:"7"`
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
	// Style and Depth are the look of a table of contents (canvas.TOCObject): "" dotted, "plain"; the deepest
	// heading level listed, 0 for all.
	Style string `json:"style,omitempty" pb:"2"`
	Depth int    `json:"depth,omitempty" pb:"3"`
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
	Lang        string  `json:"lang,omitempty" pb:"18"`
	Proposal    string  `json:"proposal,omitempty" pb:"19"`
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
	// CodeLang is the language of a code block (a paragraph of the Code style), a short lower-case name.
	CodeLang string `json:"codeLang,omitempty" pb:"12"`
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
				out = append(out, Item{TOC: &Block{Title: o.Title, Style: o.Style, Depth: o.Depth}})
			case *canvas.IndexObject:
				out = append(out, Item{Index: &Block{Title: o.Title}})
			case *canvas.Rule:
				out = append(out, Item{Rule: &RuleItem{Color: o.Color, Thickness: o.Thickness}})
			case *canvas.Image:
				if src, ok := o.Src.(canvas.URLSource); ok && src.URL() != "" {
					out = append(out, Item{Image: &ImageItem{URL: src.URL(), Width: o.W, Height: o.H, Alpha: o.Alpha,
						ClipLeft: o.Clip.Left, ClipTop: o.Clip.Top, ClipRight: o.Clip.Right, ClipBottom: o.Clip.Bottom,
						Alt: o.Alt, Decorative: o.Decorative, LongDesc: o.LongDesc, Legend: o.Legend, Credit: o.Credit, SourceURL: o.SourceURL, Diagram: o.Diagram}})
				}
			case *canvas.FormFieldObject:
				out = append(out, Item{Form: formItem(o)})
			case *canvas.EquationObject:
				out = append(out, Item{Equation: &EquationItem{Formula: o.Formula, Alt: o.Alt, Inline: o.Inline}})
			case *canvas.MediaObject:
				out = append(out, Item{Media: &MediaItem{URL: o.URL, Kind: o.Kind, Title: o.Title, Captions: o.Captions, CaptionsURL: o.CaptionsURL,
					Transcript: o.Transcript, Width: o.Width, Height: o.Height}})
			case *ImagePlaceholder:
				c := o.Item
				out = append(out, Item{Image: &c})
			case *canvas.TableObject:
				if o == nil || o.View == nil || o.View.Table == nil {
					continue // nothing to save
				}
				o.View.Table.SyncFields()
				t := &TableItem{ColWidths: slices.Clone(o.View.Table.ColWidths), RowHeights: slices.Clone(o.View.Table.RowHeights)}
				for _, row := range o.View.Table.Cells {
					t.Rows = append(t.Rows, TableRow{Cells: append([]string(nil), row...)})
				}
				for _, m := range o.View.Table.Merges {
					t.Merges = append(t.Merges, TableMerge{Row: m.Row, Col: m.Col, Rows: m.Rows, Cols: m.Cols})
				}
				for pos, st := range o.View.Table.Styles { // in reading order, so the file is the same every time
					t.Styles = append(t.Styles, TableCellStyle{Row: pos.Row, Col: pos.Col, Background: st.Background, Color: st.Color,
						Bold: st.Bold, Italic: st.Italic, Align: int(st.Align)})
				}
				slices.SortFunc(t.Styles, func(a, b TableCellStyle) int {
					if a.Row != b.Row {
						return a.Row - b.Row
					}
					return a.Col - b.Col
				})
				for pos, f := range o.View.Table.Fields {
					t.Fields = append(t.Fields, TableCellField{Row: pos.Row, Col: pos.Col, Field: formItem(f)})
				}
				slices.SortFunc(t.Fields, func(a, b TableCellField) int {
					if a.Row != b.Row {
						return a.Row - b.Row
					}
					return a.Col - b.Col
				})
				out = append(out, Item{Table: t})
			}
		}
	}
	return out
}

func formItem(o *canvas.FormFieldObject) *FormFieldItem {
	return &FormFieldItem{Kind: string(o.Kind), Name: o.Name, Label: o.Label, Placeholder: o.Placeholder,
		Pattern: o.Pattern, Required: o.Required, Options: slices.Clone(o.Options), Value: o.Value, Locked: o.Locked, LockKey: o.LockKey, LockSig: o.LockSig}
}

func formObject(ff FormFieldItem) *canvas.FormFieldObject {
	return &canvas.FormFieldObject{Kind: canvas.FormKind(ff.Kind), Name: ff.Name, Label: ff.Label,
		Placeholder: ff.Placeholder, Pattern: ff.Pattern, Required: ff.Required, Options: slices.Clone(ff.Options), Value: ff.Value, Locked: ff.Locked, LockKey: ff.LockKey, LockSig: ff.LockSig}
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
			out = append(out, canvas.Inline{Object: &canvas.TOCObject{Title: it.TOC.Title, Style: it.TOC.Style, Depth: it.TOC.Depth}, Block: true})
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
			table := &canvas.Table{Cells: cells}
			for _, m := range it.Table.Merges { // a merge that does not fit the grid is dropped
				if m.Row >= 0 && m.Col >= 0 && m.Rows >= 1 && m.Cols >= 1 && m.Row+m.Rows <= len(cells) && m.Col+m.Cols <= cols && (m.Rows > 1 || m.Cols > 1) {
					table.MergeRange(m.Row, m.Col, m.Row+m.Rows-1, m.Col+m.Cols-1)
				}
			}
			for _, st := range it.Table.Styles {
				table.SetCellStyle(st.Row, st.Col, canvas.CellStyle{Background: st.Background, Color: st.Color, Bold: st.Bold, Italic: st.Italic, Align: canvas.Align(st.Align)})
			}
			for c, w := range it.Table.ColWidths { // sizes set by hand; ones for columns or rows that are not there are dropped
				table.SetColumnWidth(c, w)
			}
			for r, h := range it.Table.RowHeights {
				table.SetRowHeight(r, h)
			}
			for _, cf := range it.Table.Fields { // a field whose cell is not in the grid is dropped
				if cf.Field != nil && table.InRange(cf.Row, cf.Col) && !table.Covered(cf.Row, cf.Col) && canvas.FormKind(cf.Field.Kind).Valid() {
					table.SetField(cf.Row, cf.Col, formObject(*cf.Field))
				}
			}
			out = append(out, canvas.Inline{Object: canvas.NewTableObject(table), Block: true})
		case it.Image != nil:
			out = append(out, canvas.Inline{Object: imageObject(*it.Image)})
		case it.Form != nil:
			out = append(out, canvas.Inline{Object: formObject(*it.Form)})
		case it.Equation != nil:
			out = append(out, canvas.Inline{Object: &canvas.EquationObject{Formula: it.Equation.Formula, Alt: it.Equation.Alt, Inline: it.Equation.Inline}, Block: !it.Equation.Inline})
		case it.Media != nil:
			// the editor has no player: a clip is a card with its kind and title (canvas.MediaObject), and the
			// text that stands for it is edited in the Pictures tab
			m := it.Media
			out = append(out, canvas.Inline{Object: &canvas.MediaObject{URL: m.URL, Kind: m.Kind, Title: m.Title, Captions: m.Captions,
				CaptionsURL: m.CaptionsURL, Transcript: m.Transcript, Width: m.Width, Height: m.Height}, Block: true})
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
	// Keep the sets themselves when there are some (something may hold them,
	// and a shared document gave them an ID prefix); fill them in place.
	if f.Comments != nil {
		f.Comments.ReplaceAll(ToComments(d.Comments).List())
	} else {
		f.Comments = ToComments(d.Comments)
	}
	if f.Footnotes != nil {
		f.Footnotes.ReplaceAll(ToFootnotes(d.Footnotes).List())
	} else {
		f.Footnotes = ToFootnotes(d.Footnotes)
	}
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
	d.Meta = slices.Clone(d.Meta) // Document is passed by value: never write through to the caller's slice
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
func FromFonts(list []fontdata.Font) []FontRef {
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
func ToFonts(refs []FontRef) []fontdata.Font {
	var out []fontdata.Font
	for _, r := range refs {
		f := fontdata.Font{Family: r.Family, Source: r.Source, Category: r.Category}
		for _, v := range r.Variants {
			f.Variants = append(f.Variants, fontdata.Variant{Weight: v.Weight, Italic: v.Italic, Style: v.Style, URL: v.URL, Key: v.Key})
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

// ImageLoader, when set, turns an image's URL into a bitmap source the moment
// a document is opened. A browser app that has already decoded the image
// (canvas.LoadImage is asynchronous) returns it from its cache; returning nil
// means "not available yet" and the document gets an ImagePlaceholder.
var ImageLoader func(url string) canvas.ImageSource

// imageObject makes the inline object for a saved image.
func imageObject(it ImageItem) canvas.InlineObject {
	if ImageLoader != nil {
		if src := ImageLoader(it.URL); src != nil {
			img := canvas.NewImage(src, 0, 0)
			img.Placement = canvas.PlaceInline
			if it.Width > 0 && it.Height > 0 {
				img.W, img.H = it.Width, it.Height
			}
			if it.Alpha > 0 {
				img.Alpha = it.Alpha
			}
			img.Clip = canvas.Insets{Left: it.ClipLeft, Top: it.ClipTop, Right: it.ClipRight, Bottom: it.ClipBottom}
			img.Alt, img.Decorative, img.LongDesc, img.Legend = it.Alt, it.Decorative, it.LongDesc, it.Legend
			img.Credit, img.SourceURL, img.Diagram = it.Credit, it.SourceURL, it.Diagram
			return img
		}
	}
	return &ImagePlaceholder{Item: it}
}

// ImagePlaceholder stands in for a saved image whose bitmap is not loaded: a
// grey box of the right size that saves back as the same image, so opening
// and saving a document does not lose it.
type ImagePlaceholder struct{ Item ImageItem }

// Measure implements canvas.InlineObject.
func (p *ImagePlaceholder) Measure(_ canvas.Painter, maxW, maxH float64) (w, h float64) {
	w, h = p.Item.Width, p.Item.Height
	if w <= 0 || h <= 0 {
		w, h = 96, 64
	}
	if w > maxW && maxW > 0 {
		h, w = h*maxW/w, maxW
	}
	if h > maxH && maxH > 0 {
		w, h = w*maxH/h, maxH
	}
	return w, h
}

// DrawAt implements canvas.InlineObject.
func (p *ImagePlaceholder) DrawAt(pt canvas.Painter, x, y float64) {
	w, h := p.Measure(pt, 0, 0)
	pt.SetFillStyle("#e6e8eb")
	pt.FillRect(x, y, w, h)
}

// TagsKey is the Meta key a document's tags are kept under.
const TagsKey = "tags"

// Tags returns the document's tags, in the order they were set.
func (d Document) Tags() []string {
	v, _ := d.Get(TagsKey)
	var out []string
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SetTags replaces the document's tags: trimmed, without commas or empty ones,
// and without repeats (compared ignoring case).
func (d *Document) SetTags(tags []string) {
	seen := map[string]bool{}
	var clean []string
	for _, t := range tags {
		t = strings.TrimSpace(strings.ReplaceAll(t, ",", " "))
		if k := strings.ToLower(t); t != "" && !seen[k] {
			seen[k] = true
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == TagsKey })
		return
	}
	d.Set(TagsKey, strings.Join(clean, ","))
}

// HasTag reports whether the document has the tag (ignoring case).
func (d Document) HasTag(tag string) bool {
	for _, t := range d.Tags() {
		if strings.EqualFold(t, strings.TrimSpace(tag)) {
			return true
		}
	}
	return false
}

// SourceFormatKey is the Meta key of a document that is a copy of a file in another format, to be read (a text format opened
// as it is: "json", "csv", "yaml"...). The editor opens such a document for reading and says it is a copy; saving it
// is saving the copy, and the key is not kept.
const SourceFormatKey = "source.format"

// SourceFormat returns the format the document is a reading copy of, or "".
func (d Document) SourceFormat() string { v, _ := d.Get(SourceFormatKey); return v }

// IDKey and NameKey are the Meta keys a document's identity is kept under: its
// ID, a UUID that never changes (two documents may share a name, never an ID),
// and its Name in a document list.
const (
	IDKey   = "id"
	NameKey = "name"
)

// NewID returns a new random (version 4) UUID.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("docfile: no randomness: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ValidID reports whether s is a UUID as NewID writes it.
func ValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case (c < '0' || c > '9') && (c < 'a' || c > 'f'):
			return false
		}
	}
	return true
}

// ID returns the document's ID, "" for one that has none yet.
func (d Document) ID() string { v, _ := d.Get(IDKey); return v }

// SetID gives the document its ID.
func (d *Document) SetID(id string) { d.Set(IDKey, id) }

// Name returns the document's name in a list, "" if it has none.
func (d Document) Name() string { v, _ := d.Get(NameKey); return v }

// SetName names the document.
func (d *Document) SetName(name string) { d.Set(NameKey, name) }

// TrashedKey is the Meta key holding when the document was moved to the trash
// (unix nanoseconds); a document that is not in the trash has none.
const TrashedKey = "trashed"

// Trashed returns when the document was trashed, 0 if it is not in the trash.
func (d Document) Trashed() int64 {
	v, _ := d.Get(TrashedKey)
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// SetTrashed records when the document was trashed; 0 takes it out of the trash.
func (d *Document) SetTrashed(ns int64) {
	if ns == 0 {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == TrashedKey })
		return
	}
	d.Set(TrashedKey, strconv.FormatInt(ns, 10))
}

// ProtectionKey is the Meta key holding a document's protection level, and ProtectionE2EE
// the one level that is written: a confidential document, whose real name and tags live
// here, inside the document, while a catalog lists a placeholder. A document with no such
// key is protected by the server only.
const (
	ProtectionKey  = "protection"
	ProtectionE2EE = "e2ee"
)

// Protection returns the document's protection level: ProtectionE2EE, or "" for the
// default.
func (d Document) Protection() string {
	if v, _ := d.Get(ProtectionKey); v == ProtectionE2EE {
		return v
	}
	return ""
}

// SetProtection records the protection level; anything but ProtectionE2EE takes the key
// away.
func (d *Document) SetProtection(level string) {
	if level != ProtectionE2EE {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == ProtectionKey })
		return
	}
	d.Set(ProtectionKey, level)
}

// FolderKey is the Meta key holding the ID of the folder a document is in; a
// document at the top level has none.
const FolderKey = "folder"

// Folder returns the ID of the folder the document is in, "" at the top level.
func (d Document) Folder() string { v, _ := d.Get(FolderKey); return v }

// SetFolder puts the document in a folder; "" takes it to the top level.
func (d *Document) SetFolder(id string) {
	if id == "" {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == FolderKey })
		return
	}
	d.Set(FolderKey, id)
}

// LangKey is the Meta key a document's language is kept under, as a BCP 47 tag ("fr", "pt-BR").
const LangKey = "lang"

// Language is the document's language, or "" when none is set (or what is set is not a language tag).
func (d Document) Language() string {
	if v, _ := d.Get(LangKey); v != "" && canvas.ValidLang(v) {
		return v
	}
	return ""
}

// ThemeKey is the Meta key a document's theme is kept under: design-token CSS (package tokens), which the
// HTML export writes into the page so the file looks like the editor's theme.
const ThemeKey = "theme"

// Theme is the document's theme as design-token CSS, "" when it has none.
func (d Document) Theme() string {
	v, _ := d.Get(ThemeKey)
	return v
}

// SetTheme sets the document's theme; "" removes it. The text is not checked here: the HTML export
// refuses anything that is not a plain :root block of custom properties, and package tokens makes it.
func (d *Document) SetTheme(css string) {
	if css == "" {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == ThemeKey })
		return
	}
	d.Set(ThemeKey, css)
}

// SetLanguage sets the document's language; "" removes it. A tag that is not valid is ignored.
func (d *Document) SetLanguage(tag string) {
	if !canvas.ValidLang(tag) {
		return
	}
	if tag == "" {
		d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == LangKey })
		return
	}
	d.Set(LangKey, tag)
}
