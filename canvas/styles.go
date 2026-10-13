package canvas

import "slices"

// Named styles. A StyleDef bundles a character Format and a paragraph
// ParaFormat under a name. Text gets a style by name: Format.Style for
// characters ("Emphasis") and ParaFormat.Style for a paragraph
// ("Heading 1"), whose Format applies to all of the paragraph's text.
// What a run looks like is, from weakest to strongest: the flow's base
// Style, the paragraph style's Format, the character style's Format, then
// the formatting set directly on the text -- so a style is a default that
// local formatting overrides, and redefining the style (TextFlow.
// DefineStyle) restyles every use of it.

// StyleDef is a named style.
type StyleDef struct {
	Name string
	// Format is the character formatting the style gives. For a paragraph
	// style it applies to the whole paragraph's text.
	Format Format
	// Para is the paragraph formatting a paragraph style gives (its own
	// Style field is ignored).
	Para ParaFormat
	// Paragraph says the style is applied to paragraphs; otherwise it is
	// a character style.
	Paragraph bool
}

// DefaultStyles is a small starter set: Normal, Title, Subtitle, Heading
// 1-3, Quote and Code (paragraph styles), Emphasis and Strong (character
// styles).
func DefaultStyles() []StyleDef {
	return []StyleDef{
		{Name: "Normal", Paragraph: true},
		{Name: "Title", Paragraph: true, Format: Format{Scale: 2.4, FontWeight: 700}, Para: ParaFormat{SpaceAfter: 6}},
		{Name: "Subtitle", Paragraph: true, Format: Format{Scale: 1.4, Color: "#5b616b"}, Para: ParaFormat{SpaceAfter: 10}},
		{Name: "Heading 1", Paragraph: true, Format: Format{Scale: 2, FontWeight: 700}, Para: ParaFormat{SpaceBefore: 12, SpaceAfter: 4}},
		{Name: "Heading 2", Paragraph: true, Format: Format{Scale: 1.6, FontWeight: 700}, Para: ParaFormat{SpaceBefore: 10, SpaceAfter: 4}},
		{Name: "Heading 3", Paragraph: true, Format: Format{Scale: 1.3, FontWeight: 700}, Para: ParaFormat{SpaceBefore: 8, SpaceAfter: 2}},
		{Name: "Quote", Paragraph: true, Format: Format{FontStyle: StyleItalic, Color: "#5b616b"}, Para: ParaFormat{IndentLeft: 16, QuoteBar: "#c4c7cd"}},
		{Name: "Code", Paragraph: true, Format: Format{FontFamily: "monospace", Background: "#eceef1"}},
		{Name: "Emphasis", Format: Format{FontStyle: StyleItalic}},
		{Name: "Strong", Format: Format{FontWeight: 700}},
	}
}

// mergeFormat lays over on top of under: a field of over that is set wins.
func mergeFormat(under, over Format) Format {
	out := under
	if over.FontFamily != "" {
		out.FontFamily = over.FontFamily
	}
	if over.FontSize != 0 {
		out.FontSize = over.FontSize
	}
	if over.Scale != 0 {
		out.Scale = over.Scale
	}
	if over.FontWeight != 0 {
		out.FontWeight = over.FontWeight
	}
	if over.FontStyle != "" {
		out.FontStyle = over.FontStyle
	}
	if over.Color != "" {
		out.Color = over.Color
	}
	if over.Background != "" {
		out.Background = over.Background
	}
	if over.Case != CaseNormal {
		out.Case = over.Case
	}
	if over.Link != "" {
		out.Link = over.Link
	}
	if over.Comment != "" {
		out.Comment = over.Comment
	}
	if over.Bookmark != "" {
		out.Bookmark = over.Bookmark
	}
	if over.Index != "" {
		out.Index = over.Index
	}
	if over.Lang != "" {
		out.Lang = over.Lang
	}
	if over.Proposal != "" {
		out.Proposal = over.Proposal
	}
	out.Underline = out.Underline || over.Underline
	out.Strike = out.Strike || over.Strike
	out.Superscript = out.Superscript || over.Superscript
	out.Subscript = out.Subscript || over.Subscript
	if over.Style != "" {
		out.Style = over.Style
	}
	return out
}

// mergePara lays over on top of under (a set field of over wins).
func mergePara(under, over ParaFormat) ParaFormat {
	out := under
	if over.Align != AlignLeft {
		out.Align = over.Align
	}
	if over.IndentLeft != 0 {
		out.IndentLeft = over.IndentLeft
	}
	if over.IndentRight != 0 {
		out.IndentRight = over.IndentRight
	}
	if over.FirstLine != 0 {
		out.FirstLine = over.FirstLine
	}
	if over.SpaceBefore != 0 {
		out.SpaceBefore = over.SpaceBefore
	}
	if over.SpaceAfter != 0 {
		out.SpaceAfter = over.SpaceAfter
	}
	if over.LineSpacing != 0 {
		out.LineSpacing = over.LineSpacing
	}
	if over.List.Kind != ListNone {
		out.List = over.List
	}
	if over.QuoteBar != "" {
		out.QuoteBar = over.QuoteBar
	}
	if over.DropCap != 0 {
		out.DropCap = over.DropCap
	}
	if over.CodeLang != "" {
		out.CodeLang = over.CodeLang
	}
	out.Style = over.Style
	return out
}

// StyleByName returns the flow's style called name.
func (f *TextFlow) StyleByName(name string) (StyleDef, bool) {
	for _, s := range f.Styles {
		if s.Name == name {
			return s, true
		}
	}
	return StyleDef{}, false
}

// DefineStyle adds a style or replaces the one of the same name; every
// use of it is restyled.
func (f *TextFlow) DefineStyle(def StyleDef) {
	for i, s := range f.Styles {
		if s.Name == def.Name {
			f.Styles[i] = def
			return
		}
	}
	f.Styles = append(f.Styles, def)
}

// styleNames lists the names of the flow's styles of one kind.
func (f *TextFlow) styleNames(paragraph bool) []string {
	var out []string
	for _, s := range f.Styles {
		if s.Paragraph == paragraph {
			out = append(out, s.Name)
		}
	}
	return slices.Clone(out)
}

// expand resolves fm's character style, over the paragraph style's
// format base.
func (f *TextFlow) expand(base, fm Format) Format {
	out := base
	if fm.Style != "" {
		if def, ok := f.StyleByName(fm.Style); ok && !def.Paragraph {
			out = mergeFormat(out, def.Format)
		}
	}
	return mergeFormat(out, fm)
}

// expandPara resolves pf's paragraph style: the style's paragraph and
// character formatting, under pf's own. The format is the paragraph
// style's character format ("" style: none).
func (f *TextFlow) expandPara(pf ParaFormat) (ParaFormat, Format) {
	if pf.Style == "" {
		return pf, Format{}
	}
	def, ok := f.StyleByName(pf.Style)
	if !ok || !def.Paragraph {
		return pf, Format{}
	}
	return mergePara(def.Para, pf), def.Format
}
