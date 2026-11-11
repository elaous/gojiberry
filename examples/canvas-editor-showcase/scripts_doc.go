//go:build js && wasm

package main

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/script"
)

type flowDoc struct {
	u      *ui
	flow   func() *canvas.TextFlow
	redraw func()
}

var errNotEditing = errors.New("the document is not open for editing (switch to edit mode)")

func (d flowDoc) flowOrNil() *canvas.TextFlow {
	if d.flow == nil {
		return nil
	}
	return d.flow()
}

func (d flowDoc) editable() (*canvas.TextFlow, error) {
	f := d.flowOrNil()
	if f == nil || d.u.stateRef == nil || !d.u.stateRef.Role.CanEdit() || d.u.stateRef.Mode != canvas.ModeEdit {
		return nil, errNotEditing
	}
	return f, nil
}

func (d flowDoc) Markdown() string {
	f := d.flowOrNil()
	if f == nil {
		return ""
	}
	_, data, _, err := d.u.transfer.Export("markdown", docfile.FromFlow(d.u.title, f))
	if err != nil {
		return ""
	}
	return string(data)
}

func (d flowDoc) SetMarkdown(md string) error {
	f, err := d.editable()
	if err != nil {
		return err
	}
	doc, err := d.u.transfer.Import("markdown", "", []byte(md))
	if err != nil {
		return err
	}
	n := utf8.RuneCountInString(f.EditText())
	f.Replace([]canvas.Edit{{From: 0, To: n, Items: docfile.ToItems(doc.Content)}})
	f.SetCursors([]canvas.Cursor{canvas.At(0)})
	d.redraw()
	return nil
}

func (d flowDoc) Selection() script.Selection {
	f := d.flowOrNil()
	if f == nil {
		return script.Selection{}
	}
	from, to := f.Selection()
	return script.Selection{From: from, To: to, Text: f.SelectedText()}
}

func (d flowDoc) SetSelection(from, to int) error {
	f, err := d.editable()
	if err != nil {
		return err
	}
	n := utf8.RuneCountInString(f.EditText())
	if from < 0 || to < 0 || from > n || to > n {
		return errors.New("that range is outside the document")
	}
	f.SetCursors([]canvas.Cursor{{Anchor: from, Caret: to}})
	d.redraw()
	return nil
}

func (d flowDoc) Format() script.FormatInfo {
	f := d.flowOrNil()
	if f == nil {
		return script.FormatInfo{}
	}
	from, to := f.Selection()
	at := from
	if from == to && from > 0 {
		at = from - 1 // the character before the caret is what is typed on
	}
	for _, it := range f.Slice(at, at+1) {
		if ch, ok := it.(canvas.Chars); ok {
			fm := ch.Format
			return script.FormatInfo{Bold: fm.FontWeight >= 700, Italic: strings.Contains(fm.FontStyle, "italic"), Underline: fm.Underline, Strike: fm.Strike,
				Color: fm.Color, Background: fm.Background, Family: fm.FontFamily, Size: fm.FontSize}
		}
	}
	return script.FormatInfo{}
}

func (d flowDoc) SetFormat(p script.FormatPatch) error {
	f, err := d.editable()
	if err != nil {
		return err
	}
	f.ApplyFormat(func(fm *canvas.Format) {
		if p.Bold != nil {
			fm.FontWeight = map[bool]int{true: 700, false: 0}[*p.Bold]
		}
		if p.Italic != nil {
			fm.FontStyle = map[bool]string{true: "italic", false: ""}[*p.Italic]
		}
		if p.Underline != nil {
			fm.Underline = *p.Underline
		}
		if p.Strike != nil {
			fm.Strike = *p.Strike
		}
		if p.Color != nil {
			fm.Color = *p.Color
		}
		if p.Background != nil {
			fm.Background = *p.Background
		}
		if p.Family != nil {
			fm.FontFamily = *p.Family
		}
		if p.Size != nil {
			fm.FontSize = *p.Size
		}
	})
	d.redraw()
	return nil
}

func paragraphStyleNames(f *canvas.TextFlow) []string {
	defs := f.Styles
	if len(defs) == 0 {
		defs = canvas.DefaultStyles()
	}
	var out []string
	for _, s := range defs {
		if s.Paragraph {
			out = append(out, s.Name)
		}
	}
	return out
}

func (d flowDoc) Styles() []string {
	if f := d.flowOrNil(); f != nil {
		return paragraphStyleNames(f)
	}
	return nil
}

func (d flowDoc) Style() string {
	f := d.flowOrNil()
	if f == nil {
		return ""
	}
	name := ""
	f.ApplyParagraph(func(p *canvas.ParaFormat) { name = p.Style }) // reads the paragraph at the caret, changes nothing
	return name
}

func (d flowDoc) SetStyle(name string) error {
	f, err := d.editable()
	if err != nil {
		return err
	}
	for _, n := range paragraphStyleNames(f) {
		if n == name {
			f.ApplyParagraph(func(p *canvas.ParaFormat) { p.Style = name })
			d.redraw()
			return nil
		}
	}
	return errors.New("no such paragraph style")
}

func (d flowDoc) Images() []script.ImageInfo {
	var out []script.ImageInfo
	for i, r := range documentImages(d.flowOrNil()) {
		out = append(out, script.ImageInfo{Index: i, Alt: r.img.Alt, Legend: r.img.Legend, Credit: r.img.Credit, Decorative: r.img.Decorative, Width: r.img.W, Height: r.img.H})
	}
	return out
}

func (d flowDoc) SetImage(index int, p script.ImagePatch) error {
	f, err := d.editable()
	if err != nil {
		return err
	}
	imgs := documentImages(f)
	if index < 0 || index >= len(imgs) {
		return errors.New("no such picture")
	}
	im := imgs[index].img
	if p.Alt != nil {
		im.Alt = *p.Alt
	}
	if p.Legend != nil {
		im.Legend = *p.Legend
	}
	if p.Decorative != nil {
		im.Decorative = *p.Decorative
	}
	f.Touch()
	d.redraw()
	return nil
}

func (d flowDoc) Meta() script.Meta { return script.Meta{Title: d.u.title, Language: d.u.docLang} }

func (d flowDoc) SetMeta(p script.MetaPatch) error {
	if _, err := d.editable(); err != nil {
		return err
	}
	if p.Title != nil {
		d.u.title = strings.TrimSpace(*p.Title)
	}
	if p.Language != nil {
		d.u.docLang = *p.Language
	}
	d.redraw()
	return nil
}
