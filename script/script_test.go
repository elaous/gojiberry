package script

import (
	"encoding/json"
	"strings"
	"testing"
)

type host struct {
	ran  []string
	text string
}

func (h *host) Commands() []Command { return []Command{{"bold", "Bold"}, {"italic", "Italic"}} }
func (h *host) Run(n string) bool   { h.ran = append(h.ran, n); return true }
func (h *host) Text() string        { return h.text }

func ask(t *testing.T, s *Session, req string) Response {
	t.Helper()
	var r Response
	if err := json.Unmarshal(s.Handle([]byte(req)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAScriptReachesOnlyWhatItWasAllowed(t *testing.T) {
	h := &host{text: "secret words"}
	var lines []string
	s := &Session{Host: h, Allow: map[Permission]bool{Run: true}, Log: func(l string) { lines = append(lines, l) }}
	if r := ask(t, s, `{"id":1,"op":"commands"}`); !r.OK || r.ID != 1 {
		t.Errorf("commands: %+v", r)
	}
	if r := ask(t, s, `{"id":2,"op":"run","name":"bold"}`); !r.OK || len(h.ran) != 1 || h.ran[0] != "bold" {
		t.Errorf("run: %+v %v", r, h.ran)
	}
	if r := ask(t, s, `{"id":3,"op":"run","name":"rm -rf"}`); r.OK || r.Error != ErrNoSuch.Error() {
		t.Errorf("a name outside the registry: %+v", r)
	}
	if r := ask(t, s, `{"id":4,"op":"text"}`); r.OK || r.Error != ErrDenied.Error() || strings.Contains(r.Error, "secret") {
		t.Errorf("text without Read: %+v", r)
	}
	ask(t, s, `{"id":5,"op":"log","message":"  two\n words "}`)
	if len(lines) != 1 || lines[0] != "two words" {
		t.Errorf("log: %q", lines)
	}
	finished := false
	s.Done = func() { finished = true }
	if r := ask(t, s, `{"id":9,"op":"done"}`); !r.OK || !finished {
		t.Errorf("done: %+v", r)
	}
	s2 := &Session{Host: h, Allow: map[Permission]bool{Read: true}}
	if r := ask(t, s2, `{"id":6,"op":"text"}`); !r.OK || r.Value != "secret words" {
		t.Errorf("text with Read: %+v", r)
	}
	if r := ask(t, s2, `{"id":7,"op":"run","name":"bold"}`); r.OK || len(h.ran) != 1 {
		t.Errorf("run without Run: %+v", r)
	}
}

func TestBadRequestsAndLimits(t *testing.T) {
	s := &Session{Host: &host{}, Allow: map[Permission]bool{Run: true}}
	for _, raw := range []string{`not json`, `{"id":1,"op":"eval"}`, `{"id":1}`} {
		if r := ask(t, s, raw); r.OK {
			t.Errorf("%q was answered", raw)
		}
	}
	if r := ask(t, s, `{"id":1,"op":"log","message":"`+strings.Repeat("x", MaxRequest)+`"}`); r.OK || r.Error != ErrTooLarge.Error() {
		t.Errorf("large: %+v", r)
	}
	var lines []string
	s = &Session{Host: &host{}, Log: func(l string) { lines = append(lines, l) }}
	ask(t, s, `{"id":1,"op":"log","message":"`+strings.Repeat("y", 3000)+`"}`)
	if n := len([]rune(lines[0])); n != MaxLog+1 {
		t.Errorf("a log line is cut: %d runes", n)
	}
	s = &Session{Host: &host{}}
	for i := 0; i < MaxOps+5; i++ {
		ask(t, s, `{"id":1,"op":"commands"}`)
	}
	if r := ask(t, s, `{"id":1,"op":"commands"}`); r.OK || !s.Stopped() {
		t.Error("a script that makes too many requests is stopped")
	}
}
