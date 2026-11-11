package console

import (
	"context"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/components"
)

const MaxLines = 500

type Session struct {
	Shell     *Shell
	Files     *PageFiles
	PickFiles func(add func(name string, data []byte))
	Before    func()
	OnDone    func(err error)
	// possibly from another goroutine
	OnChange func()
	Pending  *components.Pending

	mu       sync.Mutex
	lines    []components.ConsoleLine
	partial  map[string]string // kind -> text of a line not finished yet
	input    string
	history  []string
	hpos     int
	question string
	answer   chan bool
	runCtx   context.Context
	prompt   string // the folder being worked in, as a path
}

// NewSession makes a Session over sh.
func NewSession(sh *Shell, deliver func(name string, data []byte)) *Session {
	s := &Session{Shell: sh, Files: &PageFiles{Deliver: deliver}, partial: map[string]string{}}
	s.Pending = components.NewPending(s.changed)
	s.prompt = "/"
	sh.Files = s.Files
	sh.Out = &lineWriter{s: s, kind: "out"}
	sh.Err = &lineWriter{s: s, kind: "err"}
	sh.Confirm = s.confirm
	return s
}

func (s *Session) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

type lineWriter struct {
	s    *Session
	kind string
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	text := w.s.partial[w.kind] + string(p)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		w.s.addLocked(w.kind, text[:i])
		text = text[i+1:]
	}
	w.s.partial[w.kind] = text
	w.s.mu.Unlock()
	w.s.changed()
	return len(p), nil
}

func (s *Session) addLocked(kind, text string) {
	s.lines = append(s.lines, components.ConsoleLine{Text: text, Kind: kind})
	if len(s.lines) > MaxLines {
		s.lines = append([]components.ConsoleLine(nil), s.lines[len(s.lines)-MaxLines:]...)
	}
}

func (s *Session) flushLocked() {
	for _, kind := range []string{"out", "err"} {
		if t := s.partial[kind]; t != "" {
			s.addLocked(kind, t)
			s.partial[kind] = ""
		}
	}
}

func (s *Session) confirm(question string) bool {
	s.mu.Lock()
	ch := make(chan bool, 1)
	s.question, s.answer = question, ch
	ctx := s.runCtx
	s.mu.Unlock()
	s.changed()
	var yes bool
	select {
	case yes = <-ch:
	case <-ctx.Done():
	}
	s.mu.Lock()
	s.question, s.answer = "", nil
	s.addLocked("out", map[bool]string{true: "yes", false: "no"}[yes])
	s.mu.Unlock()
	s.changed()
	return yes
}

// Answer gives the answer to a command's question.
func (s *Session) Answer(yes bool) {
	s.mu.Lock()
	ch := s.answer
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- yes:
		default:
		}
	}
}

// SetInput keeps the text being typed.
func (s *Session) SetInput(text string) { s.mu.Lock(); s.input = text; s.mu.Unlock() }

// History moves through commands already run.
func (s *Session) History(dir int) {
	s.mu.Lock()
	s.hpos = max(0, min(len(s.history), s.hpos+dir))
	if s.hpos < len(s.history) {
		s.input = s.history[s.hpos]
	} else {
		s.input = ""
	}
	s.mu.Unlock()
	s.changed()
}

// Clear empties the log.
func (s *Session) Clear() {
	s.mu.Lock()
	s.lines = nil
	s.mu.Unlock()
	s.changed()
}

// Submit runs the typed line in the background.
func (s *Session) Submit() {
	s.mu.Lock()
	line := strings.TrimSpace(s.input)
	if line == "" || s.question != "" || s.running() {
		s.mu.Unlock()
		return
	}
	s.input = ""
	if n := len(s.history); n == 0 || s.history[n-1] != line {
		s.history = append(s.history, line)
	}
	s.hpos = len(s.history)
	s.mu.Unlock()
	s.Run(line)
}

func (s *Session) running() bool { return s.Pending.Busy() }

// Run runs one command line in the background.
func (s *Session) Run(line string) {
	if strings.TrimSpace(line) == "clear" {
		s.Clear()
		return
	}
	s.Pending.Run(func(ctx context.Context) error {
		if s.Before != nil {
			s.Before()
		}
		s.mu.Lock()
		s.runCtx = ctx
		s.addLocked("cmd", s.prompt+"> "+line)
		s.mu.Unlock()
		s.changed()
		err := s.Shell.Run(ctx, line)
		prompt := s.Shell.pathOf(s.Shell.Cwd()) // after a cd, the new folder
		s.mu.Lock()
		s.prompt = prompt
		s.flushLocked()
		if err != nil {
			s.addLocked("err", err.Error())
		}
		s.mu.Unlock()
		if s.OnDone != nil {
			s.OnDone(err)
		}
		return nil
	})
}

// AddFiles opens the chooser; picked files wait in the inbox.
func (s *Session) AddFiles() {
	if s.PickFiles == nil {
		return
	}
	s.PickFiles(func(name string, data []byte) {
		s.Files.Add(name, data)
		s.mu.Lock()
		s.addLocked("out", "added "+name)
		s.mu.Unlock()
		s.changed()
	})
}

// Props returns the props for components.ConsolePanel.
func (s *Session) Props() components.ConsoleProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.ConsoleProps{
		Lines:     append([]components.ConsoleLine(nil), s.lines...),
		Prompt:    s.prompt,
		Input:     s.input,
		OnInput:   s.SetInput,
		OnSubmit:  s.Submit,
		OnHistory: s.History,
		OnClear:   s.Clear,
		Inbox:     s.Files.Names(),
		Question:  s.question,
		Busy:      s.Pending.Busy(),
	}
	if s.question != "" {
		p.OnAnswer = s.Answer
	}
	if s.PickFiles != nil {
		p.OnAddFiles = s.AddFiles
	}
	return p
}
