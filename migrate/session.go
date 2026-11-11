package migrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
)

// SourceChoice is a platform a Session can migrate from.
type SourceChoice struct {
	Name, Label string
	New         func(token string) Source
}

type Session struct {
	Sources []SourceChoice
	Sink    func(docfile.Codec) Sink
	Formats []docfile.Codec
	Deliver func(file, mime string, data []byte)
	// possibly from another goroutine
	OnChange   func()
	OnDone     func(Report)
	Known      func(ctx context.Context) (Ledger, error)
	HistoryMax int
	// Import converts a Word file; nil is DefaultImport.
	Import  func([]byte) (docfile.Document, error)
	Pending *components.Pending

	mu       sync.Mutex
	choice   *SourceChoice
	format   string
	token    string
	src      Source
	path     []Item
	items    []Item
	selected map[string]bool
	running  bool
	done     int
	total    int
	current  string
	report   *Report
	again    bool // bring again what an earlier run brought
	history  bool // bring the version history too
	partial  bool
}

// NewSession makes a Session over the platforms.
func NewSession(sources []SourceChoice, sink func(docfile.Codec) Sink) *Session {
	s := &Session{Sources: sources, Sink: sink, selected: map[string]bool{}, Formats: docfile.Formats(), format: docfile.Open.Name()}
	s.Pending = components.NewPending(s.changed)
	return s
}

func (s *Session) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Session) set(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	s.changed()
}

func (s *Session) SetSource(name string) {
	s.set(func() {
		s.choice, s.src, s.path, s.items, s.selected, s.report = nil, nil, nil, nil, map[string]bool{}, nil
		for i := range s.Sources {
			if s.Sources[i].Name == name {
				s.choice = &s.Sources[i]
			}
		}
	})
}

// SetFormat chooses the format migrated documents are stored in.
func (s *Session) SetFormat(name string) { s.set(func() { s.format = name }) }

func (s *Session) codec() docfile.Codec {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.Formats {
		if c.Name() == s.format {
			return c
		}
	}
	return docfile.JSON
}

func (s *Session) SetAgain(on bool) { s.set(func() { s.again = on }) }

func (s *Session) SetHistory(on bool) { s.set(func() { s.history = on }) }

func (s *Session) historyMax() int {
	if s.HistoryMax > 0 {
		return s.HistoryMax
	}
	return 20
}

// SetToken keeps the access token typed.
func (s *Session) SetToken(token string) { s.set(func() { s.token = token }) }

func (s *Session) Connect(ctx context.Context) error {
	s.mu.Lock()
	choice, token := s.choice, strings.TrimSpace(s.token)
	s.mu.Unlock()
	if choice == nil || choice.New == nil {
		return errors.New("migrate: choose a platform first")
	}
	src := choice.New(token)
	items, err := src.List(ctx, "")
	if err != nil {
		return err
	}
	var stale bool
	s.set(func() {
		if s.choice != choice { // the platform was changed while this was listing
			stale = true
			return
		}
		s.src, s.path, s.items, s.selected, s.report = src, nil, items, map[string]bool{}, nil
	})
	if stale {
		return errors.New("migrate: the platform changed while connecting")
	}
	return nil
}

func (s *Session) OpenFolder(ctx context.Context, id string) error {
	s.mu.Lock()
	src := s.src
	path := slices.Clone(s.path)
	var target *Item
	if id != "" {
		for i := range s.items {
			if s.items[i].ID == id && s.items[i].Folder {
				target = &s.items[i]
				path = append(path, *target)
			}
		}
		if target == nil {
			if i := slices.IndexFunc(path, func(f Item) bool { return f.ID == id }); i >= 0 {
				path = path[:i+1]
			} else {
				s.mu.Unlock()
				return errors.New("migrate: no such folder")
			}
		}
	} else {
		path = nil
	}
	s.mu.Unlock()
	if src == nil {
		return errors.New("migrate: not connected")
	}
	items, err := src.List(ctx, id)
	if err != nil {
		return err
	}
	s.set(func() {
		if s.src != src {
			return
		}
		s.path, s.items, s.selected = path, items, map[string]bool{}
	})
	return nil
}

// Toggle selects an item, or unselects it.
func (s *Session) Toggle(id string) {
	s.set(func() {
		if s.selected[id] {
			delete(s.selected, id)
		} else {
			s.selected[id] = true
		}
	})
}

// SelectAll selects everything listed.
func (s *Session) SelectAll() {
	s.set(func() {
		for _, it := range s.items {
			s.selected[it.ID] = true
		}
	})
}

func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	src, choice := s.src, s.choice
	var items []Item
	for _, it := range s.items {
		if s.selected[it.ID] {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		items = slices.Clone(s.items)
	}
	s.mu.Unlock()
	if src == nil || choice == nil {
		return errors.New("migrate: not connected")
	}
	return s.run(ctx, src, items, tagOf(choice.Label))
}

