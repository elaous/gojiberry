package components

import (
	"strings"
	"testing"
	"time"
)

var typingT0 = time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)

// typer drives a TypingAnnouncer like a person at the keyboard: the text and caret after each key.
type typer struct {
	a     *TypingAnnouncer
	text  []rune
	caret int
	now   time.Time
	said  []string
}

func newTyper(a *TypingAnnouncer, text string) *typer {
	ty := &typer{a: a, text: []rune(text), caret: len([]rune(text)), now: typingT0}
	a.Update(ty.now, ty.state())
	return ty
}

func (ty *typer) state() TypingState {
	s := TypingState{Text: string(ty.text), Caret: ty.caret}
	// the line: the paragraph the caret is in
	start := ty.caret
	for start > 0 && ty.text[start-1] != '\n' {
		start--
	}
	end := ty.caret
	for end < len(ty.text) && ty.text[end] != '\n' {
		end++
	}
	s.Line, s.LineStart = string(ty.text[start:end]), start
	return s
}

func (ty *typer) update(ms int) string {
	ty.now = ty.now.Add(time.Duration(ms) * time.Millisecond)
	out := ty.a.Update(ty.now, ty.state())
	if out != "" {
		ty.said = append(ty.said, out)
	}
	return out
}

// typeKeys types s one key at a time, ms apart, and returns what was said.
func (ty *typer) typeKeys(s string, ms int) {
	for _, r := range s {
		ty.text = append(ty.text[:ty.caret], append([]rune{r}, ty.text[ty.caret:]...)...)
		ty.caret++
		ty.update(ms)
	}
}

func (ty *typer) move(to, ms int) string { ty.caret = to; return ty.update(ms) }

func TestTypingSaysNothingByDefault(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{}, "")
	ty.typeKeys("hello world ", 100)
	ty.move(0, 1000)
	if len(ty.said) != 0 || ty.a.Pending() {
		t.Errorf("a person who set nothing hears nothing: %q", ty.said)
	}
}

func TestTypingWordsSaysEachFinishedWordAndHoldsToSpaceTheSpeech(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingWords}, "")
	ty.typeKeys("hello", 100)
	if len(ty.said) != 0 {
		t.Fatalf("a word is said when it is finished, not letter by letter: %q", ty.said)
	}
	ty.typeKeys(" ", 100)
	// the space finished "hello", but 0.6 s have not gone by since the start: first sentence is free
	if strings.Join(ty.said, "|") != "hello" {
		t.Fatalf("hello: %q", ty.said)
	}
	// two more words inside the spacing are held and said together
	ty.typeKeys("big ", 50) // "big" held
	ty.typeKeys("red ", 50) // "red" held
	if strings.Join(ty.said, "|") != "hello" {
		t.Fatalf("held inside the spacing: %q", ty.said)
	}
	ty.update(600)
	if strings.Join(ty.said, "|") != "hello|big red" {
		t.Errorf("said together after the spacing: %q", ty.said)
	}
	if ty.a.Pending() {
		t.Error("nothing is left to say")
	}
}

func TestTypingWordsSaysAWordLeftUnfinishedAfterAPause(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingWords}, "")
	ty.typeKeys("pause", 100)
	if !ty.a.Pending() {
		t.Error("an unfinished word is pending")
	}
	ty.update(1000)
	if len(ty.said) != 0 {
		t.Fatalf("not yet: %q", ty.said)
	}
	ty.update(700)
	if strings.Join(ty.said, "|") != "pause" {
		t.Errorf("after a pause the word is said: %q", ty.said)
	}
}

func TestTypingCharactersNamesSpacesAndLineBreaksAndSaysDeletions(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingCharacters}, "")
	ty.typeKeys("a", 600)
	ty.typeKeys(" ", 600)
	ty.typeKeys("\n", 600)
	ty.typeKeys("é", 600)
	ty.caret--
	ty.text = append(ty.text[:ty.caret], ty.text[ty.caret+1:]...) // Backspace
	ty.update(600)
	if got := strings.Join(ty.said, "|"); got != "a|space|new line|é|Deleted é" {
		t.Errorf("each character as it is typed: %q", got)
	}
	// the same letter twice in a row is still two sentences
	ty = newTyper(&TypingAnnouncer{Level: TypingCharacters}, "")
	ty.typeKeys("aa", 600)
	if got := strings.Join(ty.said, "|"); got != "a|a" {
		t.Errorf("repeated: %q", got)
	}
}

func TestTypingBoundsWhatIsHeldAndNamesAPaste(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingCharacters}, "")
	ty.typeKeys("abcdefghijkl", 10) // faster than the spacing: held, and only the latest are kept
	ty.update(600)
	if got := ty.said[len(ty.said)-1]; got != "g h i j k l" {
		t.Errorf("only the last few are kept: %q", ty.said)
	}
	ty = newTyper(&TypingAnnouncer{Level: TypingWords}, "")
	ty.text = []rune(strings.Repeat("word ", 30))
	ty.caret = len(ty.text)
	if got := ty.update(600); got != "Pasted 150 characters" {
		t.Errorf("a paste is counted, not read: %q", got)
	}
	// a word taken from the list is a piece that is finished
	ty = newTyper(&TypingAnnouncer{Level: TypingWords}, "ze")
	ty.text = []rune("zebra")
	ty.caret = 5
	if got := ty.update(600); got != "zebra" {
		t.Errorf("a completed word is said at once: %q", got)
	}
}

