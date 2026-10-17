package helper

import (
	"errors"
	"strings"
	"testing"
)

func TestPromptNamesTheActionTheTextAndTheLanguage(t *testing.T) {
	for _, a := range Actions {
		p, err := Prompt(a, "The cat sat.", "en")
		if err != nil || !strings.Contains(p, "The cat sat.") || !strings.Contains(p, "tag en") || !strings.Contains(p, "same language") {
			t.Errorf("%s: %q %v", a, p, err)
		}
	}
	if p, _ := Prompt(Plainer, "x", ""); strings.Contains(p, "tag") {
		t.Error("no language tag when none is given")
	}
	if _, err := Prompt("sing", "x", ""); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := Prompt(Shorter, "   ", ""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	var tl ErrTooLong
	if _, err := Prompt(Summarise, strings.Repeat("a", MaxText+1), ""); !errors.As(err, &tl) || tl.Runes != MaxText+1 {
		t.Errorf("too long is refused, not cut: %v", err)
	}
	if _, err := Prompt(Summarise, strings.Repeat("é", MaxText), ""); err != nil {
		t.Errorf("the limit counts characters, not bytes: %v", err)
	}
}

func TestReplacesOnlyForRewrites(t *testing.T) {
	want := map[Action]bool{Summarise: false, Explain: false, Plainer: true, Shorter: true, Formal: true, Friendly: true}
	for _, a := range Actions {
		if a.Replaces() != want[a] {
			t.Errorf("%s: Replaces = %v", a, a.Replaces())
		}
	}
}

func TestCleanTidiesAModelsAnswer(t *testing.T) {
	for in, want := range map[string]string{
		"  A short text.  ":                          "A short text.",
		"\"Quoted whole.\"":                          "Quoted whole.",
		"“Smart quotes.”":                            "Smart quotes.",
		"Here is the summary:\nThe cat sat.":         "The cat sat.",
		"Sure, here's a shorter version:\nIt sat.":   "It sat.",
		"```\nfenced\n```":                           "fenced",
		"```text\nfenced too\n```":                   "fenced too",
		"He said \"no\" and \"yes\".":                "He said \"no\" and \"yes\".",
		"\"One\" and \"two\"":                        "\"One\" and \"two\"",
		"A line:\nnot an introduction I should drop": "A line:\nnot an introduction I should drop",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
