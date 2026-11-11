//go:build js && wasm

package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/grammar"
	"github.com/elaous/gojiberry/lang"
	"github.com/elaous/gojiberry/translate"
	"github.com/elaous/gojiberry/xango"
)

type langState struct {
	translator translate.Translator // the server at ?translate=…, else the translate add-on, else nil
	grammar    grammar.Checker
	target     string
	translated string
	selFrom    int // the selection that was translated
	selTo      int
	transText  string
	// offsets mean something only in the whole text
	issues  []grammar.Issue
	text    string // the text the issues were found in
	checked bool
	busy    bool
	err     string
}

var translateTargets = []components.LanguageTarget{
	{Code: "DE", Name: "German"}, {Code: "FR", Name: "French"}, {Code: "ES", Name: "Spanish"},
	{Code: "IT", Name: "Italian"}, {Code: "PT-PT", Name: "Portuguese"}, {Code: "NL", Name: "Dutch"}, {Code: "EN-US", Name: "English"},
}

func (u *ui) setupLanguage(param func(string) string) {
	if url := pageEndpoint(param("translate")); url != "" {
		u.lang.translator = &translate.Client{BaseURL: url}
	}
	if url := pageEndpoint(param("grammar")); url != "" {
		model := param("model")
		if model == "" {
			model = "llama3.1"
		}
		u.lang.grammar = &grammar.Client{BaseURL: url, Model: model, HTTP: &http.Client{Timeout: 90 * time.Second}}
	}
	u.lang.target = "DE"
}

func langPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroll func(), redraw, renderUI func()) xango.Node {
	l := &u.lang
	fl := flow()
	tr := u.translator()
	gc := u.grammarChecker()
	detected, detectedCode := "", ""
	if fl != nil {
		if code, ok := lang.Detect(fl.EditText()); ok {
			detected, detectedCode = lang.Names[code], code
		}
	}
	props := components.LanguagePanelProps{
		Detected: detected, Target: l.target, Translation: l.translated, Busy: l.busy, Error: l.err, Checked: l.checked,
		GrammarEnabled:  gc != nil,
		ReadOnly:        !state.Role.CanEdit() || state.Mode == canvas.ModeView,
		OnTarget:        func(code string) { l.target = code },
		UseDetectedDone: u.docLang == detectedCode,
		OnUseDetected: func() {
			if state.Role.CanEdit() && detectedCode != "" {
				u.docLang = detectedCode
				u.announce(T("a11y.docLang", "Language of the document: %s", detected))
				renderUI()
			}
		},
	}
	if tr != nil {
		props.Targets = translateTargets
	}
	if fl != nil {
		from, to := fl.Selection()
		props.HasSelection = to > from
	}
	for _, is := range l.issues {
		props.Issues = append(props.Issues, components.GrammarIssue{Text: u.issueText(is), Message: is.Message, Suggestion: is.Suggestion})
	}
	props.OnTranslate = func() {
		if fl == nil || tr == nil || l.busy {
			return
		}
		text := fl.SelectedText()
		l.selFrom, l.selTo = fl.Selection()
		l.transText = fl.EditText()
		l.busy, l.err = true, ""
		renderUI()
		go func() {
			timeout := 30 * time.Second
			if l.translator == nil {
				timeout = 5 * time.Minute // a model on this device takes its time
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			r, err := tr.Translate(ctx, text, l.target, "")
			l.busy = false
			if err != nil {
				l.err = err.Error()
			} else {
				l.translated = r.Text
			}
			renderUI()
		}()
	}
	props.OnUseTranslate = func() {
		if fl == nil || l.translated == "" || props.ReadOnly {
			return
		}
		if fl.EditText() != l.transText {
			l.translated, l.err = "", "The text changed since it was translated: translate the selection again."
			renderUI()
			return
		}
		fl.ReplaceRange(l.selFrom, l.selTo, l.translated)
		l.translated = ""
		redraw()
		renderUI()
	}
	props.OnCheckGrammar = func() {
		if fl == nil || gc == nil || l.busy {
			return
		}
		text := fl.EditText()
		l.busy, l.err = true, ""
		renderUI()
		go func() {
			timeout := 90 * time.Second
			if l.grammar == nil {
				timeout = 10 * time.Minute // a model on this device takes its time
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			issues, err := gc.Check(ctx, text)
			l.busy = false
			if err != nil {
				l.err = err.Error()
			} else {
				l.issues, l.text, l.checked = issues, text, true
				u.markGrammar(fl)
			}
			redraw()
			renderUI()
		}()
	}
	props.OnApplyIssue = func(i int) {
		if fl == nil || i >= len(l.issues) || fl.EditText() != l.text {
			return
		}
		is := l.issues[i]
		fl.ReplaceRange(is.From, is.To, is.Suggestion)
		l.issues = append(l.issues[:i:i], l.issues[i+1:]...)
		l.text = fl.EditText()
		delta := len([]rune(is.Suggestion)) - (is.To - is.From)
		for j := i; j < len(l.issues); j++ {
			if l.issues[j].From >= is.To {
				l.issues[j].From += delta
				l.issues[j].To += delta
			}
		}
		u.markGrammar(fl)
		redraw()
		renderUI()
	}
	props.OnGotoIssue = func(i int) {
		if fl != nil && i < len(l.issues) && fl.EditText() == l.text {
			fl.GotoMatch(canvas.Match{From: l.issues[i].From, To: l.issues[i].To})
			scroll()
			redraw()
		}
	}
	return components.LanguagePanel(props)
}

func (u *ui) issueText(is grammar.Issue) string {
	rs := []rune(u.lang.text)
	if is.From < 0 || is.To > len(rs) {
		return ""
	}
	return strings.TrimSpace(string(rs[is.From:is.To]))
}

func (u *ui) markGrammar(fl *canvas.TextFlow) {
	var spans []canvas.Match
	for _, is := range u.lang.issues {
		spans = append(spans, canvas.Match{From: is.From, To: is.To})
	}
	fl.SetSquigglesAs("grammar", spans, "#7c5cff")
}
