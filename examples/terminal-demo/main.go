// Command terminal-demo is a text-mode document manager and editor: the same saver.Session
// that stands behind the browser's document components (DocList, Saver, capacity meter)
// drawn as text, with the console's command grammar for the file work (mv, tag, rm,
// pack...) and a small line editor for a document's text. No browser, no raw terminal mode,
// no dependencies: lines in, screens out.
//
//	go run ./examples/terminal-demo -store files:./docs
//
// Commands: ls cd mkdir mv cp tag rm restore purge pack unpack check usage help (see
// docs/console.md), and: new NAME | open NAME | edit | save [NAME] | quit.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/console"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

func main() {
	store := flag.String("store", "memory", "memory or files:DIR")
	flag.Parse()
	if err := run(*store, os.Stdin, os.Stdout, true); err != nil {
		fmt.Fprintln(os.Stderr, "terminal-demo:", err)
		os.Exit(1)
	}
}

// app is the demo's state: the session, the console, and the text being edited.
type app struct {
	ctx   context.Context
	s     *saver.Session
	sh    *console.Shell
	in    *bufio.Reader
	out   io.Writer
	lines []string // the document being edited, a line each
	dirty bool     // edited since it was last saved or opened
	ansi  bool     // clear the screen between screens
}

func openStore(spec string) (saver.Target, error) {
	switch {
	case spec == "" || spec == "memory":
		return saver.Target{Name: "docs", Label: "Memory", Adapter: docstore.NewMemoryAdapter(), Capacity: 1 << 20}, nil
	case strings.HasPrefix(spec, "files:"):
		a, err := docstore.NewLocalFSAdapter(strings.TrimPrefix(spec, "files:"))
		if err != nil {
			return saver.Target{}, err
		}
		return saver.Target{Name: "docs", Label: "Files", Adapter: a}, nil
	}
	return saver.Target{}, fmt.Errorf("unknown store %q (memory or files:DIR)", spec)
}

func run(store string, in io.Reader, out io.Writer, ansi bool) error {
	t, err := openStore(store)
	if err != nil {
		return err
	}
	a := &app{ctx: context.Background(), in: bufio.NewReader(in), out: out, ansi: ansi}
	sv := &saver.Saver{Targets: []saver.Target{t}}
	a.s = saver.NewSession(sv, a.document, a.load)
	a.s.Pending.Inline = true
	a.sh = console.New(sv, t.Name, a.in, out, out)
	if err := a.s.Refresh(a.ctx); err != nil {
		return err
	}
	return a.loop()
}

// document is what the session saves: the lines as the text of one run.
func (a *app) document() docfile.Document {
	return docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: strings.Join(a.lines, "\n")}}}}
}

// load is called by the session when a document is opened.
func (a *app) load(d docfile.Document) {
	var b strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			b.WriteString(it.Chars.Text)
		}
	}
	a.lines = strings.Split(b.String(), "\n")
	a.dirty = false
}

func (a *app) printf(format string, v ...any) { _, _ = fmt.Fprintf(a.out, format, v...) }

// screen draws the document list, as text, from the session's props: the same data the
// browser's DocList and Capacity are given.
func (a *app) screen() {
	if a.ansi {
		a.printf("\x1b[2J\x1b[H")
	}
	p := a.s.Props()
	path := "/"
	for _, f := range p.Docs.Path {
		path += f.Name + "/"
	}
	a.printf("== %s ==\n", path)
	for _, f := range p.Docs.Folders {
		a.printf("  %s/\n", f.Name)
	}
	for _, d := range p.Docs.Docs {
		mark := " "
		if d.ID == p.Docs.Current {
			mark = "*"
		}
		tags := ""
		if len(d.Tags) > 0 {
			tags = "  [" + strings.Join(d.Tags, " ") + "]"
		}
		a.printf(" %s %-24s %10s  %s%s\n", mark, d.Name, components.SizeText(int64(d.Size)), d.Updated.Format("2006-01-02 15:04"), tags)
	}
	if len(p.Docs.Docs)+len(p.Docs.Folders) == 0 {
		a.printf("  (nothing saved here yet: new NAME, then edit)\n")
	}
	if c := p.Capacity; c != nil && c.Total > 0 {
		pct := float64(c.Used) / float64(c.Total) * 100
		a.printf("-- %s of %s used (%.0f%%)", components.SizeText(c.Used), components.SizeText(c.Total), pct)
		if pct >= 80 {
			a.printf("\n!! filling up: pack what you need now (pack out.opendoc), then purge old documents")
		}
		a.printf("\n")
	}
	if p.Status != "" {
		a.printf("-- %s\n", p.Status)
	}
}

