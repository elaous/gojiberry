package components

import (
	"strings"
	"time"
	"unicode"

	"github.com/elaous/xango"
)

type TypingAnnouncer struct {
	Level      string
	Formatting bool
	Lines      bool
	Every      time.Duration
	Pause      time.Duration

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

type TypingState struct {
	Text      string
	Caret     int
	Line      string
	LineStart int
	Marks     []string
	Block     string
}

const typingHeld = 6 // the most sentences kept to say together

func (a *TypingAnnouncer) Pending() bool { return len(a.held) > 0 || len(a.word) > 0 }

func (a *TypingAnnouncer) Reset() {
	*a = TypingAnnouncer{Level: a.Level, Formatting: a.Formatting, Lines: a.Lines, Every: a.Every, Pause: a.Pause}
}

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
		} else {
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
		if len(ins) > 1 {
			a.word = nil
			a.say(typingWordAt(is, caret))
		}
	}
}

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

func TypingLive(message string, n int) xango.Node {
	if message != "" && n%2 == 1 {
		message += " "
	}
	return xango.El("div", []xango.Attr{xango.A("id", "typing-live"), xango.A("class", "gj-sr-only"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("aria-atomic", "true")},
		xango.Text(message))
}
