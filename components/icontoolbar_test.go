package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func TestIconToolbarIsAToolbarOfNamedButtons(t *testing.T) {
	var ran []string
	html := render(t, IconToolbar(IconToolbarProps{ID: "tb", Label: "Formatting", Items: []ToolbarItem{
		{ID: "undo", Label: "Undo", Icon: "undo", Shortcut: "Ctrl+Z", Disabled: true},
		{Separator: true},
		{ID: "bold", Label: "Bold", Icon: "format_bold", Shortcut: "Ctrl+B", Toggle: true, Pressed: true, OnClick: func() { ran = append(ran, "bold") }},
		{ID: "link", Label: "Link", Icon: "link", OnClick: func() { ran = append(ran, "link") }},
		{Icon: "link"}, // no label: drawn as nothing
	}}))
	for _, want := range []string{`role="toolbar"`, `aria-label="Formatting"`, `aria-label="Bold"`, `title="Bold (Ctrl+B)"`, `aria-keyshortcuts="Ctrl+B"`,
		`aria-pressed="true"`, `role="separator"`, `data-material="format_bold"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the toolbar lacks %q:\n%s", want, html)
		}
	}
	if n := strings.Count(html, "<button"); n != 3 {
		t.Errorf("%d buttons, want 3 (the item without a label is not drawn)", n)
	}
	if n := strings.Count(html, `tabindex="0"`); n != 1 {
		t.Errorf("%d tab stops, want 1", n)
	}
	if i := strings.Index(html, `tabindex="0"`); !strings.Contains(html[i:i+200], `aria-label="Bold"`) && !strings.Contains(html[max(0, i-300):i+10], `aria-label="Bold"`) {
		t.Errorf("the tab stop is not the first enabled button (Bold): %s", html)
	}
	if !strings.Contains(html, "disabled") {
		t.Error("the disabled button is not disabled")
	}
	if strings.Contains(html, `data-cmd="undo"`) && !strings.Contains(html[strings.Index(html, `data-cmd="undo"`):], "disabled") {
		t.Error("undo should be disabled")
	}
}

func TestCommandToolbarItemsAgreeWithTheMenu(t *testing.T) {
	cmds := []canvas.CommandInfo{{Name: canvas.CmdBold, Title: "Bold"}, {Name: canvas.CmdItalic, Title: "Italic"}, {Name: canvas.CmdBulletList, Title: "Bulleted list"}}
	km := canvas.DefaultKeymap()
	var ran []canvas.Command
	items := CommandToolbarItems(cmds, km, false, [][]canvas.Command{{canvas.CmdBold, canvas.CmdItalic}, {canvas.CmdStrike}, {canvas.CmdBulletList}}, nil, func(c canvas.Command) { ran = append(ran, c) })
	var kinds []string
	for _, it := range items {
		if it.Separator {
			kinds = append(kinds, "|")
		} else {
			kinds = append(kinds, it.ID)
		}
	}
	if got := strings.Join(kinds, " "); got != "bold italic | list.bullets" {
		t.Fatalf("items = %q, want bold italic | list.bullets (strikethrough is not installed, so its group is gone)", got)
	}
	b := items[0]
	if b.Label != "Bold" || b.Icon != "format_bold" || b.Shortcut != "Ctrl+B" || !HasMaterial(b.Icon) {
		t.Errorf("bold = %+v", b)
	}
	items[0].OnClick()
	items[3].OnClick()
	if len(ran) != 2 || ran[0] != canvas.CmdBold || ran[1] != canvas.CmdBulletList {
		t.Errorf("ran %v", ran)
	}
}

func TestToolbarCommandsHaveIcons(t *testing.T) {
	for _, c := range []canvas.Command{canvas.CmdUndo, canvas.CmdRedo, canvas.CmdBold, canvas.CmdItalic, canvas.CmdUnderline, canvas.CmdStrike,
		canvas.CmdBulletList, canvas.CmdNumberedList, canvas.CmdAlignLeft, canvas.CmdAlignCenter, canvas.CmdAlignRight, canvas.CmdEditLink, canvas.CmdCommentAdd} {
		if ic := CommandMaterial(string(c)); ic == "" || !HasMaterial(ic) {
			t.Errorf("command %q has icon %q, which is not drawn", c, ic)
		}
	}
}
