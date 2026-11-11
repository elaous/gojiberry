package speechcoach

import (
	"encoding/json"
	"errors"
	"time"
)

// Status is where a phrase stands in a session.
type Status string

const (
	Pending  Status = "pending"
	Recorded Status = "recorded"
	Skipped  Status = "skipped"
)

// Session is one sitting through a phrase set.
type Session struct {
	set   Set
	cur   int
	items []item
}

type item struct {
	status   Status
	pcm      []int16
	quality  Quality
	hints    []string
	attempts int
	at       time.Time
}

// NewSession starts at the first phrase of set.
func NewSession(set Set) *Session {
	s := &Session{set: set, items: make([]item, len(set.Phrases))}
	for i := range s.items {
		s.items[i].status = Pending
	}
	return s
}

// Set is the phrase set of the session.
func (s *Session) Set() Set { return s.set }

// Len is the number of phrases.
func (s *Session) Len() int { return len(s.items) }

// Index is the position of the current phrase, from 0.
func (s *Session) Index() int { return s.cur }

// Phrase is the current phrase ("" for an empty set).
func (s *Session) Phrase() string {
	if s.cur < len(s.set.Phrases) {
		return s.set.Phrases[s.cur]
	}
	return ""
}

func (s *Session) Status(i int) Status {
	if i < 0 || i >= len(s.items) {
		return Pending
	}
	return s.items[i].status
}

// Take is what is kept for one phrase.
type Take struct {
	Phrase   int
	Text     string
	PCM      []int16
	Quality  Quality
	Hints    []string
	Attempts int // how many times the phrase was recorded
	At       time.Time
}

func (s *Session) Current() (t Take, ok bool) { return s.Take(s.cur) }

// Take is the take of phrase i.
func (s *Session) Take(i int) (Take, bool) {
	if i < 0 || i >= len(s.items) || s.items[i].status != Recorded {
		return Take{}, false
	}
	it := s.items[i]
	return Take{Phrase: i, Text: s.set.Phrases[i], PCM: it.pcm, Quality: it.quality, Hints: it.hints, Attempts: it.attempts, At: it.at}, true
}

var ErrNoPhrase = errors.New("speechcoach: no phrase")

// ErrEmptyTake is returned for a recording with no samples.
var ErrEmptyTake = errors.New("speechcoach: nothing was recorded")

func (s *Session) Record(pcm []int16, at time.Time) (Take, error) {
	if s.cur >= len(s.items) {
		return Take{}, ErrNoPhrase
	}
	if len(pcm) == 0 {
		return Take{}, ErrEmptyTake
	}
	if len(pcm) > MaxSamples {
		pcm = pcm[:MaxSamples]
	}
	it := &s.items[s.cur]
	q := Analyze(pcm)
	it.status, it.pcm, it.quality, it.hints, it.at = Recorded, append([]int16(nil), pcm...), q, q.Hints(), at
	it.attempts++
	t, _ := s.Current()
	return t, nil
}

func (s *Session) Discard() {
	if s.cur < len(s.items) {
		s.items[s.cur].status, s.items[s.cur].pcm, s.items[s.cur].hints = Pending, nil, nil
		s.items[s.cur].quality = Quality{}
	}
}

func (s *Session) Skip() {
	if s.cur >= len(s.items) {
		return
	}
	if s.items[s.cur].status == Pending {
		s.items[s.cur].status = Skipped
	}
	s.Next()
}

func (s *Session) Next() bool {
	n := len(s.items)
	for _, want := range []Status{Pending, Skipped} {
		for i := 1; i < n; i++ {
			if j := (s.cur + i) % n; s.items[j].status == want {
				s.cur = j
				return true
			}
		}
	}
	if s.cur+1 < n {
		s.cur++
		return true
	}
	return false
}

// Prev goes back one phrase; it reports whether it moved.
func (s *Session) Prev() bool {
	if s.cur > 0 {
		s.cur--
		return true
	}
	return false
}

func (s *Session) Goto(i int) bool {
	if i < 0 || i >= len(s.items) {
		return false
	}
	s.cur = i
	return true
}

// Progress counts the phrases by status and the recordings made.
type Progress struct {
	Total     int `json:"total"`
	Recorded  int `json:"recorded"`
	Skipped   int `json:"skipped"`
	Remaining int `json:"remaining"` // pending phrases
	Attempts  int `json:"attempts"`  // recordings made, retakes included
}

// Progress is the per-session progress record.
func (s *Session) Progress() Progress {
	p := Progress{Total: len(s.items)}
	for _, it := range s.items {
		p.Attempts += it.attempts
		switch it.status {
		case Recorded:
			p.Recorded++
		case Skipped:
			p.Skipped++
		default:
			p.Remaining++
		}
	}
	return p
}

func (s *Session) Finished() bool { return s.Progress().Remaining == 0 }

// Takes lists the kept takes in phrase order.
func (s *Session) Takes() []Take {
	var out []Take
	for i := range s.items {
		if t, ok := s.Take(i); ok {
			out = append(out, t)
		}
	}
	return out
}

type ProgressRecord struct {
	SetID    string   `json:"set"`
	Cur      int      `json:"cur"`
	Status   []Status `json:"status"`
	Attempts []int    `json:"attempts"`
}

// Marshal saves the progress.
func (s *Session) Marshal() ([]byte, error) {
	r := ProgressRecord{SetID: s.set.ID, Cur: s.cur}
	for _, it := range s.items {
		r.Status = append(r.Status, it.status)
		r.Attempts = append(r.Attempts, it.attempts)
	}
	return json.Marshal(r)
}

func Resume(set Set, saved []byte, d *Dataset) *Session {
	s := NewSession(set)
	var r ProgressRecord
	if json.Unmarshal(saved, &r) != nil || r.SetID != set.ID || len(r.Status) != len(set.Phrases) || len(r.Attempts) != len(set.Phrases) {
		return s
	}
	for i, st := range r.Status {
		switch st {
		case Skipped:
			s.items[i].status = Skipped
		case Recorded:
			if d == nil {
				continue
			}
			if e := d.ByPrompt(set.Phrases[i]); e != nil {
				if pcm, err := DecodeWAV(e.WAV); err == nil {
					q := Analyze(pcm)
					s.items[i] = item{status: Recorded, pcm: pcm, quality: q, hints: q.Hints(), attempts: r.Attempts[i], at: e.Created}
				}
			}
		}
		if s.items[i].attempts < r.Attempts[i] && r.Attempts[i] >= 0 && r.Attempts[i] < 1000 {
			s.items[i].attempts = r.Attempts[i]
		}
	}
	if r.Cur >= 0 && r.Cur < len(set.Phrases) {
		s.cur = r.Cur
	}
	return s
}