// ImportFiles migrates Word files from this computer.
func (s *Session) ImportFiles(ctx context.Context, files Files) error {
	if len(files) == 0 {
		return nil
	}
	items, _ := files.List(ctx, "")
	return s.run(ctx, files, items, "word-file")
}

func tagOf(name string) string { return strings.ReplaceAll(strings.ToLower(name), " ", "-") }

func (s *Session) run(ctx context.Context, src Source, items []Item, tag string) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("migrate: a migration is already running")
	}
	s.running, s.report, s.done, s.total, s.current, s.partial = true, nil, 0, 0, "", false
	s.mu.Unlock()
	defer s.set(func() { s.running = false })
	sink, finish, err := s.sinkFor()
	if err != nil {
		return err
	}
	s.mu.Lock()
	opt := Options{Import: s.Import, Tag: tag, Again: s.again}
	if _, ok := src.(Versioned); ok && s.history {
		opt.History = s.historyMax()
	}
	s.mu.Unlock()
	opt.OnProgress = func(done, total int, name string) {
		s.set(func() { s.done, s.total, s.current = done, total, name })
	}
	if s.Known != nil && finish == nil {
		if opt.Ledger, err = s.Known(ctx); err != nil {
			return err
		}
	}
	rep, err := Run(ctx, src, items, sink, opt)
	if finish != nil {
		if err != nil {
			s.set(func() { s.partial = true })
		} else if ferr := finish(); ferr != nil {
			err = ferr
		}
	}
	s.set(func() { s.report = &rep })
	if s.OnDone != nil {
		s.OnDone(rep)
	}
	return err
}

func (s *Session) sinkFor() (Sink, func() error, error) {
	s.mu.Lock()
	pick := s.format
	s.mu.Unlock()
	if pick != PackFormat || s.Deliver == nil {
		if s.Sink == nil {
			return nil, nil, errors.New("migrate: nowhere to put the documents")
		}
		return s.Sink(s.codec()), nil, nil
	}
	ps, err := NewPackSink("migrated " + time.Now().Format("2006-01-02"))
	if err != nil {
		return nil, nil, err
	}
	return ps, func() error {
		data, n, err := ps.Bytes(nil)
		if err != nil || n == 0 {
			return err
		}
		s.Deliver("migrated-"+time.Now().Format("2006-01-02")+".opendoc", "application/vnd.opendocs.pack", data)
		return nil
	}, nil
}

func (s *Session) Props(pickFiles func()) components.MigrateProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := func(f func(context.Context) error) func() { return func() { s.Pending.Run(f) } }
	p := components.MigrateProps{
		OnSource:  s.SetSource,
		Token:     s.token,
		OnToken:   s.SetToken,
		Format:    s.format,
		OnFormat:  s.SetFormat,
		OnConnect: run(s.Connect),
		Connected: s.src != nil,
		OnOpenFolder: func(id string) {
			s.Pending.Run(func(ctx context.Context) error { return s.OpenFolder(ctx, id) })
		},
		OnToggle:    s.Toggle,
		OnSelectAll: s.SelectAll,
		OnStart:     run(s.Start),
		OnPickFiles: pickFiles,
		Busy:        s.Pending.Busy() || s.running,
		OnAgain:     s.SetAgain, Again: s.again,
		Done: s.done, Total: s.total, Current: s.current,
	}
	if _, ok := s.src.(Versioned); ok {
		p.OnHistory, p.History, p.HistoryMax = s.SetHistory, s.history, s.historyMax()
	}
	for _, c := range s.Formats {
		p.Formats = append(p.Formats, components.DocChoice{Name: c.Name(), Label: c.Label()})
	}
	if s.Deliver != nil {
		p.Formats = append(p.Formats, components.DocChoice{Name: PackFormat, Label: components.Translate("migrate.pack", "One .opendoc pack (downloaded, not saved here)")})
	}
	for _, c := range s.Sources {
		p.Sources = append(p.Sources, components.DocChoice{Name: c.Name, Label: c.Label})
	}
	if s.choice != nil {
		p.Source = s.choice.Name
	}
	for _, f := range s.path {
		p.Path = append(p.Path, components.MigrateItem{ID: f.ID, Name: f.Name, Folder: true})
	}
	for _, it := range s.items {
		mi := components.MigrateItem{ID: it.ID, Name: it.Name, Folder: it.Folder, Selected: s.selected[it.ID]}
		if !it.Modified.IsZero() {
			mi.Meta = it.Modified.Format("2006-01-02")
		}
		p.Items = append(p.Items, mi)
	}
	if r := s.report; r != nil {
		p.Reported, p.Imported, p.Truncated, p.Partial = true, r.Imported, r.Truncated, s.partial
		p.Skipped, p.Versions = r.Skipped, r.Versions
		for _, f := range r.Failed {
			p.Failed = append(p.Failed, components.MigrateFailure{Name: f.Name, Err: f.Err.Error()})
		}
	}
	if err := s.Pending.Err(); err != nil {
		p.Error = err.Error()
	}
	return p
}

func (s *Session) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("migrate session: %d listed", len(s.items))
}
