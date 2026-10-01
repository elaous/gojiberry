package canvas

import (
	"strings"
	"testing"
)

func styledFlow(items ...Item) (*TextFlow, *Stage) {
	f, st := paraFlow(items...)
	f.Styles = DefaultStyles()
	return f, st
}

func TestParagraphAndCharacterStylesLayOut(t *testing.T) {
	f, _ := styledFlow(
		ParaFormat{Style: "Heading 1"}, Chars{Text: "Title\n"},
		Chars{Text: "plain "}, Chars{Text: "emph", Format: Format{Style: "Emphasis"}},
		Chars{Text: " and "}, Chars{Text: "strong", Format: Format{Style: "Strong", FontWeight: 400}},
	)
	lines := f.Layout().Lines
	if lines[0].H != 44 || !strings.Contains(lines[0].Runs[0].Font, "700") {
		t.Fatalf("heading line: H=%g runs=%+v", lines[0].H, lines[0].Runs)
	}
	var emph, strong, plain Run
	var runs []Run
	for _, ln := range lines[1:] {
		runs = append(runs, ln.Runs...)
	}
	for _, r := range runs {
		switch r.Text {
		case "emph":
			emph = r
		case "strong":
			strong = r
		case "plain ":
			plain = r
		}
	}
	if !strings.Contains(emph.Font, "italic") || strings.Contains(plain.Font, "italic") {
		t.Errorf("Emphasis is italic: %q, plain %q", emph.Font, plain.Font)
	}
	if !strings.Contains(strong.Font, "400") {
		t.Errorf("direct formatting beats the style: %q", strong.Font)
	}
	// The paragraph style's spacing applies; the next paragraph is unaffected.
	if lines[1].Y-lines[0].Y != 44+4 { // line height + Heading 1's SpaceAfter
		t.Errorf("space after the heading: %g", lines[1].Y-lines[0].Y)
	}

	// Redefining a style restyles its uses.
	f.DefineStyle(StyleDef{Name: "Heading 1", Paragraph: true, Format: Format{Scale: 3}})
	if h := f.Layout().Lines[0].H; h != 66 {
		t.Errorf("redefined heading: %g", h)
	}
	f.DefineStyle(StyleDef{Name: "Custom", Format: Format{Color: "#f00"}})
	if _, ok := f.StyleByName("Custom"); !ok || len(f.Styles) != 11 {
		t.Errorf("DefineStyle adds: %d", len(f.Styles))
	}
	if got := f.styleNames(false); len(got) != 3 {
		t.Errorf("character styles: %v", got)
	}
	// Unknown names, and a character style used as a paragraph style, do nothing.
	g, _ := styledFlow(ParaFormat{Style: "Emphasis"}, Chars{Text: "a\n"}, ParaFormat{Style: "Nope"}, Chars{Text: "b", Format: Format{Style: "Heading 1"}})
	for _, ln := range g.Layout().Lines {
		if ln.H != 22 || strings.Contains(ln.Runs[0].Font, "italic") {
			t.Errorf("ignored styles: %+v", ln)
		}
	}
	// A paragraph style's character format does not leak into the next paragraph.
	h, _ := styledFlow(ParaFormat{Style: "Heading 1"}, Chars{Text: "A\nB"})
	if lines := h.Layout().Lines; lines[1].H != 22 {
		t.Errorf("next paragraph is plain: %g", lines[1].H)
	}
}

func TestMergeHelpers(t *testing.T) {
	got := mergeFormat(Format{Scale: 2, FontWeight: 700, Color: "red", Underline: true}, Format{Scale: 3, FontStyle: StyleItalic, Case: CaseUpper, Link: "u", Style: "S", FontFamily: "f", FontSize: 9, Background: "b", Strike: true, Superscript: true, Subscript: true})
	if got.Scale != 3 || got.FontWeight != 700 || got.Color != "red" || !got.Underline || !got.Strike || got.FontStyle != StyleItalic || got.Case != CaseUpper || got.Link != "u" || got.Style != "S" || got.FontFamily != "f" || got.FontSize != 9 || got.Background != "b" {
		t.Errorf("mergeFormat = %+v", got)
	}
	p := mergePara(ParaFormat{IndentLeft: 5, SpaceAfter: 2, List: ListStyle{Kind: ListBullet}}, ParaFormat{IndentLeft: 9, Align: AlignRight, IndentRight: 1, FirstLine: 2, SpaceBefore: 3, LineSpacing: 2, QuoteBar: "q", DropCap: 2, Style: "S", List: ListStyle{Kind: ListOrdered}})
	if p.IndentLeft != 9 || p.SpaceAfter != 2 || p.Align != AlignRight || p.List.Kind != ListOrdered || p.Style != "S" || p.DropCap != 2 || p.QuoteBar != "q" || p.LineSpacing != 2 || p.FirstLine != 2 || p.IndentRight != 1 || p.SpaceBefore != 3 {
		t.Errorf("mergePara = %+v", p)
	}
}

