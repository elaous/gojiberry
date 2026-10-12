package components

import (
	"strings"
	"time"
	"unicode"

	"github.com/elaous/gojiberry/xango"
)

// A canvas gives a screen reader nothing to say when a person types, or moves the caret: the keys go to a
// hidden input whose text is not the document. TypingAnnouncer turns what changed in the document into
// short sentences for a live region (TypingLive): the words as they are finished, or each character; the
// line the caret has gone to; a formatting change at the caret ("Bold on"). It is a pure state machine: the
// host gives it the document as it is after each change (Update) and says what it returns. What it says is
// spaced (at most one sentence per Every, the rest held and said together) and bounded, so quick typing is
// not read as a queue that runs on after the person has stopped.
//
// Preferences: Level (off, words, characters), Formatting and Lines, each on its own, because what a
// person wants differs, and a screen reader may already echo what the hidden input receives.
type TypingAnnouncer struct {
	// Level is what is said of typed text: TypingOff nothing (the default), TypingWords each word when it
	// is finished (and a word left unfinished for Pause), TypingCharacters each character as it is typed.
	Level string
	// Formatting says changes of the formatting at the caret: "Bold on", "Bold off", and the paragraph
	// style ("Heading 2") when the caret goes into another.
	Formatting bool
	// Lines is reading as the caret moves: the line it goes to when it changes line (the arrow keys up and
	// down, a click). Within a line, TypingCharacters says the character passed over, and either level
	// the text passed over by a move of more than one character (Ctrl+Left or Ctrl+Right).
	Lines bool
	// Every is the least time between two sentences (500 ms if 0). What comes sooner is held and said
	// together at the next Update after it.
	Every time.Duration
	// Pause is how long a word being typed may stand unfinished before it is said (1.5 s if 0).
	Pause time.Duration

	started bool
	state   TypingState
	word    []rune
	wordAt  time.Time
	held    []string
	last    time.Time
}

// The levels of TypingAnnouncer.Level.
const (
	TypingOff        = ""
	TypingWords      = "words"
	TypingCharacters = "characters"
)

// TypingState is the document as the announcer needs it, after a change.
type TypingState struct {
	// Text is the editor's text and Caret the offset of the (first) caret in it, in runes.
	Text  string
	Caret int
	// Line is the text of the line the caret is on (as drawn, so a wrapped line is the part on screen) and
	// LineStart its offset in Text; two lines with the same text are told apart by the offset.
	Line      string
	LineStart int
	// Marks are the names of the formatting at the caret ("Bold", "Italic") and Block the name of the
	// paragraph style ("Heading 2"; "" for plain text).
	Marks []string
	Block string
}

const typingHeld = 6 // the most sentences kept to say together

// Pending reports whether something is held (or a word is unfinished), to be said by a later Update: the
// host calls Update again after Every.
func (a *TypingAnnouncer) Pending() bool { return len(a.held) > 0 || len(a.word) > 0 }

// Reset forgets the document and what is held: the next Update only remembers (use it when the editing
// stops, or the document is replaced, so the whole text is not taken for a paste).
func (a *TypingAnnouncer) Reset() {
	*a = TypingAnnouncer{Level: a.Level, Formatting: a.Formatting, Lines: a.Lines, Every: a.Every, Pause: a.Pause}
}

// Update takes the document as it is now and returns the sentence to say now, or "".
func (a *TypingAnnouncer) Update(now time.Time, cur TypingState) string {
	prev := a.state
	a.state = cur
	if !a.started {
		a.started = true
		return ""
	}
	if a.Level == TypingOff && !a.Formatting && !a.Lines {
		a.word, a.held = nil, nil
		return ""
	}
	if cur.Text != prev.Text {
		a.edited(now, []rune(prev.Text), []rune(cur.Text), cur.Caret)
	} else if cur.Caret != prev.Caret {
		a.word = nil // the caret left the word
		a.moved(prev, cur)
	}
	if a.Formatting {
		a.formatted(prev, cur)
	}
	if len(a.word) > 0 && now.Sub(a.wordAt) >= orDuration(a.Pause, 1500*time.Millisecond) {
		a.say(string(a.word))
		a.word = nil
	}
	if len(a.held) == 0 || now.Sub(a.last) < orDuration(a.Every, 500*time.Millisecond) {
		return ""
	}
	out := strings.Join(a.held, " ")
	a.held, a.last = nil, now
	return out
}

