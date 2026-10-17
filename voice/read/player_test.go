package read

import (
	"errors"
	"testing"
)

// fakeEngine says what it is told by hand: tests finish an utterance when they choose.
type fakeEngine struct {
	avail  bool
	spoken []Utterance
	hs     []Handler
	sps    []*fakeSpeech
	sync   bool // finish each utterance as soon as it starts
}

type fakeSpeech struct{ cancelled, paused bool }

func (s *fakeSpeech) Cancel() { s.cancelled = true }
func (s *fakeSpeech) Pause()  { s.paused = true }
func (s *fakeSpeech) Resume() { s.paused = false }

func (f *fakeEngine) ID() string      { return "fake" }
func (f *fakeEngine) Label() string   { return "Fake" }
func (f *fakeEngine) Available() bool { return f.avail }
func (f *fakeEngine) Voices() []Voice { return nil }
func (f *fakeEngine) Speak(u Utterance, h Handler) Speech {
	f.spoken, f.hs = append(f.spoken, u), append(f.hs, h)
	s := &fakeSpeech{}
	f.sps = append(f.sps, s)
	if f.sync && h.OnEnd != nil {
		h.OnEnd()
	}
	return s
}

func chunksOf(texts ...string) []Chunk {
	var out []Chunk
	for _, t := range texts {
		out = append(out, Chunk{Text: t, Lang: "en"})
	}
	return out
}

func TestPlayerSaysThePiecesInOrderAndStops(t *testing.T) {
	e := &fakeEngine{avail: true}
	var pieces []string
	stops := 0
	p := &Player{Engine: e, Voice: func(lang string) string { return "v-" + lang }, Rate: func() float64 { return 1.5 },
		OnPiece: func(c Chunk, reveal bool) { pieces = append(pieces, c.Text) }, OnStop: func() { stops++ }}
	p.Start(chunksOf("One.", "Two.", "Three."))
	if len(e.spoken) != 1 || e.spoken[0].Text != "One." || e.spoken[0].Voice != "v-en" || e.spoken[0].Rate != 1.5 {
		t.Fatalf("first: %+v", e.spoken)
	}
	e.hs[0].OnStart(KindDownloaded)
	if p.Kind() != KindDownloaded {
		t.Fatalf("kind %q", p.Kind())
	}
	e.hs[0].OnEnd()
	e.hs[1].OnEnd()
	if len(e.spoken) != 3 || !p.Playing() {
		t.Fatalf("spoken %d playing %v", len(e.spoken), p.Playing())
	}
	e.hs[2].OnEnd()
	if p.Playing() || stops != 1 || len(pieces) != 3 {
		t.Fatalf("done: playing %v stops %d pieces %v", p.Playing(), stops, pieces)
	}
}

func TestPlayerPauseSkipAndLateCallbacks(t *testing.T) {
	e := &fakeEngine{avail: true}
	p := &Player{Engine: e}
	p.Start(chunksOf("A.", "B.", "C."))
	p.Pause()
	if !p.Paused() || !e.sps[0].paused {
		t.Fatal("pause holds the speech")
	}
	p.Pause()
	if p.Paused() || e.sps[0].paused {
		t.Fatal("pause again continues")
	}
	p.Skip(1)
	if !e.sps[0].cancelled || len(e.spoken) != 2 || e.spoken[1].Text != "B." {
		t.Fatalf("skip cancels and moves on: %+v", e.spoken)
	}
	e.hs[0].OnEnd() // the cancelled utterance ends late: ignored
	if len(e.spoken) != 2 {
		t.Fatalf("a late end of a cancelled utterance moved on: %d", len(e.spoken))
	}
	p.Skip(-5)
	if e.spoken[2].Text != "A." {
		t.Fatalf("skip back stops at the first: %+v", e.spoken)
	}
	p.Stop()
	if p.Playing() || !e.sps[2].cancelled {
		t.Fatal("stop cancels")
	}
	e.hs[2].OnEnd()
	if len(e.spoken) != 3 {
		t.Fatal("nothing goes on after stop")
	}
}

func TestPlayerWordMarksAndErrors(t *testing.T) {
	e := &fakeEngine{avail: true}
	var from, to int
	var gotErr error
	p := &Player{Engine: e, OnWord: func(c Chunk, f, t int) { from, to = f, t }, OnError: func(err error) { gotErr = err }}
	c := Chunk{Text: "Hello world", Offsets: []int{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}}
	p.Start([]Chunk{c, {Text: "Next"}})
	e.hs[0].OnWord(6, 5)
	if from != 16 || to != 21 {
		t.Fatalf("word range %d-%d", from, to)
	}
	e.hs[0].OnError(errors.New("no voice"))
	if p.Playing() || gotErr == nil || len(e.spoken) != 1 {
		t.Fatalf("an error stops the reading: playing %v err %v", p.Playing(), gotErr)
	}
}

func TestPlayerWithAnEngineThatAnswersAtOnceAndNoEngine(t *testing.T) {
	e := &fakeEngine{avail: true, sync: true}
	p := &Player{Engine: e}
	p.Start(chunksOf("A.", "B.", "C."))
	if len(e.spoken) != 3 || p.Playing() {
		t.Fatalf("spoken %d playing %v", len(e.spoken), p.Playing())
	}
	off := &Player{Engine: &fakeEngine{}}
	off.Start(chunksOf("A."))
	if off.Playing() {
		t.Fatal("an engine that is not available does not start")
	}
}
