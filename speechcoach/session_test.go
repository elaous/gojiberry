package speechcoach

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

func threeSet() Set {
	return Set{ID: "t", Lang: "en", Title: "t", Phrases: []string{"one", "two", "three"}}
}

func TestParseSet(t *testing.T) {
	s, err := ParseSet("mine", "en", "Mine", "# comment\n\nHello there\nhello THERE\n  Good\x00 night \n")
	if err != nil || !slices.Equal(s.Phrases, []string{"Hello there", "Good night"}) {
		t.Fatalf("%v %v", s.Phrases, err)
	}
	for name, text := range map[string]string{
		"empty":   "# only a comment\n\n",
		"long":    strings.Repeat("x", MaxPhraseLen+1),
		"invalid": "bad \xff\xfe",
		"many":    manyLines(MaxPhrases + 1),
	} {
		if _, err := ParseSet("x", "en", "x", text); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func manyLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "phrase %d\n", i)
	}
	return b.String()
}

func TestBuiltinSetsAreUsable(t *testing.T) {
	langs := map[string]bool{}
	for _, s := range Builtin() {
		langs[s.Lang] = true
		if len(s.Phrases) < 10 {
			t.Errorf("%s has %d phrases", s.ID, len(s.Phrases))
		}
		again, err := ParseSet(s.ID, s.Lang, s.Title, strings.Join(s.Phrases, "\n"))
		if err != nil || len(again.Phrases) != len(s.Phrases) {
			t.Errorf("%s does not survive its own rules: %v", s.ID, err)
		}
	}
	if len(langs) != 4 || SetFor("de-AT").Lang != "de" || SetFor("FR_ca").Lang != "fr" || SetFor("ja").Lang != "en" {
		t.Errorf("languages %v", langs)
	}
}

func TestSessionRecordRetakeSkipAndFinish(t *testing.T) {
	s := NewSession(threeSet())
	if s.Phrase() != "one" || s.Finished() {
		t.Fatal("starts on the first phrase")
	}
	if _, err := s.Record(nil, t0); err != ErrEmptyTake {
		t.Fatalf("an empty take: %v", err)
	}
	take, err := s.Record(tone(2, 0.4, 0.002), t0)
	if err != nil || len(take.Hints) != 0 || take.Attempts != 1 || s.Index() != 0 {
		t.Fatalf("a good take keeps the position and has no hints: %+v %v", take, err)
	}
	// a retake replaces the take and counts; a faint one gets a hint but is kept
	take, _ = s.Record(tone(2, 0.01, 0.0001), t0.Add(time.Minute))
	if take.Attempts != 2 || !slices.Contains(take.Hints, HintTooQuiet) || s.Status(0) != Recorded {
		t.Fatalf("retake: %+v", take)
	}
	if got, _ := s.Current(); !got.At.Equal(t0.Add(time.Minute)) {
		t.Error("the retake is the one kept")
	}
	if !s.Next() || s.Phrase() != "two" {
		t.Fatalf("next: %q", s.Phrase())
	}
	s.Skip()
	if s.Status(1) != Skipped || s.Phrase() != "three" {
		t.Fatalf("skip moves on: %q %v", s.Phrase(), s.Status(1))
	}
	if _, err := s.Record(tone(1, 0.4, 0.002), t0); err != nil {
		t.Fatal(err)
	}
	p := s.Progress()
	if p != (Progress{Total: 3, Recorded: 2, Skipped: 1, Remaining: 0, Attempts: 3}) {
		t.Fatalf("progress %+v", p)
	}
	if !s.Finished() {
		t.Error("finished with one skipped: skipping is allowed")
	}
	// next from the end offers the skipped phrase again; the person may also go back and discard
	if !s.Next() || s.Phrase() != "two" {
		t.Errorf("a skipped phrase is offered again: %q", s.Phrase())
	}
	if !s.Prev() || s.Phrase() != "one" || s.Prev() {
		t.Error("back, and no further back than the first")
	}
	s.Discard()
	if s.Status(0) != Pending || s.Progress().Remaining != 1 || len(s.Takes()) != 1 {
		t.Errorf("discard: %+v", s.Progress())
	}
	if s.Goto(7) || !s.Goto(2) || s.Phrase() != "three" {
		t.Error("goto")
	}
}

func TestNextPrefersPendingAndWraps(t *testing.T) {
	s := NewSession(threeSet())
	s.Goto(2)
	if !s.Next() || s.Index() != 0 {
		t.Fatalf("from the last, wraps to the first pending: %d", s.Index())
	}
	_, _ = s.Record(tone(1, 0.4, 0.002), t0)
	s.Goto(2)
	_, _ = s.Record(tone(1, 0.4, 0.002), t0)
	s.Goto(0)
	if !s.Next() || s.Index() != 1 {
		t.Fatalf("pending phrase 1: %d", s.Index())
	}
	_, _ = s.Record(tone(1, 0.4, 0.002), t0)
	if s.Next() && s.Index() != 2 || !s.Finished() {
		t.Errorf("all recorded: stays moving by one: %d", s.Index())
	}
	if NewSession(Set{}).Next() || NewSession(Set{}).Phrase() != "" {
		t.Error("an empty set")
	}
	if _, err := NewSession(Set{}).Record(tone(1, 0.4, 0), t0); err != ErrNoPhrase {
		t.Errorf("no phrase: %v", err)
	}
}

func TestResumeFromProgressAndDataset(t *testing.T) {
	set := threeSet()
	s := NewSession(set)
	var d Dataset
	take, _ := s.Record(tone(1.5, 0.4, 0.002), t0)
	d.Add(NewEntry("en", "one", "one", Prompted, take.PCM, take.Hints, t0))
	s.Next()
	s.Skip()
	saved, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	r := Resume(set, saved, &d)
	if r.Status(0) != Recorded || r.Status(1) != Skipped || r.Status(2) != Pending || r.Index() != s.Index() {
		t.Fatalf("resume: %v %v %v at %d", r.Status(0), r.Status(1), r.Status(2), r.Index())
	}
	if got, ok := r.Take(0); !ok || len(got.PCM) != len(take.PCM) || got.Attempts != 1 {
		t.Error("the take comes back from the dataset")
	}
	// the take is gone from the dataset: the phrase is pending again; a record of another set is ignored
	if r := Resume(set, saved, &Dataset{}); r.Status(0) != Pending {
		t.Error("no audio, no recorded status")
	}
	other := set
	other.ID = "other"
	if r := Resume(other, saved, &d); r.Status(1) != Pending {
		t.Error("another set starts fresh")
	}
	if r := Resume(set, []byte("{broken"), &d); r.Progress().Remaining != 3 {
		t.Error("damaged progress starts fresh")
	}
}

func TestLongTakeIsCutNotRefused(t *testing.T) {
	s := NewSession(threeSet())
	take, err := s.Record(make([]int16, MaxSamples+SampleRate), t0)
	if err != nil || len(take.PCM) != MaxSamples {
		t.Fatalf("%d %v", len(take.PCM), err)
	}
}