func orDuration(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (a *TypingAnnouncer) say(s string) {
	if s == "" {
		return
	}
	a.held = append(a.held, s)
	if len(a.held) > typingHeld {
		a.held = a.held[len(a.held)-typingHeld:]
	}
}

// edited is what was typed, deleted or pasted: the part of the text that differs, if the caret is where a
// person's own edit would leave it (a change made elsewhere, by another person, is not said).
func (a *TypingAnnouncer) edited(now time.Time, was, is []rune, caret int) {
	p := 0
	for p < len(was) && p < len(is) && was[p] == is[p] {
		p++
	}
	s := 0
	for s < len(was)-p && s < len(is)-p && was[len(was)-1-s] == is[len(is)-1-s] {
		s++
	}
	del, ins := was[p:len(was)-s], is[p:len(is)-s]
	if caret != p+len(ins) {
		a.word = nil
		return
	}
	if len(ins) == 0 {
		if a.Level == TypingCharacters {
			if len(del) == 1 {
				a.say(trf("typing.deleted", "Deleted %s", typingChar(del[0])))
			} else {
				a.say(trn("typing.deletedMany", "Deleted %d character", "Deleted %d characters", len(del)))
			}
		}
		if len(a.word) > 0 {
			if len(del) >= len(a.word) {
				a.word = nil
			} else {
				a.word = a.word[:len(a.word)-len(del)]
			}
		}
		return
	}
	switch {
	case len(ins) > 40:
		a.word = nil
		a.say(trn("typing.pasted", "Pasted %d character", "Pasted %d characters", len(ins)))
	case a.Level == TypingCharacters:
		if len(ins) == 1 {
			a.say(typingChar(ins[0]))
		} else { // several at once: a suggestion taken, a paste, a replacement: say them as they are
			a.say(strings.TrimSpace(string(ins)))
		}
	case a.Level == TypingWords:
		for _, r := range ins {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || (r == '-' && len(a.word) > 0) {
				a.word = append(a.word, r)
				a.wordAt = now
				continue
			}
			if len(a.word) > 0 {
				a.say(string(a.word))
				a.word = nil
			}
		}
		if len(ins) > 1 { // a whole piece at once (a word taken from the list) is said with the word it is in
			a.word = nil
			a.say(typingWordAt(is, caret))
		}
	}
}

// moved is the caret going somewhere with the text as it was.
func (a *TypingAnnouncer) moved(prev, cur TypingState) {
	if a.Lines && cur.LineStart != prev.LineStart {
		if strings.TrimSpace(cur.Line) == "" {
			a.say(tr("typing.blank", "blank"))
		} else {
			a.say(strings.TrimSpace(cur.Line))
		}
		return
	}
	if a.Level == TypingOff || !a.Lines {
		return
	}
	text := []rune(cur.Text)
	from, to := min(prev.Caret, cur.Caret), max(prev.Caret, cur.Caret)
	from, to = max(from, 0), min(to, len(text))
	if from >= to {
		return
	}
	switch {
	case to-from == 1 && a.Level == TypingCharacters:
		a.say(typingChar(text[from]))
	case to-from > 1:
		if span := strings.TrimSpace(string(text[from:to])); span != "" && to-from <= 60 {
			a.say(span)
		}
	}
}

// formatted says what changed in the formatting at the caret.
func (a *TypingAnnouncer) formatted(prev, cur TypingState) {
	for _, m := range cur.Marks {
		if !typingHas(prev.Marks, m) {
			a.say(trf("typing.markOn", "%s on", m))
		}
	}
	for _, m := range prev.Marks {
		if !typingHas(cur.Marks, m) {
			a.say(trf("typing.markOff", "%s off", m))
		}
	}
	if cur.Block != prev.Block && cur.Block != "" {
		a.say(cur.Block)
	}
}

func typingHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// typingWordAt is the word the offset is in or at the end of ("" if there is none).
func typingWordAt(text []rune, at int) string {
	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-'
	}
	from, to := min(at, len(text)), min(at, len(text))
	for from > 0 && isWord(text[from-1]) {
		from--
	}
	for to < len(text) && isWord(text[to]) {
		to++
	}
	return string(text[from:to])
}

// typingChar is a character as a person would hear it said: a space, a line break and a tab are named,
// the rest is itself.
func typingChar(r rune) string {
	switch r {
	case ' ', ' ':
		return tr("typing.space", "space")
	case '\n':
		return tr("typing.newLine", "new line")
	case '\t':
		return tr("typing.tab", "tab")
	}
	return string(r)
}

// TypingLive is the live region TypingAnnouncer's sentences go to: polite, so it waits for what the reader
// is saying, and separate from Announcer, whose messages (the mode, the number of matches) must not be
// replaced by a typed word. n counts the sentences said: a sentence said twice in a row ("a", "a") is
// still a change of the region, so the reader says it again.
func TypingLive(message string, n int) xango.Node {
	if message != "" && n%2 == 1 {
		message += " "
	}
	return xango.El("div", []xango.Attr{xango.A("id", "typing-live"), xango.A("class", "gj-sr-only"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("aria-atomic", "true")},
		xango.Text(message))
}