func TestStyleCommands(t *testing.T) {
	f, st := editFlow(t, 2, Chars{Text: "abcd\nefgh"})
	if len(f.Styles) != 0 {
		t.Fatal("no styles until one is used")
	}
	if !st.HandleChord("Mod+Alt+1") || f.ParagraphAt(0).Style != "Heading 1" || len(f.Styles) == 0 {
		t.Fatalf("heading 1: %+v styles %d", f.ParagraphAt(0), len(f.Styles))
	}
	if f.ParagraphAt(5).Style != "" {
		t.Error("only the caret's paragraph")
	}
	if f.Layout().Lines[0].H != 44 {
		t.Errorf("styled: %g", f.Layout().Lines[0].H)
	}
	st.HandleChord("Mod+Alt+1")
	if f.ParagraphAt(0).Style != "" {
		t.Error("applying it again takes it off")
	}
	st.HandleChord("Mod+Alt+2")
	st.HandleChord("Mod+Alt+3")
	if f.ParagraphAt(0).Style != "Heading 3" {
		t.Errorf("heading 3: %+v", f.ParagraphAt(0))
	}
	st.HandleChord("Mod+Alt+0")
	if f.ParagraphAt(0).Style != "" {
		t.Error("normal")
	}
	for cmd, name := range map[Command]string{CmdStyleTitle: "Title", CmdStyleSubtitle: "Subtitle", CmdStyleQuote: "Quote", CmdStyleCode: "Code"} {
		if !st.RunCommand(cmd) || f.ParagraphAt(0).Style != name {
			t.Errorf("%s: %+v", cmd, f.ParagraphAt(0))
		}
		st.RunCommand(cmd)
	}

	// Character styles toggle on the selection.
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 2}})
	if !st.RunCommand(CmdStyleEmphasis) || f.Slice(0, 1)[0].(Chars).Format.Style != "Emphasis" {
		t.Fatalf("emphasis: %+v", f.ContentItems())
	}
	st.RunCommand(CmdStyleEmphasis)
	if f.Slice(0, 1)[0].(Chars).Format.Style != "" {
		t.Error("emphasis toggles off")
	}
	st.RunCommand(CmdStyleStrong)

	// Clear formatting keeps links and lists, and drops the rest.
	f.ApplyFormat(func(x *Format) { x.Link = "http://x"; x.Color = "#f00" })
	f.ApplyParagraph(func(p *ParaFormat) { p.Align = AlignRight; p.List = ListStyle{Kind: ListBullet}; p.Style = "Heading 2" })
	if !st.HandleChord("Mod+\\") {
		t.Fatal("clear formatting")
	}
	if fm := firstChars(f).Format; fm != (Format{Link: "http://x"}) {
		t.Errorf("character formatting cleared, link kept: %+v", fm)
	}
	if p := f.ParagraphAt(0); p != (ParaFormat{List: ListStyle{Kind: ListBullet}}) {
		t.Errorf("paragraph formatting cleared, list kept: %+v", p)
	}
	if st.HandleChord("Mod+\\") {
		t.Error("nothing left to clear")
	}
}

func TestDefineStyleFromTheText(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "abcd\nefgh"})
	name := "Callout"
	st.Prompt = func(msg, initial string) (string, bool) { return name, name != "cancel" }
	f.ApplyFormat(func(x *Format) { x.Color = "#00f" })
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 4}})
	f.ApplyFormat(func(x *Format) { x.Color = "#00f"; x.FontWeight = 700 })
	f.ApplyParagraph(func(p *ParaFormat) { p.IndentLeft = 30; p.SpaceAfter = 8 })
	if !st.RunCommand(CmdDefineParaStyle) {
		t.Fatal("define paragraph style")
	}
	def, ok := f.StyleByName("Callout")
	if !ok || !def.Paragraph || def.Format.Color != "#00f" || def.Format.FontWeight != 700 || def.Para.IndentLeft != 30 || def.Para.SpaceAfter != 8 {
		t.Fatalf("definition = %+v", def)
	}
	if got := f.ParagraphAt(0); got.Style != "Callout" || got.IndentLeft != 0 {
		t.Errorf("the paragraph now uses the style: %+v", got)
	}
	// Redefining the style restyles the paragraph.
	def.Para.IndentLeft = 50
	f.DefineStyle(def)
	if x := f.Layout().Lines[0].X; x != 10+50 {
		t.Errorf("indent from the redefined style: %g", x)
	}
	name = "Mark"
	f.SetCursors([]Cursor{{Anchor: 5, Caret: 9}})
	f.ApplyFormat(func(x *Format) { x.Background = "#ff0" })
	if !st.RunCommand(CmdDefineCharStyle) {
		t.Fatal("define character style")
	}
	if d, ok := f.StyleByName("Mark"); !ok || d.Paragraph || d.Format.Background != "#ff0" {
		t.Errorf("character style = %+v", d)
	}
	name = "cancel"
	if st.RunCommand(CmdDefineCharStyle) {
		t.Error("cancel")
	}
	name = ""
	if st.RunCommand(CmdDefineCharStyle) {
		t.Error("an empty name")
	}
	st.Prompt = nil
	if st.RunCommand(CmdDefineParaStyle) {
		t.Error("no prompt")
	}
	// A box has no styles.
	bst, _ := stageWith(t, "x")
	if bst.HandleChord("Mod+Alt+1") || bst.RunCommand(CmdStyleEmphasis) {
		t.Error("a floating text has no named styles")
	}
	if ensureStyle(f, "No such style") {
		t.Error("unknown default")
	}
}

// firstChars is the first Chars item of the flow's content.
func firstChars(f *TextFlow) Chars {
	for _, it := range f.ContentItems() {
		if c, ok := it.(Chars); ok {
			return c
		}
	}
	return Chars{}
}