func TestTypingDoesNotSayChangesMadeElsewhere(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingCharacters}, "Hello")
	// someone else adds text at the start: the local caret is not where the new text ends
	ty.text = append([]rune("Ana: "), ty.text...)
	ty.caret = 10
	ty.update(600)
	if len(ty.said) != 0 {
		t.Errorf("another person's edit is not read as typing: %q", ty.said)
	}
}

func TestTypingReadsTheLineTheCaretGoesToAndWhatItPassesOver(t *testing.T) {
	text := "first line\nsecond line\n\nfourth"
	ty := newTyper(&TypingAnnouncer{Level: TypingCharacters, Lines: true}, text)
	if got := ty.move(15, 600); got != "second line" { // to another line: it is read
		t.Errorf("line: %q", got)
	}
	if got := ty.move(16, 600); got != "n" { // right: the character passed over
		t.Errorf("char: %q", got)
	}
	if got := ty.move(11, 600); got != "secon" { // a longer move in the line: what it passed over
		t.Errorf("span: %q", got)
	}
	if got := ty.move(23, 600); got != "blank" {
		t.Errorf("an empty line: %q", got)
	}
	// the same text on two lines is still told apart
	ty = newTyper(&TypingAnnouncer{Lines: true}, "same\nsame")
	ty.move(2, 600)
	if got := ty.move(7, 600); got != "same" {
		t.Errorf("the second same: %q", got)
	}
	// without the preference nothing is read
	ty = newTyper(&TypingAnnouncer{Level: TypingCharacters}, text)
	if got := ty.move(15, 600); got != "" {
		t.Errorf("not asked for: %q", got)
	}
}

func TestTypingWordsReadsTheWordsPassedOverByACtrlArrow(t *testing.T) {
	ty := newTyper(&TypingAnnouncer{Level: TypingWords, Lines: true}, "one two three")
	ty.move(0, 600)
	if got := ty.move(4, 600); got != "one" {
		t.Errorf("ctrl+right: %q", got)
	}
	if got := ty.move(5, 600); got != "" {
		t.Errorf("one character is not a word at this level: %q", got)
	}
}

func TestTypingSaysFormattingChangesAtTheCaret(t *testing.T) {
	a := &TypingAnnouncer{Formatting: true}
	a.Update(typingT0, TypingState{Text: "x"})
	at := func(ms int, marks []string, block string) string {
		return a.Update(typingT0.Add(time.Duration(ms)*time.Millisecond), TypingState{Text: "x", Marks: marks, Block: block})
	}
	if got := at(600, []string{"Bold"}, ""); got != "Bold on" {
		t.Errorf("on: %q", got)
	}
	if got := at(1200, []string{"Bold", "Italic"}, ""); got != "Italic on" {
		t.Errorf("another: %q", got)
	}
	if got := at(1800, nil, ""); got != "Bold off Italic off" {
		t.Errorf("both off, said together: %q", got)
	}
	if got := at(2400, nil, "Heading 2"); got != "Heading 2" {
		t.Errorf("block: %q", got)
	}
	if got := at(3000, nil, "Heading 2"); got != "" {
		t.Errorf("no change, no words: %q", got)
	}
	// off by preference: silent
	b := &TypingAnnouncer{}
	b.Update(typingT0, TypingState{Text: "x"})
	if got := b.Update(typingT0.Add(time.Second), TypingState{Text: "x", Marks: []string{"Bold"}}); got != "" {
		t.Errorf("formatting not asked for: %q", got)
	}
}

func TestTypingLiveIsAPoliteStatusRegionThatChangesForARepeatedSentence(t *testing.T) {
	a := render(t, TypingLive("a", 0))
	b := render(t, TypingLive("a", 1))
	for _, want := range []string{`id="typing-live"`, `role="status"`, `aria-live="polite"`, `gj-sr-only`} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %s", want, a)
		}
	}
	if a == b {
		t.Errorf("a sentence said twice must differ in the page: %s", a)
	}
	if empty := render(t, TypingLive("", 1)); strings.Contains(empty, " ") {
		t.Errorf("an empty region stays empty: %q", empty)
	}
}

func TestTypingResetForgetsTheDocument(t *testing.T) {
	a := &TypingAnnouncer{Level: TypingWords}
	a.Update(typingT0, TypingState{Text: "a", Caret: 1})
	a.Reset()
	if got := a.Update(typingT0.Add(time.Second), TypingState{Text: strings.Repeat("x ", 100), Caret: 200}); got != "" {
		t.Errorf("a document that replaces the old one is not a paste: %q", got)
	}
	if a.Level != TypingWords {
		t.Error("the settings stay")
	}
}
