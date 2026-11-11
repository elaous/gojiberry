package canvas

import (
	"reflect"
	"testing"
)

type spy struct {
	name  string
	log   *[]string
	mode  Mode
	down  bool // result MouseDown returns
	key   bool // result HandleKey returns
	focus bool
	css   string
}

func (s *spy) Mode() Mode              { return s.mode }
func (s *spy) SetMode(m Mode) bool     { s.mode = m; return true }
func (s *spy) Draw()                   { *s.log = append(*s.log, s.name+":draw") }
func (s *spy) SetPainter(ImagePainter) { *s.log = append(*s.log, s.name+":painter") }
func (s *spy) MouseDown(x, y float64) bool {
	*s.log = append(*s.log, s.name+":down")
	return s.down
}
func (s *spy) MouseMove(x, y float64) bool { *s.log = append(*s.log, s.name+":move"); return false }
func (s *spy) MouseUp(x, y float64) bool   { *s.log = append(*s.log, s.name+":up"); return true }
func (s *spy) MouseLeave() bool            { *s.log = append(*s.log, s.name+":leave"); return false }
func (s *spy) HandleKey(k string, shift bool) bool {
	*s.log = append(*s.log, s.name+":key")
	return s.key
}
func (s *spy) InsertText(string) bool { *s.log = append(*s.log, s.name+":text"); return s.key }
func (s *spy) WantsFocus() bool       { return s.focus }
func (s *spy) CSSCursor() string      { return s.css }

type plain struct{ drawn *int }

func (plain) Mode() Mode        { return ModeView }
func (plain) SetMode(Mode) bool { return false }
func (p plain) Draw()           { *p.drawn++ }

func TestStageDrawClearsThenDrawsBackToFront(t *testing.T) {
	var log []string
	p := &fakePainter{}
	s := NewStage(p, 300, 200, &spy{name: "a", log: &log}, &spy{name: "b", log: &log})
	log = nil
	s.Draw()
	if !reflect.DeepEqual(log, []string{"a:draw", "b:draw"}) {
		t.Fatalf("log = %v", log)
	}
	if p.calls[0] != "clearRect 0 0 300 200" {
		t.Fatalf("first call = %q", p.calls[0])
	}
}

func TestStageGivesComponentsThePainter(t *testing.T) {
	var log []string
	s := NewStage(&fakePainter{}, 1, 1, &spy{name: "a", log: &log})
	s.Add(&spy{name: "b", log: &log})
	s.SetPainter(&fakePainter{})
	if !reflect.DeepEqual(log, []string{"a:painter", "b:painter", "a:painter", "b:painter"}) {
		t.Fatalf("log = %v", log)
	}
}

func TestStageDispatchesPointerEventsTopmostFirstToEveryone(t *testing.T) {
	var log []string
	a := &spy{name: "a", log: &log}
	b := &spy{name: "b", log: &log, down: true}
	s := NewStage(nil, 1, 1, a, b, plain{new(int)})
	log = nil
	if !s.MouseDown(1, 2) {
		t.Fatal("b asked for a redraw")
	}
	if !reflect.DeepEqual(log, []string{"b:down", "a:down"}) {
		t.Fatalf("log = %v", log)
	}
	if s.MouseMove(0, 0) || s.MouseLeave() {
		t.Fatal("nobody changed")
	}
	if !s.MouseUp(0, 0) {
		t.Fatal("up reports a change")
	}
}

func TestStageKeysStopAtFirstConsumer(t *testing.T) {
	var log []string
	a := &spy{name: "a", log: &log, key: true}
	b := &spy{name: "b", log: &log}
	s := NewStage(nil, 1, 1, a, b)
	if !s.HandleKey("x", false) || !reflect.DeepEqual(log, []string{"b:key", "a:key"}) {
		t.Fatalf("log = %v", log)
	}
	log = nil
	a.key = false
	if s.HandleKey("x", false) || s.InsertText("q") {
		t.Fatal("nobody consumed")
	}
}

func TestStageFocusAndCursor(t *testing.T) {
	var log []string
	a := &spy{name: "a", log: &log, css: "text"}
	b := &spy{name: "b", log: &log}
	s := NewStage(nil, 1, 1, a, b, plain{new(int)})
	if s.WantsFocus() {
		t.Fatal("nobody wants focus")
	}
	a.focus = true
	if !s.WantsFocus() {
		t.Fatal("a wants focus")
	}
	if s.Cursor() != "text" {
		t.Fatalf("cursor = %q", s.Cursor())
	}
	b.css = "move"
	if s.Cursor() != "move" {
		t.Fatal("topmost opinion wins")
	}
	a.css, b.css = "", ""
	if s.Cursor() != "default" {
		t.Fatal("default")
	}
}

func TestStageSetModeAndCaret(t *testing.T) {
	var log []string
	a := &spy{name: "a", log: &log}
	s := NewStage(nil, 1, 1, a)
	s.SetMode(ModeEdit)
	if a.Mode() != ModeEdit {
		t.Fatal("mode not forwarded")
	}
	if s.SetCaretVisible(true) {
		t.Fatal("no Blinker, no change")
	}
}

func TestStageDrivesRealComponentsTogether(t *testing.T) {
	p := &fakePainter{}
	flow := NewTextFlow("hello world", 10, 40, 300, 100)
	im := NewImage(fakeImage{50, 50}, 100, 50)
	im.SetFlow(flow)
	tv := NewTableView(NewTable(1, 1), nil)
	s := NewStage(p, 500, 400, flow, im, tv)
	s.SetMode(ModeEdit)
	if tv.Painter == nil || im.Painter == nil || flow.Painter == nil {
		t.Fatal("painters not distributed")
	}
	if !s.MouseDown(120, 70) || !im.Dragging() {
		t.Fatal("image should start dragging")
	}
	s.MouseMove(145, 70)
	s.MouseUp(145, 70)
	if im.X != 125 {
		t.Fatalf("X = %g", im.X)
	}
	s.Draw()
	if !p.has("drawImage") || !p.has(`fillText "hello world"`) {
		t.Fatalf("components not drawn: %d calls", len(p.calls))
	}
}

func TestStageOnDraw(t *testing.T) {
	n := 0
	s := NewStage(&fakePainter{}, 1, 1)
	s.OnDraw = func() { n++ }
	s.Draw()
	s.Draw()
	if n != 2 {
		t.Fatalf("OnDraw called %d times", n)
	}
}
