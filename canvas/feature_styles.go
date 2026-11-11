package canvas

// StyleHost is implemented by targets with named styles (a TextFlow).
type StyleHost interface {
	StyleByName(name string) (StyleDef, bool)
	DefineStyle(StyleDef)
}

var _ StyleHost = (*TextFlow)(nil)

// Styles applies named styles and clears formatting.
type Styles struct{}

const (
	CmdStyleNormal     Command = "style.normal"
	CmdStyleHeading1   Command = "style.heading-1"
	CmdStyleHeading2   Command = "style.heading-2"
	CmdStyleHeading3   Command = "style.heading-3"
	CmdStyleHeading4   Command = "style.heading-4"
	CmdStyleHeading5   Command = "style.heading-5"
	CmdStyleHeading6   Command = "style.heading-6"
	CmdStyleTitle      Command = "style.title"
	CmdStyleSubtitle   Command = "style.subtitle"
	CmdStyleQuote      Command = "style.quote"
	CmdStyleCode       Command = "style.code"
	CmdStyleEmphasis   Command = "style.emphasis"
	CmdStyleStrong     Command = "style.strong"
	CmdClearFormatting Command = "format.clear"
	CmdDefineParaStyle Command = "style.define-paragraph"
	CmdDefineCharStyle Command = "style.define-character"
)

func (Styles) Name() string { return "styles" }

func ensureStyle(h StyleHost, name string) bool {
	if _, ok := h.StyleByName(name); ok {
		return true
	}
	for _, d := range DefaultStyles() {
		if d.Name == name {
			h.DefineStyle(d)
			return true
		}
	}
	return false
}

func (Styles) Install(r *Registry) {
	para := func(cmd Command, title, chord, name string) {
		r.AddCommand(cmd, title, chord, func(c *CommandContext) bool {
			h, hok := c.Target.(StyleHost)
			pe, pok := c.Target.(ParagraphEditor)
			if !hok || !pok || (name != "" && !ensureStyle(h, name)) {
				return false
			}
			all := name != ""
			for _, f := range pe.ParagraphFormats() {
				all = all && f.Style == name
			}
			next := name
			if all {
				next = "" // applying a style again takes it off
			}
			pe.ApplyParagraph(func(p *ParaFormat) { p.Style = next })
			return true
		})
	}
	para(CmdStyleNormal, "Normal text", "Mod+Alt+0", "")
	para(CmdStyleHeading1, "Heading 1", "Mod+Alt+1", "Heading 1")
	para(CmdStyleHeading2, "Heading 2", "Mod+Alt+2", "Heading 2")
	para(CmdStyleHeading3, "Heading 3", "Mod+Alt+3", "Heading 3")
	para(CmdStyleHeading4, "Heading 4", "Mod+Alt+4", "Heading 4")
	para(CmdStyleHeading5, "Heading 5", "Mod+Alt+5", "Heading 5")
	para(CmdStyleHeading6, "Heading 6", "Mod+Alt+6", "Heading 6")
	para(CmdStyleTitle, "Title", "", "Title")
	para(CmdStyleSubtitle, "Subtitle", "", "Subtitle")
	para(CmdStyleQuote, "Quote", "", "Quote")
	para(CmdStyleCode, "Code block", "", "Code")

	char := func(cmd Command, title, name string) {
		r.AddCommand(cmd, title, "", func(c *CommandContext) bool {
			h, hok := c.Target.(StyleHost)
			fe, fok := c.Target.(FormatEditor)
			if !hok || !fok || !ensureStyle(h, name) {
				return false
			}
			next := name
			if fe.CommonFormat().Style == name {
				next = ""
			}
			return fe.ApplyFormat(func(f *Format) { f.Style = next })
		})
	}
	char(CmdStyleEmphasis, "Emphasis", "Emphasis")
	char(CmdStyleStrong, "Strong", "Strong")

	r.AddCommand(CmdClearFormatting, "Clear formatting", "Mod+\\", func(c *CommandContext) bool {
		changed := false
		if fe, ok := c.Target.(FormatEditor); ok {
			changed = fe.ApplyFormat(func(f *Format) { *f = Format{Link: f.Link} }) || changed
		}
		if pe, ok := c.Target.(ParagraphEditor); ok {
			changed = pe.ApplyParagraph(func(p *ParaFormat) { *p = ParaFormat{List: p.List} }) || changed
		}
		return changed
	})

	define := func(cmd Command, title string, paragraph bool) {
		r.AddCommand(cmd, title, "", func(c *CommandContext) bool {
			h, hok := c.Target.(StyleHost)
			fe, fok := c.Target.(FormatEditor)
			if !hok || !fok || c.Prompt == nil {
				return false
			}
			name, ok := c.Prompt("Name for the new style", "")
			if !ok || name == "" {
				return false
			}
			def := StyleDef{Name: name, Format: fe.CommonFormat(), Paragraph: paragraph}
			def.Format.Style, def.Format.Link = "", ""
			if pe, ok := c.Target.(ParagraphEditor); ok && paragraph {
				if fs := pe.ParagraphFormats(); len(fs) > 0 {
					def.Para = fs[0]
					def.Para.Style, def.Para.List = "", ListStyle{}
				}
			}
			h.DefineStyle(def)
			// tag the text so editing the style later restyles it
			if paragraph {
				if pe, ok := c.Target.(ParagraphEditor); ok {
					pe.ApplyParagraph(func(p *ParaFormat) { *p = ParaFormat{List: p.List, Style: name} })
				}
			}
			return true
		})
	}
	define(CmdDefineParaStyle, "New paragraph style from here…", true)
	define(CmdDefineCharStyle, "New character style from the selection…", false)
}
