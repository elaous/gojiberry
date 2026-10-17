// Package helper holds the writing-help actions the llm add-on can be asked for (summarise, make plainer, make shorter,
// change the tone, explain): the prompt of each, and the cleaning of what a model answers. It knows no model and no
// browser; the page sends the prompt to the add-on and shows the answer for the author to read and decide on.
package helper

import (
	"strings"
	"unicode/utf8"
)

// Action is something to ask for.
type Action string

// The actions, in the order a list shows them.
const (
	Summarise Action = "summarise"
	Plainer   Action = "plainer"
	Shorter   Action = "shorter"
	Formal    Action = "formal"
	Friendly  Action = "friendly"
	Explain   Action = "explain"
)

// Actions lists them.
var Actions = []Action{Summarise, Plainer, Shorter, Formal, Friendly, Explain}

// Valid says a is one of them.
func (a Action) Valid() bool {
	for _, v := range Actions {
		if v == a {
			return true
		}
	}
	return false
}

// Replaces says the answer is a new version of the text (it can replace the selection); Explain and Summarise are
// about the text and are put beside it.
func (a Action) Replaces() bool { return a == Plainer || a == Shorter || a == Formal || a == Friendly }

// MaxText is the longest text one request takes, in runes. Longer text is refused, not cut: a summary of half a text
// would pass for a summary of all of it.
const MaxText = 6000

const common = "Answer with only the result, in the same language as the text, with no introduction, no quotation marks and no notes."

var instructions = map[Action]string{
	Summarise: "Summarise the text below in a few sentences.",
	Plainer:   "Rewrite the text below in plain language: short sentences, common words, the same meaning, nothing added.",
	Shorter:   "Rewrite the text below to be shorter, keeping its meaning and its facts.",
	Formal:    "Rewrite the text below in a formal tone, keeping its meaning.",
	Friendly:  "Rewrite the text below in a friendly, warm tone, keeping its meaning.",
	Explain:   "Explain the text below in a few sentences for someone who does not know the subject.",
}

// ErrTooLong: the text is longer than MaxText.
type ErrTooLong struct{ Runes int }

func (e ErrTooLong) Error() string { return "helper: the text is too long for one request" }

// Prompt is the prompt for an action on a text; lang is the text's language tag ("" when not known).
func Prompt(a Action, text, lang string) (string, error) {
	if !a.Valid() {
		return "", ErrUnknown
	}
	if n := utf8.RuneCountInString(text); n > MaxText {
		return "", ErrTooLong{n}
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrEmpty
	}
	var b strings.Builder
	b.WriteString(instructions[a])
	b.WriteString(" ")
	b.WriteString(common)
	if lang != "" {
		b.WriteString(" The text is in the language with the tag " + lang + ".")
	}
	b.WriteString("\n\nText:\n")
	b.WriteString(text)
	return b.String(), nil
}

type helperError string

func (e helperError) Error() string { return string(e) }

// Errors of Prompt.
const (
	ErrUnknown helperError = "helper: no such action"
	ErrEmpty   helperError = "helper: there is no text"
)

// Clean tidies a model's answer: a code fence around it, a leading "Here is…" line and quotation marks round the whole
// of it are taken off, and surrounding space.
func Clean(answer string) string {
	s := strings.TrimSpace(answer)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		first := strings.ToLower(strings.TrimSpace(s[:i]))
		if strings.HasSuffix(first, ":") && len(first) < 60 && (strings.HasPrefix(first, "here") || strings.HasPrefix(first, "sure") || strings.HasPrefix(first, "certainly")) {
			s = strings.TrimSpace(s[i+1:])
		}
	}
	for _, q := range [][2]string{{"\"", "\""}, {"“", "”"}, {"«", "»"}} {
		if len(s) > len(q[0])+len(q[1]) && strings.HasPrefix(s, q[0]) && strings.HasSuffix(s, q[1]) && !strings.Contains(s[len(q[0]):len(s)-len(q[1])], q[0]) {
			s = strings.TrimSpace(s[len(q[0]) : len(s)-len(q[1])])
		}
	}
	return s
}
