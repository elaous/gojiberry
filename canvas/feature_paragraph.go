package canvas

// Paragraphs are the paragraph commands of a word processor, for targets
// with paragraph formats (a TextFlow):
//
//   - alignment: Ctrl/Cmd+L, E, R (left, centre, right);
//   - indents: Ctrl/Cmd+] and [ indent and outdent (a list item changes
//     level instead); para.first-line toggles a first-line indent;
//   - spacing: para.space-before and para.space-after toggle a 12px gap;
//     para.line-spacing cycles 1, 1.15, 1.5, 2;
//   - lists: Ctrl/Cmd+Shift+8 bullets, +7 numbers, +9 check boxes (each
//     toggles; Tab and Shift+Tab nest an item); list.cycle-bullet and
//     list.cycle-number-format change the bullet character and the
//     number style ("1." "1)" "(1)" "a." "i." "I." "A.");
//   - para.quote toggles a block quote, para.drop-cap a three-line
//     initial letter.
//
// Enter in a list continues it, and on an empty item ends it.
type Paragraphs struct{}

// Command names.
const (
	CmdAlignLeft         Command = "para.align-left"
	CmdAlignCenter       Command = "para.align-center"
	CmdAlignRight        Command = "para.align-right"
	CmdIndent            Command = "para.indent"
	CmdOutdent           Command = "para.outdent"
	CmdFirstLineIndent   Command = "para.first-line"
	CmdSpaceBefore       Command = "para.space-before"
	CmdSpaceAfter        Command = "para.space-after"
	CmdLineSpacing       Command = "para.line-spacing"
	CmdQuote             Command = "para.quote"
	CmdDropCap           Command = "para.drop-cap"
	CmdBulletList        Command = "list.bullets"
	CmdNumberedList      Command = "list.numbers"
	CmdCheckList         Command = "list.checks"
	CmdCycleBullet       Command = "list.cycle-bullet"
	CmdCycleNumberFormat Command = "list.cycle-number-format"
)

// Name implements Feature.
func (Paragraphs) Name() string { return "paragraphs" }

// ParagraphGap is the space the space-before and space-after commands
// toggle.
const ParagraphGap = 12.0

// Install implements Feature.
func (Paragraphs) Install(r *Registry) {
	set := func(name Command, title, chord string, fn func(*ParaFormat)) {
		r.AddCommand(name, title, chord, func(c *CommandContext) bool {
			pe, ok := c.Target.(ParagraphEditor)
			if !ok {
				return false
			}
			return pe.ApplyParagraph(fn)
		})
	}
	// toggle makes a command that switches on, or -- when every touched
	// paragraph already has it -- off.
	toggle := func(name Command, title, chord string, has func(ParaFormat) bool, on, off func(*ParaFormat)) {
		r.AddCommand(name, title, chord, func(c *CommandContext) bool {
			pe, ok := c.Target.(ParagraphEditor)
			if !ok {
				return false
			}
			all := true
			for _, f := range pe.ParagraphFormats() {
				all = all && has(f)
			}
			if all {
				return pe.ApplyParagraph(off)
			}
			return pe.ApplyParagraph(on)
		})
	}
	align := func(a Align) func(*ParaFormat) { return func(p *ParaFormat) { p.Align = a } }
	set(CmdAlignLeft, "Align left", "Mod+L", align(AlignLeft))
	set(CmdAlignCenter, "Align centre", "Mod+E", align(AlignCenter))
	set(CmdAlignRight, "Align right", "Mod+R", align(AlignRight))
	set(CmdIndent, "Increase indent", "Mod+]", func(p *ParaFormat) {
		if p.List.Kind != ListNone {
			p.List.Level++
		} else {
			p.IndentLeft += ListIndent
		}
	})
	set(CmdOutdent, "Decrease indent", "Mod+[", func(p *ParaFormat) {
		if p.List.Kind != ListNone {
			p.List.Level = max(p.List.Level-1, 0)
		} else {
			p.IndentLeft = max(p.IndentLeft-ListIndent, 0)
		}
	})
	toggle(CmdFirstLineIndent, "First-line indent", "", func(p ParaFormat) bool { return p.FirstLine > 0 },
		func(p *ParaFormat) { p.FirstLine = ListIndent }, func(p *ParaFormat) { p.FirstLine = 0 })
	toggle(CmdSpaceBefore, "Space before paragraph", "", func(p ParaFormat) bool { return p.SpaceBefore > 0 },
		func(p *ParaFormat) { p.SpaceBefore = ParagraphGap }, func(p *ParaFormat) { p.SpaceBefore = 0 })
	toggle(CmdSpaceAfter, "Space after paragraph", "", func(p ParaFormat) bool { return p.SpaceAfter > 0 },
		func(p *ParaFormat) { p.SpaceAfter = ParagraphGap }, func(p *ParaFormat) { p.SpaceAfter = 0 })
	set(CmdLineSpacing, "Cycle line spacing", "", func(p *ParaFormat) {
		steps := []float64{0, 1.15, 1.5, 2}
		for i, s := range steps {
			if p.LineSpacing == s {
				p.LineSpacing = steps[(i+1)%len(steps)]
				return
			}
		}
		p.LineSpacing = 0
	})
	toggle(CmdQuote, "Block quote", "", func(p ParaFormat) bool { return p.QuoteBar != "" },
		func(p *ParaFormat) { p.QuoteBar, p.IndentLeft = "#c4c7cd", max(p.IndentLeft, 16) },
		func(p *ParaFormat) { p.QuoteBar, p.IndentLeft = "", 0 })
	toggle(CmdDropCap, "Drop cap", "", func(p ParaFormat) bool { return p.DropCap > 0 },
		func(p *ParaFormat) { p.DropCap = 3 }, func(p *ParaFormat) { p.DropCap = 0 })

	list := func(name Command, title, chord string, kind ListKind) {
		toggle(name, title, chord, func(p ParaFormat) bool { return p.List.Kind == kind },
			func(p *ParaFormat) { p.List.Kind = kind }, func(p *ParaFormat) { p.List = ListStyle{} })
	}
	list(CmdBulletList, "Bulleted list", "Mod+Shift+8", ListBullet)
	list(CmdNumberedList, "Numbered list", "Mod+Shift+7", ListOrdered)
	list(CmdCheckList, "Check list", "Mod+Shift+9", ListCheck)
	set(CmdCycleBullet, "Next bullet character", "", func(p *ParaFormat) {
		p.List.Kind = ListBullet
		cur := p.List.bullet()
		next := BulletChoices[0]
		for i, b := range BulletChoices {
			if b == cur {
				next = BulletChoices[(i+1)%len(BulletChoices)]
			}
		}
		p.List.Marker = next
	})
	set(CmdCycleNumberFormat, "Next number format", "", func(p *ParaFormat) {
		cur := 0
		for i, nf := range NumberFormats {
			marker := p.List.Marker
			if marker == "" {
				marker = "{n}."
			}
			if nf.Style == p.List.Number && nf.Marker == marker && p.List.Kind == ListOrdered {
				cur = (i + 1) % len(NumberFormats)
			}
		}
		p.List.Kind, p.List.Number, p.List.Marker = ListOrdered, NumberFormats[cur].Style, NumberFormats[cur].Marker
	})
}
