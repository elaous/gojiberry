//go:build !(js && wasm)

package e2etest

import (
	"strings"
	"unicode"

	"github.com/chromedp/cdproto/input"
)

type keyDef struct {
	key, code string
	vk        int64
	text      string // what typing it inserts, if anything
}

var named = map[string]keyDef{
	"Enter":      {"Enter", "Enter", 13, "\r"},
	"Tab":        {"Tab", "Tab", 9, ""},
	"Escape":     {"Escape", "Escape", 27, ""},
	"Backspace":  {"Backspace", "Backspace", 8, ""},
	"Delete":     {"Delete", "Delete", 46, ""},
	"Insert":     {"Insert", "Insert", 45, ""},
	"Home":       {"Home", "Home", 36, ""},
	"End":        {"End", "End", 35, ""},
	"PageUp":     {"PageUp", "PageUp", 33, ""},
	"PageDown":   {"PageDown", "PageDown", 34, ""},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37, ""},
	"ArrowUp":    {"ArrowUp", "ArrowUp", 38, ""},
	"ArrowRight": {"ArrowRight", "ArrowRight", 39, ""},
	"ArrowDown":  {"ArrowDown", "ArrowDown", 40, ""},
	"Space":      {" ", "Space", 32, " "},
	"F3":         {"F3", "F3", 114, ""},
	"Control":    {"Control", "ControlLeft", 17, ""},
	"Shift":      {"Shift", "ShiftLeft", 16, ""},
	"Alt":        {"Alt", "AltLeft", 18, ""},
	"Meta":       {"Meta", "MetaLeft", 91, ""},
}

var modBits = map[string]input.Modifier{
	"Control": input.ModifierCtrl, "Shift": input.ModifierShift, "Alt": input.ModifierAlt, "Meta": input.ModifierMeta,
}

type punctKey struct {
	vk   int64
	code string
}

var punct = map[rune]punctKey{
	'!': {49, "Digit1"}, '@': {50, "Digit2"}, '#': {51, "Digit3"}, '$': {52, "Digit4"}, '%': {53, "Digit5"},
	'^': {54, "Digit6"}, '&': {55, "Digit7"}, '*': {56, "Digit8"}, '(': {57, "Digit9"}, ')': {48, "Digit0"},
	' ': {32, "Space"}, ';': {186, "Semicolon"}, ':': {186, "Semicolon"}, '=': {187, "Equal"}, '+': {187, "Equal"},
	',': {188, "Comma"}, '<': {188, "Comma"}, '-': {189, "Minus"}, '_': {189, "Minus"}, '.': {190, "Period"},
	'>': {190, "Period"}, '/': {191, "Slash"}, '?': {191, "Slash"}, '`': {192, "Backquote"}, '~': {192, "Backquote"},
	'[': {219, "BracketLeft"}, '{': {219, "BracketLeft"}, '\\': {220, "Backslash"}, '|': {220, "Backslash"},
	']': {221, "BracketRight"}, '}': {221, "BracketRight"}, '\'': {222, "Quote"}, '"': {222, "Quote"},
}

func keyOf(name string) keyDef {
	if k, ok := named[name]; ok {
		return k
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("e2etest: unknown key " + name)
	}
	c := r[0]
	k := keyDef{key: name, text: name}
	switch {
	case unicode.IsLetter(c) && c < 128:
		k.code, k.vk = "Key"+strings.ToUpper(name), int64(unicode.ToUpper(c))
	case unicode.IsDigit(c) && c < 128:
		k.code, k.vk = "Digit"+name, int64(c)
	default:
		if p, ok := punct[c]; ok {
			k.vk, k.code = p.vk, p.code
		}
	}
	return k
}

func splitKeys(spec string) (mods []string, key string) {
	parts := strings.Split(spec, "+")
	if spec == "+" || strings.HasSuffix(spec, "++") {
		return parts[:len(parts)-2], "+"
	}
	return parts[:len(parts)-1], parts[len(parts)-1]
}
