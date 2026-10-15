//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// formatToolbar is the row of icon buttons for the formatting commands people use most (roadmap "Button icons"):
// each runs the command the menu and the key already run, with the menu's name, icon and shortcut, so the three
// agree; the ones that are on or off (bold, italic, underline, strikethrough) say which at the caret.
func formatToolbar(state *State, at canvas.Format, cmds []canvas.CommandInfo, keymap canvas.Keymap, mac bool, run func(canvas.Command)) xango.Node {
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	groups := [][]canvas.Command{
		{canvas.CmdUndo, canvas.CmdRedo},
		{canvas.CmdBold, canvas.CmdItalic, canvas.CmdUnderline, canvas.CmdStrike},
		{canvas.CmdBulletList, canvas.CmdNumberedList, canvas.CmdAlignLeft, canvas.CmdAlignCenter, canvas.CmdAlignRight},
		{canvas.CmdEditLink, canvas.CmdCommentAdd},
	}
	on := func(c canvas.Command) (toggle, pressed, disabled bool) {
		switch c {
		case canvas.CmdBold:
			return true, at.FontWeight >= 600, !canEdit
		case canvas.CmdItalic:
			return true, at.FontStyle == "italic", !canEdit
		case canvas.CmdUnderline:
			return true, at.Underline, !canEdit
		case canvas.CmdStrike:
			return true, at.Strike, !canEdit
		case canvas.CmdCommentAdd: // a commenter can comment without editing
			return false, false, !state.Role.CanComment()
		}
		return false, false, !canEdit
	}
	return components.IconToolbar(components.IconToolbarProps{ID: "format-toolbar", Label: T("tool.label", "Formatting"),
		Items: components.CommandToolbarItems(cmds, keymap, mac, groups, on, run)})
}
