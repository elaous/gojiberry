package canvas

import (
	"reflect"
	"testing"
)

func TestChord(t *testing.T) {
	for _, tt := range []struct {
		key                    string
		ctrl, shift, alt, meta bool
		want                   string
	}{
		{"b", true, false, false, false, "Mod+B"},
		{"B", false, false, false, true, "Mod+B"}, // Cmd is Mod too
		{"c", true, false, true, false, "Mod+Alt+C"},
		{"x", true, true, false, false, "Mod+Shift+X"},
		{"Enter", true, false, false, false, "Mod+Enter"},
		{".", true, false, false, false, "Mod+."},
		{"a", false, false, false, false, "A"},
		{"z", true, true, true, false, "Mod+Alt+Shift+Z"},
	} {
		if got := Chord(tt.key, tt.ctrl, tt.shift, tt.alt, tt.meta); got != tt.want {
			t.Errorf("Chord(%q,...) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestDefaultKeymap(t *testing.T) {
	km := DefaultKeymap()
	for chord, cmd := range map[string]Command{
		"Mod+A": CmdSelectAll, "Mod+B": CmdBold, "Mod+I": CmdItalic, "Mod+U": CmdUnderline,
		"Mod+Shift+X": CmdStrike, "Mod+.": CmdSuperscript, "Mod+,": CmdSubscript,
		"Mod+Alt+C": CmdCopyFormat, "Mod+Alt+V": CmdPasteFormat,
	} {
		if km[chord] != cmd {
			t.Errorf("%s = %q, want %q", chord, km[chord], cmd)
		}
	}
	if _, ok := km["Mod+C"]; ok {
		t.Error("copy goes through the browser's clipboard events, not the keymap")
	}
}

func TestCommonFormat(t *testing.T) {
	bold := Format{FontWeight: 700, Color: "#f00", Underline: true, FontFamily: "Georgia", FontSize: 20, Scale: 2, Background: "#ff0", FontStyle: StyleItalic, Strike: true, Superscript: true, Subscript: true}
	if got := CommonFormat(); got != (Format{}) {
		t.Fatalf("no formats: %+v", got)
	}
	if got := CommonFormat(bold); got != bold {
		t.Fatalf("one format is its own common format: %+v", got)
	}
	if got := CommonFormat(bold, bold); got != bold {
		t.Fatalf("identical formats: %+v", got)
	}
	other := Format{FontWeight: 400, Color: "#00f", FontFamily: "Arial", FontSize: 10, Scale: 1, Background: "#0ff", FontStyle: StyleNormal}
	if got := CommonFormat(bold, other); got != (Format{}) {
		t.Fatalf("nothing in common: %+v", got)
	}
	a := Format{FontWeight: 700, Color: "#f00", Underline: true, Strike: true}
	b := Format{FontWeight: 700, Color: "#00f", Underline: true}
	c := Format{FontWeight: 700, Underline: true, Strike: true}
	want := Format{FontWeight: 700, Underline: true}
	if got := CommonFormat(a, b, c); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFormatClipboard(t *testing.T) {
	var c FormatClipboard
	if _, ok := c.Get(); ok {
		t.Fatal("empty")
	}
	c.Set(Format{FontWeight: 700})
	if f, ok := c.Get(); !ok || f.FontWeight != 700 {
		t.Fatal("set/get")
	}
	c.Set(Format{}) // copying plain text is a copy too
	if _, ok := c.Get(); !ok {
		t.Fatal("a zero format is still a format")
	}
	c.Clear()
	if _, ok := c.Get(); ok {
		t.Fatal("cleared")
	}
}

type cmdSpy struct {
	spy
	cmds    []Command
	apply   bool
	copyTxt string
	copyOK  bool
	pasted  []string
	formats *FormatClipboard
}

func (c *cmdSpy) HandleCommand(cmd Command) bool        { c.cmds = append(c.cmds, cmd); return c.apply }
func (c *cmdSpy) CopyText() (string, bool)              { return c.copyTxt, c.copyOK }
func (c *cmdSpy) CutText() (string, bool)               { return c.copyTxt + "!", c.copyOK }
func (c *cmdSpy) PasteText(s string) bool               { c.pasted = append(c.pasted, s); return c.apply }
func (c *cmdSpy) SetFormatClipboard(f *FormatClipboard) { c.formats = f }

type typerSpy struct {
	spy
	typed []string
}

func (t *typerSpy) InsertText(s string) bool { t.typed = append(t.typed, s); return true }

func TestStageChordsAndClipboardRouting(t *testing.T) {
	var log []string
	a := &cmdSpy{spy: spy{name: "a", log: &log}, apply: true, copyTxt: "from a", copyOK: true}
	b := &cmdSpy{spy: spy{name: "b", log: &log}}
	s := NewStage(&fakePainter{}, 1, 1, a, b)
	if a.formats == nil || a.formats != s.Formats || b.formats != s.Formats {
		t.Fatal("components share the stage's format clipboard")
	}
	if !s.HandleChord("Mod+B") || !reflect.DeepEqual(b.cmds, []Command{CmdBold}) || !reflect.DeepEqual(a.cmds, []Command{CmdBold}) {
		t.Fatalf("topmost first, then on to a: b=%v a=%v", b.cmds, a.cmds)
	}
	if s.HandleChord("Mod+Q") {
		t.Fatal("unbound chord")
	}
	s.Keymap = Keymap{"Mod+Q": CmdItalic}
	if !s.HandleChord("Mod+Q") || s.HandleChord("Mod+B") {
		t.Fatal("a custom keymap replaces the default")
	}
	if text, ok := s.Copy(); !ok || text != "from a" {
		t.Fatalf("copy = %q %v (b has nothing, so a answers)", text, ok)
	}
	if text, ok := s.Cut(); !ok || text != "from a!" {
		t.Fatalf("cut = %q %v", text, ok)
	}
	if !s.Paste("pasted") || len(b.pasted) != 1 || len(a.pasted) != 1 {
		t.Fatalf("paste offered topmost first, then on: b=%v a=%v", b.pasted, a.pasted)
	}
	a.copyOK = false
	if _, ok := s.Copy(); ok {
		t.Fatal("nothing to copy")
	}
	if _, ok := s.Cut(); ok {
		t.Fatal("nothing to cut")
	}
	a.apply = false
	if s.Paste("x") {
		t.Fatal("nobody took it")
	}
	var log2 []string
	ty := &typerSpy{spy: spy{name: "t", log: &log2}}
	s2 := NewStage(&fakePainter{}, 1, 1, ty)
	if !s2.Paste("hello") || !reflect.DeepEqual(ty.typed, []string{"hello"}) {
		t.Fatalf("typed = %v", ty.typed)
	}
	nc := &FormatClipboard{}
	s.SetFormatClipboard(nc)
	if a.formats != nc || s.Formats != nc {
		t.Fatal("SetFormatClipboard")
	}
}

func TestScrollViewForwardsCommandsAndClipboard(t *testing.T) {
	var log []string
	sv := NewScrollView(0, 0, 100, 100)
	sv.SetPainter(&fakePainter{})
	c := &cmdSpy{spy: spy{name: "c", log: &log}, apply: true, copyTxt: "inner", copyOK: true}
	sv.Add(c, 10, 10)
	st := NewStage(&fakePainter{}, 1, 1, sv)
	if c.formats != st.Formats {
		t.Fatal("the stage's format clipboard reaches components inside a ScrollView")
	}
	if !sv.HandleCommand(CmdBold) || len(c.cmds) != 1 {
		t.Fatal("command")
	}
	if text, _ := sv.CopyText(); text != "inner" {
		t.Fatal("copy")
	}
	if text, _ := sv.CutText(); text != "inner!" {
		t.Fatal("cut")
	}
	if !sv.PasteText("p") || len(c.pasted) != 1 {
		t.Fatal("paste")
	}
}

func TestCommonFormatDropsPlaceMarks(t *testing.T) {
	got := CommonFormat(Format{FontWeight: 700, Comment: "c1", Bookmark: "b", Index: "i"})
	if got.Comment != "" || got.Bookmark != "" || got.Index != "" || got.FontWeight != 700 {
		t.Fatalf("%+v", got)
	}
}
