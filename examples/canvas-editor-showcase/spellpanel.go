//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"syscall/js"
	"time"

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

	// Dictionaries beyond English come from a server on demand (spell.Remote, spell/dictserver).
	remote  *spell.Remote
	offered []spell.Language // what the server has; nil until asked, empty if it has none
	asked   bool
	dict    string // the dictionary in use: "en", a name from offered, or "addon:" and the id of one the spell add-on keeps
	note    string
	failed  bool
}

// dictionaryServer is the address of the dictionary server: ?dictapi= when it is this origin or a loopback host,
// else /dict/ on the page's own origin.
func dictionaryServer() string {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	if q.Call("has", "dictapi").Bool() {
		if b := pageEndpoint(strings.TrimSpace(q.Call("get", "dictapi").String())); b != "" {
			return strings.TrimSuffix(b, "/")
		}
	}
	return js.Global().Get("location").Get("origin").String() + "/dict"
}

// askDictionaries finds out which dictionaries the server has (once); nothing is shown if it has none.
func (u *ui) askDictionaries(renderUI func()) {
	s := &u.spell
	if s.asked {
		return
	}
	s.asked = true
	s.remote = &spell.Remote{BaseURL: dictionaryServer()}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		langs, err := s.remote.Languages(ctx)
		if err != nil || len(langs) == 0 {
			s.offered = []spell.Language{}
		} else {
			s.offered = langs
		}
		renderUI()
	}()
}

// dictionaryName is how a dictionary is shown to people.
func (s *spellState) dictionaryName(name string) string {
	if name == "" || name == "en" {
		return lang.Names["en"]
	}
	if id, ok := strings.CutPrefix(name, spellAddonPrefix); ok {
		for _, m := range addonsState.models["spell"] {
			if m.ID == id {
				return m.Label
			}
		}
		return id
	}
	for _, l := range s.offered {
		if l.Lang == name && l.Name != "" {
			return l.Name
		}
	}
	return name
}

// chooseDictionary switches the checker to the English dictionary or loads one from the server.
func (u *ui) chooseDictionary(name string, again, renderUI func()) {
	s := &u.spell
	if s.checker == nil {
		return
	}
	s.failed = false
	if name == "en" {
		s.checker.Dict, s.dict, s.note = spell.English(), "en", ""
		again()
		return
	}
	s.note = T("spell.loadingDict", "Loading the dictionary %s…", s.dictionaryName(name))
	renderUI()
	if id, ok := strings.CutPrefix(name, spellAddonPrefix); ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			d, err := loadAddonDictionary(ctx, id)
			if err != nil {
				s.failed = true
				s.note = T("spell.dictFailed", "The dictionary %s could not be loaded: %s", s.dictionaryName(name), err.Error())
				renderUI()
				return
			}
			s.checker.Dict, s.dict, s.note = d, name, ""
			again()
		}()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		d, err := s.remote.Load(ctx, name)
		if err != nil {
			s.failed = true
			s.note = T("spell.dictFailed", "The dictionary %s could not be loaded: %s", s.dictionaryName(name), err.Error())
			renderUI()
			return
		}
		s.checker.Dict, s.dict, s.note = d, name, ""
		again()
	}()
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
			flow.SetSquigglesAs("spell", nil, "")
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
	flow.SetSquigglesAs("spell", s.spans, "")
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
	if s.enabled {
		u.askDictionaries(renderUI)
		askSpellAddon(renderUI)
	}
	dicts := []components.DocChoice{{Name: "en", Label: lang.Names["en"]}}
	for _, l := range s.offered {
		label := l.Name
		if label == "" {
			label = l.Lang
		}
		dicts = append(dicts, components.DocChoice{Name: l.Lang, Label: label})
	}
	for _, m := range addonsState.models["spell"] { // the dictionaries the add-on keeps on this device
		if m.Installed {
			dicts = append(dicts, components.DocChoice{Name: spellAddonPrefix + m.ID, Label: m.Label})
		}
	}
	current := s.dict
	if current == "" {
		current = "en"
	}
	return components.SpellPanel(components.SpellPanelProps{
		Dictionaries: dicts, Dictionary: current, DictionaryNote: s.note, DictionaryFailed: s.failed,
		OnDictionary: func(name string) { u.chooseDictionary(name, again, renderUI) },
		Enabled:      s.enabled, Language: s.dictionaryName(current), Detected: detected, Items: s.words,
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