func (a *app) loop() error {
	for {
		a.screen()
		a.printf("> ")
		line, err := a.in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			quit, cerr := a.command(line)
			if cerr != nil {
				a.printf("error: %v\n", cerr)
			}
			if quit {
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
}

func (a *app) command(line string) (quit bool, err error) {
	words, err := console.Split(line)
	if err != nil || len(words) == 0 {
		return false, err
	}
	switch words[0] {
	case "quit", "exit":
		if a.dirty {
			return false, errors.New("the text has unsaved changes: save, or quit again with 'quit!'")
		}
		return true, nil
	case "quit!":
		return true, nil
	case "new":
		if len(words) < 2 {
			return false, errors.New("usage: new NAME")
		}
		a.s.Detach()
		a.s.SetName(strings.Join(words[1:], " "))
		a.lines, a.dirty = []string{}, true
		return false, a.editor()
	case "open":
		if len(words) < 2 {
			return false, errors.New("usage: open NAME")
		}
		name := strings.Join(words[1:], " ")
		for _, d := range a.s.Docs().Docs {
			if strings.EqualFold(d.Name, name) {
				if err := a.s.Open(a.ctx, d.ID); err != nil {
					return false, err
				}
				return false, a.editor()
			}
		}
		return false, fmt.Errorf("no document called %q here", name)
	case "edit":
		if a.s.Name() == "" && a.lines == nil {
			return false, errors.New("nothing to edit: new NAME or open NAME")
		}
		return false, a.editor()
	case "save":
		if len(words) > 1 {
			a.s.SetName(strings.Join(words[1:], " "))
		}
		if a.s.Name() == "" {
			return false, errors.New("give it a name: save NAME")
		}
		if err := a.s.Save(a.ctx); err != nil {
			return false, err
		}
		a.dirty = false
		return false, nil
	}
	// Everything else is the console's: mv, tag, rm, pack...
	if err := a.sh.Exec(a.ctx, words); err != nil {
		return false, err
	}
	if err := a.s.OpenFolder(a.ctx, a.sh.Cwd()); err != nil { // the list follows cd
		return false, err
	}
	return false, a.s.Refresh(a.ctx)
}

// editor is a line editor: the text is shown with numbers, and commands start with a colon.
func (a *app) editor() error {
	for {
		a.printf("\n-- editing %q (%d lines%s) --\n", a.s.Name(), len(a.lines), map[bool]string{true: ", unsaved", false: ""}[a.dirty])
		if len(a.lines) == 0 {
			a.printf("     (empty: type a line to add it)\n")
		}
		for i, l := range a.lines {
			a.printf("%3d  %s\n", i+1, l)
		}
		a.printf(":a TEXT append   :i N TEXT insert before N   :r N TEXT replace   :d N delete   :w save   :q back   :q! discard\ne> ")
		raw, err := a.in.ReadString('\n')
		line := strings.TrimRight(raw, "\r\n")
		if strings.HasPrefix(line, ":") {
			done, cerr := a.editCommand(line[1:])
			if cerr != nil {
				a.printf("error: %v\n", cerr)
			}
			if done {
				return nil
			}
		} else if line != "" {
			a.lines = append(a.lines, line) // a bare line is appended
			a.dirty = true
		}
		if err != nil {
			return nil
		}
	}
}

func (a *app) editCommand(cmd string) (done bool, err error) {
	verb, rest, _ := strings.Cut(cmd, " ")
	atLine := func(rest string) (n int, text string, err error) {
		num, text, _ := strings.Cut(rest, " ")
		if _, err := fmt.Sscan(num, &n); err != nil || n < 1 || n > len(a.lines) {
			return 0, "", fmt.Errorf("a line number from 1 to %d", len(a.lines))
		}
		return n, text, nil
	}
	switch verb {
	case "a":
		a.lines, a.dirty = append(a.lines, rest), true
	case "i":
		n, text, err := atLine(rest)
		if err != nil {
			return false, err
		}
		a.lines = append(a.lines[:n-1], append([]string{text}, a.lines[n-1:]...)...)
		a.dirty = true
	case "r":
		n, text, err := atLine(rest)
		if err != nil {
			return false, err
		}
		a.lines[n-1], a.dirty = text, true
	case "d":
		n, _, err := atLine(rest)
		if err != nil {
			return false, err
		}
		a.lines = append(a.lines[:n-1], a.lines[n:]...)
		a.dirty = true
	case "w":
		if a.s.Name() == "" {
			return false, errors.New("give it a name first: leave with :q and save NAME")
		}
		if err := a.s.Save(a.ctx); err != nil {
			return false, err
		}
		a.dirty = false
	case "q":
		if a.dirty {
			return false, errors.New("unsaved changes: :w to save, or :q! to throw them away")
		}
		return true, nil
	case "q!":
		a.dirty = false
		return true, nil
	default:
		return false, fmt.Errorf("unknown editor command :%s", verb)
	}
	return false, nil
}
