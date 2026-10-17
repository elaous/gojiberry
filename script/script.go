// Package script is the host side of scripting over the command registry: a script a person wrote or pasted runs in a
// sandbox that has nothing of the page (no document, no network, no storage), and can only send requests to the host,
// which answers them here. A request is one JSON message; the host answers the ones the person allowed for this script
// and refuses the rest, so a script reaches the editor through the same commands the menus and key chords do and
// through nothing else. A command added to the registry is therefore reachable at once, without a list kept by hand.
//
// What the requests are:
//
//	{"id":1,"op":"commands"}              the registry: name and title of each command (always allowed)
//	{"id":2,"op":"run","name":"bold"}     run a command on the document (needs Run)
//	{"id":3,"op":"text"}                  the text of the document (needs Read)
//	{"id":4,"op":"log","message":"..."}   a line for the person to read (always allowed)
//	{"id":5,"op":"done"}                  the script has finished (always allowed)
//
// The sandbox itself (an iframe with an opaque origin and a policy that allows no connection) belongs to the page; this
// package is what the page asks. See docs/scripting.md.
package script

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Permission is something a person lets a script do.
type Permission string

const (
	// Read lets the script see the text of the document. It is the one that can show the document to whoever wrote
	// the script, so the page asks for it separately and says so.
	Read Permission = "read"
	// Run lets the script run the editor's commands.
	Run Permission = "run"
)

// Command is a command of the registry as a script sees it.
type Command struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// Host is the editor, as far as a script may reach it.
type Host interface {
	// Commands lists the registry.
	Commands() []Command
	// Run runs a command, reporting whether it applied.
	Run(name string) bool
	// Text is the text of the document.
	Text() string
}

// Limits of one run.
const (
	MaxRequest = 64 << 10 // bytes of one request
	MaxOps     = 2000     // requests in one run
	MaxLog     = 1000     // runes of one log line
	MaxLogs    = 500      // log lines in one run
	MaxText    = 4 << 20  // bytes of document text handed over
)

// Errors a script sees as the text of a refusal.
var (
	ErrDenied   = errors.New("not allowed: the person did not give this script that permission")
	ErrUnknown  = errors.New("unknown request")
	ErrTooMany  = errors.New("this script made too many requests and was stopped")
	ErrTooLarge = errors.New("request or answer too large")
	ErrNoSuch   = errors.New("no such command")
)

// Request is one message from the sandbox.
type Request struct {
	ID      int    `json:"id"`
	Op      string `json:"op"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
}

// Response is the answer to one.
type Response struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Value is the answer: the commands, a flag for run, the text.
	Value any `json:"value,omitempty"`
}

// Session is one run of one script.
type Session struct {
	Host  Host
	Allow map[Permission]bool
	// Log receives the script's log lines (the page shows them). Done is called when the script says it has finished.
	Log  func(line string)
	Done func()

	ops, logs int
	stopped   bool
}

// Stopped says the script went over a limit and its requests are no longer answered.
func (s *Session) Stopped() bool { return s.stopped }

// Handle answers one request (raw JSON) with the JSON of a Response.
func (s *Session) Handle(raw []byte) []byte {
	var req Request
	switch {
	case s.stopped:
		return reply(Response{Error: ErrTooMany.Error()})
	case len(raw) > MaxRequest:
		return reply(Response{Error: ErrTooLarge.Error()})
	case json.Unmarshal(raw, &req) != nil:
		return reply(Response{Error: ErrUnknown.Error()})
	}
	if s.ops++; s.ops > MaxOps {
		s.stopped = true
		return reply(Response{ID: req.ID, Error: ErrTooMany.Error()})
	}
	fail := func(err error) []byte { return reply(Response{ID: req.ID, Error: err.Error()}) }
	ok := func(v any) []byte { return reply(Response{ID: req.ID, OK: true, Value: v}) }
	switch req.Op {
	case "commands":
		return ok(s.Host.Commands())
	case "done":
		if s.Done != nil {
			s.Done()
		}
		return ok(nil)
	case "log":
		if s.logs++; s.logs <= MaxLogs && s.Log != nil {
			m := strings.Join(strings.Fields(req.Message), " ")
			if utf8.RuneCountInString(m) > MaxLog {
				m = string([]rune(m)[:MaxLog]) + "…"
			}
			s.Log(m)
		}
		return ok(nil)
	case "run":
		if !s.Allow[Run] {
			return fail(ErrDenied)
		}
		for _, c := range s.Host.Commands() {
			if c.Name == req.Name {
				return ok(s.Host.Run(req.Name))
			}
		}
		return fail(ErrNoSuch)
	case "text":
		if !s.Allow[Read] {
			return fail(ErrDenied)
		}
		t := s.Host.Text()
		if len(t) > MaxText {
			return fail(ErrTooLarge)
		}
		return ok(t)
	}
	return fail(ErrUnknown)
}

func reply(r Response) []byte {
	b, err := json.Marshal(r)
	if err != nil {
		return []byte(`{"ok":false,"error":"internal"}`)
	}
	return b
}
