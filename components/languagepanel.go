package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// GrammarIssue is one thing the grammar check found, as LanguagePanel shows it.
type GrammarIssue struct {
	// Text is the words concerned, Message what is wrong, Suggestion what to
	// write instead ("" when the model offered none).
	Text, Message, Suggestion string
}

// LanguageTarget is a language the text can be translated into.
type LanguageTarget struct{ Code, Name string }

// LanguagePanelProps configures LanguagePanel. It is controlled: the caller
// runs the backends (packages lang, translate and grammar) and feeds back what
// they say.
type LanguagePanelProps struct {
	// Detected is the name of the language the text seems to be in ("" if
	// unknown).
	Detected string
	// OnUseDetected, when set, adds a button that makes the detected language the language of the document
	// (UseDetectedDone says it already is, and the button is then off).
	OnUseDetected   func()
	UseDetectedDone bool

	// Targets are the languages offered for translation; empty means no
	// translation backend is set up, which is said instead of offering it.
	Targets []LanguageTarget
	// Target is the chosen one. Translation is the result for the selection (""
	// until asked), and HasSelection whether anything is selected to translate.
	Target       string
	Translation  string
	HasSelection bool

	// GrammarEnabled says a grammar backend is set up; Issues are what it found.
	GrammarEnabled bool
	Issues         []GrammarIssue
	// Checked is true once a grammar check has run (so "no issues" can be told
	// from "not run").
	Checked bool

	// Busy is shown while a backend is working; Error is the last failure.
	Busy  bool
	Error string
	// ReadOnly hides the buttons that change the text.
	ReadOnly bool

	OnTarget       func(code string)
	OnTranslate    func()
	OnUseTranslate func()
	OnCheckGrammar func()
	OnApplyIssue   func(index int)
	OnGotoIssue    func(index int)
}

// LanguagePanel shows the detected language, a translator for the selection and
// the grammar issues with the fix for each.
func LanguagePanel(p LanguagePanelProps) xango.Node {
	call := func(f func()) func(xango.Event) {
		return func(xango.Event) {
			if f != nil {
				f()
			}
		}
	}
	btn := func(class, label string, disabled bool, f func()) xango.Node {
		a := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.On("click", call(f))}
		if disabled {
			a = append(a, xango.A("disabled", "disabled"))
		}
		return xango.El("button", a, xango.Text(label))
	}
	detected := trf("lang.detected", "Language: %s", tr("lang.notSure", "not sure"))
	if p.Detected != "" {
		detected = trf("lang.detected", "Language: %s", p.Detected)
	}
	kids := []xango.Node{xango.El("p", []xango.Attr{xango.A("class", "gj-lang-detected")}, xango.Text(detected))}
	if p.Detected != "" && p.OnUseDetected != nil {
		kids = append(kids, btn("gj-lang-docuse", tr("lang.useDetected", "Use as the language of the document"), p.UseDetectedDone, p.OnUseDetected))
	}
	if p.Busy {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-busy"), xango.A("role", "status")}, xango.Text(tr("lang.working", "Working…"))))
	}
	if p.Error != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-error"), xango.A("role", "alert")}, xango.Text(p.Error)))
	}

	// Translation.
	kids = append(kids, xango.El("h4", nil, xango.Text(tr("lang.translateHead", "Translate the selection"))))
	if len(p.Targets) == 0 {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-off")}, xango.Text(tr("lang.noTranslator", "No translation server is set up."))))
	} else {
		var opts []xango.Node
		for _, t := range p.Targets {
			o := []xango.Attr{xango.A("value", t.Code)}
			if t.Code == p.Target {
				o = append(o, xango.A("selected", "selected"))
			}
			opts = append(opts, xango.El("option", o, xango.Text(t.Name)))
		}
		sel := xango.El("select", []xango.Attr{xango.A("class", "gj-lang-target"), xango.A("aria-label", tr("lang.translateInto", "Translate into")),
			xango.On("change", func(e xango.Event) {
				if p.OnTarget != nil {
					p.OnTarget(xango.EventValue[string](e))
				}
			})}, opts...)
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-lang-row")}, sel,
			btn("gj-lang-translate", tr("lang.translate", "Translate"), !p.HasSelection || p.Busy, p.OnTranslate)))
		if !p.HasSelection {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-off")}, xango.Text(tr("lang.selectFirst", "Select some text to translate it."))))
		}
		if p.Translation != "" {
			kids = append(kids, xango.El("blockquote", []xango.Attr{xango.A("class", "gj-lang-translation")}, xango.Text(p.Translation)))
			if !p.ReadOnly {
				kids = append(kids, btn("gj-lang-use", tr("lang.replaceSelection", "Replace the selection"), false, p.OnUseTranslate))
			}
		}
	}

	// Grammar.
	kids = append(kids, xango.El("h4", nil, xango.Text(tr("lang.grammar", "Grammar"))))
	if !p.GrammarEnabled {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-off")}, xango.Text(tr("lang.noGrammar", "No grammar model is set up."))))
	} else {
		kids = append(kids, btn("gj-lang-check", tr("lang.checkGrammar", "Check grammar"), p.Busy, p.OnCheckGrammar))
		if p.Checked && len(p.Issues) == 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-lang-none"), xango.A("role", "status")}, xango.Text(tr("lang.noIssues", "No grammar issues found"))))
		}
		for i, is := range p.Issues {
			row := []xango.Node{
				xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-lang-issue-text"), xango.A("title", tr("lang.goIssue", "Go to it")), xango.On("click", call(func() {
					if p.OnGotoIssue != nil {
						p.OnGotoIssue(i)
					}
				}))}, xango.Text(is.Text)),
				xango.El("span", []xango.Attr{xango.A("class", "gj-lang-issue-message")}, xango.Text(is.Message)),
			}
			if is.Suggestion != "" && !p.ReadOnly {
				row = append(row, btn("gj-lang-apply", trf("lang.changeTo", "Change to “%s”", is.Suggestion), false, func() {
					if p.OnApplyIssue != nil {
						p.OnApplyIssue(i)
					}
				}))
			}
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-lang-issue"), xango.A("data-index", strconv.Itoa(i))}, row...))
		}
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-lang"), xango.A("aria-label", tr("lang.region", "Language tools"))}, kids...)
}
