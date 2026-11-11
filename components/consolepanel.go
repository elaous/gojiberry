package components

import (
	"strings"

	"github.com/elaous/gojiberry/xango"
)

type ConsoleLine struct {
	Text string
	Kind string
}

type ConsoleProps struct {
	Lines     []ConsoleLine
	Prompt    string
	Input     string
	OnInput   func(text string)
	OnSubmit  func()
	OnHistory func(dir int)
	// OnClear empties the log.
	OnClear    func()
	OnAddFiles func()
	Inbox      []string
	Question   string
	OnAnswer   func(yes bool)
	// Busy is set while a command runs.
	Busy bool
}

func ConsolePanel(p ConsoleProps) xango.Node {
	var lines []xango.Node
	for _, l := range p.Lines {
		class := "gj-console-line gj-console-" + l.Kind
		lines = append(lines, xango.El("p", []xango.Attr{xango.A("class", class)}, xango.Text(l.Text)))
	}
	kids := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-console-log"), xango.A("role", "log"), xango.A("aria-live", "polite"),
			xango.A("aria-label", tr("console.log", "Console output")), xango.A("tabindex", "0")}, lines...),
	}
	if p.Question != "" {
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-console-question"), xango.A("role", "alert")},
			xango.El("span", nil, xango.Text(p.Question)),
			docButton("gj-console-yes", tr("console.yes", "Yes"), p.OnAnswer == nil, func() { p.OnAnswer(true) }),
			docButton("gj-console-no", tr("console.no", "No"), p.OnAnswer == nil, func() { p.OnAnswer(false) })))
	}
	input := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-console-input"), xango.A("autocomplete", "off"), xango.A("spellcheck", "false"),
		xango.A("autocapitalize", "off"), xango.A("aria-label", tr("console.input", "Command")), xango.A("value", p.Input),
		xango.A("placeholder", tr("console.placeholder", "Type a command; help lists them")),
		xango.On("input", func(e xango.Event) {
			if p.OnInput != nil {
				p.OnInput(xango.EventValue[string](e))
			}
		}),
		xango.On("keydown", func(e xango.Event) {
			k, ok := e.Value.(xango.KeyData)
			if !ok || k.Ctrl || k.Alt || k.Meta || k.Shift {
				return
			}
			switch k.Key {
			case "Enter":
				e.PreventDefault()
				if p.OnSubmit != nil && !p.Busy {
					p.OnSubmit()
				}
			case "ArrowUp", "ArrowDown":
				if p.OnHistory != nil {
					e.PreventDefault()
					dir := -1
					if k.Key == "ArrowDown" {
						dir = 1
					}
					p.OnHistory(dir)
				}
			}
		})}
	if p.Busy {
		input = append(input, xango.A("aria-busy", "true"))
	}
	kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-console-row")},
		xango.El("span", []xango.Attr{xango.A("class", "gj-console-prompt"), xango.A("aria-hidden", "true")}, xango.Text(p.Prompt+">")),
		xango.El("input", input)))
	actions := []xango.Node{}
	if p.OnAddFiles != nil {
		actions = append(actions, docButton("gj-console-addfiles", tr("console.addFiles", "Add files to import…"), p.Busy, p.OnAddFiles))
	}
	if p.OnClear != nil {
		actions = append(actions, docButton("gj-console-clear", tr("console.clear", "Clear"), p.Busy || len(p.Lines) == 0, p.OnClear))
	}
	if len(actions) > 0 {
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-console-row")}, actions...))
	}
	if len(p.Inbox) > 0 {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-console-inbox")},
			xango.Text(trf("console.inbox", "Files added for import: %s", strings.Join(p.Inbox, ", ")))))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-console")}, kids...)
}
