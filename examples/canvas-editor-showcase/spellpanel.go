//go:build js && wasm

package main

import (
	"encoding/json"
	"slices"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/lang"
	"github.com/elaous/gojiberry/spell"
	"github.com/elaous/gojiberry/xango"
)

const customWordsKey = "showcase:dictionary"

// spellState is the spell checker: off until asked for (the dictionary takes a
// moment to load), then every change of the text is checked again.
type spellState struct {
	enabled bool
	checker *spell.Checker
	spans   []canvas.Match
	text    string // the text the spans were found in
	words   []components.SpellItem
}

func loadCustomWords() []string {
	var words []string
	if ls := js.Global().Get("localStorage"); !ls.IsUndefined() && !ls.IsNull() {
		if v := ls.Call("getItem", customWordsKey); v.Truthy() {
			_ = json.Unmarshal([]byte(v.String()), &words)
		}
	}
	return words
}

func saveCustomWords(words []string) {
	if ls := js.Global().Get("localStorage"); !ls.IsUndefined() && !ls.IsNull() {
		b, _ := json.Marshal(words)
		ls.Call("setItem", customWordsKey, string(b))
	}
}

// enableSpell loads the dictionary and the user's own words.
func (u *ui) enableSpell() {
	if u.spell.checker == nil {
		c := spell.NewChecker(spell.English())
		for _, w := range loadCustomWords() {
			c.Add(w)
		}
		u.spell.checker = c
	}
	u.spell.enabled = true
}

// refreshSpell checks the flow's text again and marks the words on the canvas.
func (u *ui) refreshSpell(flow *canvas.TextFlow) {
	s := &u.spell
	s.spans, s.words = nil, nil
	if flow == nil || !s.enabled || s.checker == nil {
		if flow != nil {
			flow.SetSquiggles(nil, "")
		}
		return
	}
	s.text = flow.EditText()
	rs := []rune(s.text)
	spans := s.checker.Misspelled(s.text)
	order := []string{}
	byWord := map[string]*components.SpellItem{}
	for _, sp := range spans {
		s.spans = append(s.spans, canvas.Match{From: sp.From, To: sp.To})
		w := string(rs[sp.From:sp.To])
		key := strings.ToLower(w)
		if it, ok := byWord[key]; ok {
			it.Count++
			continue
		}
		byWord[key] = &components.SpellItem{Word: w, Count: 1}
		order = append(order, key)
	}
	for i, key := range order {
		it := byWord[key]
		if i < 25 { // suggestions cost a little; the rest show none until fixed
			it.Suggestions = s.checker.Suggest(it.Word, 5)
		}
		s.words = append(s.words, *it)
	}
	flow.SetSquiggles(s.spans, "")
}

// occurrences returns the spans of word (any case) in the checked text.
func (u *ui) occurrences(word string) []canvas.Match {
	rs := []rune(u.spell.text)
	var out []canvas.Match
	for _, m := range u.spell.spans {
		if strings.EqualFold(string(rs[m.From:m.To]), word) {
			out = append(out, m)
		}
	}
	return out
}

// spellPanel is the Spelling tab.
func spellPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroll func(), redraw, renderUI func()) xango.Node {
	s := &u.spell
	fl := flow()
	again := func() {
		u.refreshSpell(fl)
		redraw()
		renderUI()
	}
	detected := ""
	if fl != nil && s.enabled {
		if code, ok := lang.Detect(fl.EditText()); ok {
			detected = lang.Names[code]
		}
	}
	return components.SpellPanel(components.SpellPanelProps{
		Enabled: s.enabled, Language: lang.Names["en"], Detected: detected, Items: s.words,
		ReadOnly: !state.Role.CanEdit() || state.Mode == canvas.ModeView,
		OnToggle: func(on bool) {
			if on {
				u.enableSpell()
			} else {
				s.enabled = false
			}
			again()
		},
		OnPick: func(word, suggestion string) {
			if fl == nil {
				return
			}
			if _, err := fl.ReplaceMatches(u.occurrences(word), canvas.FindOptions{Query: word}, suggestion); err == nil {
				again()
			}
		},
		OnIgnore: func(word string) { s.checker.Ignore(word); again() },
		OnAdd: func(word string) {
			s.checker.Add(word)
			words := loadCustomWords()
			if w := strings.ToLower(word); !slices.Contains(words, w) {
				saveCustomWords(append(words, w))
			}
			again()
		},
		OnGoto: func(word string) {
			if ms := u.occurrences(word); len(ms) > 0 && fl != nil {
				fl.GotoMatch(ms[0])
				scroll()
				redraw()
			}
		},
	})
}
