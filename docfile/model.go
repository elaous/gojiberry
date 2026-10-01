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

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
)

// Document is one saved document.
type Document struct {
	Title   string      `json:"title,omitempty" pb:"1"`
	Updated int64       `json:"updated,omitempty" pb:"2"` // unix nanoseconds
	Content []Item      `json:"content,omitempty" pb:"3"`
	Styles  []StyleDef  `json:"styles,omitempty" pb:"4"`
	Fonts   []FontRef   `json:"fonts,omitempty" pb:"5"`
	Meta    []MetaEntry `json:"meta,omitempty" pb:"6"`
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
	return Document{Title: title, Content: FromItems(f.ContentItems()), Styles: FromStyles(f.Styles)}
}

// Apply puts the document's content and styles into f, replacing what
// it had.
func (d Document) Apply(f *canvas.TextFlow) {
	f.ResetContent() // forget edits and history first: it also clears Items
	f.Source, f.Transform, f.Text = "", nil, ""
	f.Items = ToItems(d.Content)
	f.Styles = ToStyles(d.Styles)
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
