package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

func newTestSession(t *testing.T) (*Session, *saver.Saver, map[string][]byte) {
	t.Helper()
	sv, target := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}, "m"
	delivered := map[string][]byte{}
	s := NewSession(New(sv, target, nil, nil, nil), func(name string, data []byte) { delivered[name] = data })
	s.Pending.Inline = true
	return s, sv, delivered
}

func (s *Session) type_(line string) {
	s.SetInput(line)
	s.Submit()
}

func texts(p components.ConsoleProps, kind string) string {
	var out []string
	for _, l := range p.Lines {
		if kind == "" || l.Kind == kind {
			out = append(out, l.Text)
		}
	}
	return strings.Join(out, "\n")
}

func TestSessionRunsCommandsAndKeepsALogAndAPrompt(t *testing.T) {
	ctx := context.Background()
	s, sv, _ := newTestSession(t)
	for _, name := range []string{"Report Q1", "Report Q2"} {
		doc := docfile.Document{Title: name, Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x"}}}}
		doc.SetName(name)
		if _, err := sv.Save(ctx, s.Shell.Target, docfile.JSON, doc); err != nil {
			t.Fatal(err)
		}
	}
	s.type_("ls")
	p := s.Props()
	if got := texts(p, "cmd"); got != "/> ls" || !strings.Contains(texts(p, "out"), "Report Q1") || !strings.Contains(texts(p, "out"), "Report Q2") || p.Input != "" {
		t.Fatalf("%+v", p)
	}
	s.type_("mkdir Archive")
	s.type_("mv 'Report *' Archive/")
	s.type_("cd Archive")
	p = s.Props()
	if p.Prompt != "/Archive" {
		t.Errorf("prompt %q", p.Prompt)
	}
	s.type_("ls")
	if p = s.Props(); !strings.HasSuffix(texts(p, "out"), "Report Q2") {
		t.Errorf("%s", texts(p, ""))
	}
	s.type_("nosuchcommand")
	p = s.Props()
	if !strings.Contains(texts(p, "err"), "unknown command") {
		t.Errorf("%s", texts(p, ""))
	}
	s.type_("pwd")
	if p = s.Props(); !strings.HasSuffix(texts(p, "out"), "/Archive") {
		t.Errorf("%s", texts(p, ""))
	}
	s.type_("clear")
	if p = s.Props(); len(p.Lines) != 0 {
		t.Errorf("%d lines after clear", len(p.Lines))
	}
}

func TestSessionHistoryWalksTheCommandsRun(t *testing.T) {
	s, _, _ := newTestSession(t)
	s.type_("pwd")
	s.type_("usage")
	s.type_("usage") // the same twice in a row is kept once
	p := s.Props()
	p.OnHistory(-1)
	if got := s.Props().Input; got != "usage" {
		t.Errorf("%q", got)
	}
	s.History(-1)
	if got := s.Props().Input; got != "pwd" {
		t.Errorf("%q", got)
	}
	s.History(-1) // the oldest stays
	if got := s.Props().Input; got != "pwd" {
		t.Errorf("%q", got)
	}
	s.History(1)
	s.History(1)
	if got := s.Props().Input; got != "" {
		t.Errorf("past the newest is an empty line: %q", got)
	}
}

func TestSessionImportsPickedFilesAndDeliversExports(t *testing.T) {
	s, _, delivered := newTestSession(t)
	var add func(string, []byte)
	s.PickFiles = func(f func(string, []byte)) { add = f }
	p := s.Props()
	if p.OnAddFiles == nil {
		t.Fatal("no Add files button")
	}
	p.OnAddFiles()
	add("notes.md", []byte("# Notes\n\nhello"))
	if got := s.Props().Inbox; len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("inbox %v", got)
	}
	s.type_("import *.md")
	if p = s.Props(); !strings.Contains(texts(p, "out"), "import notes.md") || texts(p, "err") != "" {
		t.Fatalf("%s", texts(p, ""))
	}
	s.type_("ls")
	if p = s.Props(); !strings.Contains(texts(p, "out"), "notes") {
		t.Fatalf("%s", texts(p, ""))
	}
	s.type_("export -f markdown notes")
	if len(delivered) != 1 {
		t.Fatalf("delivered %v: %s", delivered, texts(s.Props(), ""))
	}
	for name, data := range delivered {
		if !strings.HasSuffix(name, ".md") || !strings.Contains(string(data), "hello") {
			t.Errorf("%s: %q", name, data)
		}
	}
	s.type_("export -f markdown notes")
	if !strings.Contains(texts(s.Props(), "err"), "exists") {
		t.Errorf("%s", texts(s.Props(), ""))
	}
	s.type_("export --force -f markdown notes")
	s.type_("pack all.opendoc")
	if _, ok := delivered["all.opendoc"]; !ok {
		t.Errorf("no pack: %s", texts(s.Props(), ""))
	}
	s.Files.Add("all.opendoc", delivered["all.opendoc"])
	s.type_("unpack all.opendoc")
	if !strings.Contains(texts(s.Props(), "out"), "documents") {
		t.Errorf("%s", texts(s.Props(), ""))
	}
}

func TestSessionAsksWithYesAndNo(t *testing.T) {
	ctx := context.Background()
	s, sv, _ := newTestSession(t)
	s.Pending.Inline = false
	doc := docfile.Document{Title: "Old", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x"}}}}
	doc.SetName("Old")
	if _, err := sv.Save(ctx, s.Shell.Target, docfile.JSON, doc); err != nil {
		t.Fatal(err)
	}
	waitFor := func(cond func(components.ConsoleProps) bool) components.ConsoleProps {
		t.Helper()
		for range 400 {
			if p := s.Props(); cond(p) {
				return p
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out: %s", texts(s.Props(), ""))
		return components.ConsoleProps{}
	}
	s.Run("rm Old")
	waitFor(func(p components.ConsoleProps) bool { return !p.Busy && strings.Contains(texts(p, "out"), "trash Old") })
	s.Run("purge Old")
	p := waitFor(func(p components.ConsoleProps) bool { return p.Question != "" })
	if !strings.Contains(p.Question, "Delete 1 documents") || p.OnAnswer == nil {
		t.Fatalf("%+v", p)
	}
	p.OnAnswer(false)
	p = waitFor(func(p components.ConsoleProps) bool { return !p.Busy && p.Question == "" })
	if !strings.Contains(texts(p, "out"), "left alone") {
		t.Errorf("%s", texts(p, ""))
	}
	if _, trashed, _, _ := sv.Overview(ctx, s.Shell.Target); len(trashed) != 1 {
		t.Errorf("a refused purge deleted: %v", trashed)
	}
	s.Run("purge Old")
	p = waitFor(func(p components.ConsoleProps) bool { return p.Question != "" })
	p.OnAnswer(true)
	waitFor(func(p components.ConsoleProps) bool { return !p.Busy && p.Question == "" })
	if _, trashed, _, _ := sv.Overview(ctx, s.Shell.Target); len(trashed) != 0 {
		t.Errorf("an accepted purge left it: %v", trashed)
	}
}
