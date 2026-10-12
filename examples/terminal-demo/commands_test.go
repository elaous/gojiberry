package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newTestApp makes an app on a memory store that reads script (what the editor and prompts read).
func newTestApp(t *testing.T, script string) (*app, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	a, err := newApp("memory", strings.NewReader(script), &out, false)
	if err != nil {
		t.Fatal(err)
	}
	return a, &out
}

func wantErr(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), sub) {
		t.Errorf("err = %v, want one containing %q", err, sub)
	}
}

func TestOpenStore(t *testing.T) {
	for _, spec := range []string{"", "memory"} {
		tg, err := openStore(spec)
		if err != nil || tg.Adapter == nil || tg.Capacity != 1<<20 || tg.Label != "Memory" {
			t.Errorf("%q: %+v %v", spec, tg, err)
		}
	}
	dir := t.TempDir()
	tg, err := openStore("files:" + dir)
	if err != nil || tg.Adapter == nil || tg.Label != "Files" || tg.Capacity != 0 {
		t.Errorf("files: %+v %v", tg, err)
	}
	// A directory that cannot be made: a file stands where it should be.
	blocker := filepath.Join(dir, "plain")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore("files:" + filepath.Join(blocker, "sub")); err == nil {
		t.Error("a store under a plain file was opened")
	}
	_, err = openStore("s3:bucket")
	wantErr(t, err, `unknown store "s3:bucket"`)
}

func TestScreenClearsOnlyWithANSI(t *testing.T) {
	var plain, fancy bytes.Buffer
	a, err := newApp("memory", strings.NewReader(""), &plain, false)
	if err != nil {
		t.Fatal(err)
	}
	a.screen()
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("escape codes without ansi: %q", plain.String())
	}
	if !strings.Contains(plain.String(), "(nothing saved here yet") {
		t.Errorf("an empty store says so: %q", plain.String())
	}
	if err := run("memory", strings.NewReader("quit\n"), &fancy, true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fancy.String(), "\x1b[2J\x1b[H") {
		t.Errorf("no clear at the start: %q", fancy.String())
	}
}

func TestTheLastLineWithoutANewlineIsStillRun(t *testing.T) {
	a, out := newTestApp(t, "new! x")
	if err := a.loop(); err != nil {
		t.Fatal(err)
	}
	// "new! x" at end of input entered the editor, which ended at end of input too.
	if !strings.Contains(out.String(), `editing "x"`) {
		t.Errorf("the final unterminated line was dropped:\n%s", out)
	}
}

func TestQuitNeedsASavedDocumentUnlessForced(t *testing.T) {
	a, _ := newTestApp(t, "")
	for _, w := range []string{"quit", "exit", "quit!"} {
		if q, err := a.command(w); !q || err != nil {
			t.Errorf("%s on a clean app: %v %v", w, q, err)
		}
	}
	a.dirty = true
	for _, w := range []string{"quit", "exit"} {
		q, err := a.command(w)
		if q {
			t.Errorf("%s quit with unsaved text", w)
		}
		wantErr(t, err, "unsaved changes")
	}
	if q, err := a.command("quit!"); !q || err != nil {
		t.Errorf("quit! while dirty: %v %v", q, err)
	}
}

func TestCommandUsageAndGuards(t *testing.T) {
	a, _ := newTestApp(t, "")
	for line, sub := range map[string]string{
		"new":              "usage: new NAME",
		"open":             "usage: open NAME",
		"edit":             "nothing to edit",
		"save":             "give it a name: save NAME",
		"open ghost":       `no document called "ghost" here`,
		"echo 'unclosed":   "", // a quoting error is returned from the splitter
		"frobnicate extra": "unknown command",
	} {
		quit, err := a.command(line)
		if quit {
			t.Errorf("%q quit", line)
		}
		if err == nil {
			t.Errorf("%q: no error", line)
		} else if !strings.Contains(err.Error(), sub) {
			t.Errorf("%q: err = %v, want %q", line, err, sub)
		}
	}
	// Blank input is not a command.
	if q, err := a.command("   "); q || err != nil {
		t.Errorf("blank: %v %v", q, err)
	}
}

func TestSaveWithANameAndOpenIsCaseInsensitive(t *testing.T) {
	// The script feeds the editor of "new!" and then the editor of "open".
	a, _ := newTestApp(t, ":a alpha\n:a beta\n:w\n:q\n:q\n")
	if _, err := a.command("new! draft"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.lines, []string{"alpha", "beta"}) || a.dirty {
		t.Fatalf("after the editor: lines %q dirty %v", a.lines, a.dirty)
	}
	// save NAME renames and saves in one step.
	a.dirty = true
	if _, err := a.command("save Plan B"); err != nil {
		t.Fatal(err)
	}
	if a.dirty || a.s.Name() != "Plan B" {
		t.Errorf("dirty %v, name %q", a.dirty, a.s.Name())
	}
	a.lines = nil
	if _, err := a.command("open plan b"); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !reflect.DeepEqual(a.lines, []string{"alpha", "beta"}) || a.dirty {
		t.Errorf("reopened lines %q dirty %v", a.lines, a.dirty)
	}
}

func TestEditNeedsADocumentThenReenters(t *testing.T) {
	a, out := newTestApp(t, ":q\n")
	a.s.SetName("n")
	a.lines = []string{"one"}
	if _, err := a.command("edit"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1  one") {
		t.Errorf("the editor did not show the text:\n%s", out)
	}
}

func TestEditCommands(t *testing.T) {
	a, _ := newTestApp(t, "")
	a.s.SetName("doc")
	run := func(cmd string) error {
		t.Helper()
		_, err := a.editCommand(cmd)
		return err
	}
	for _, c := range []string{"a one", "a two", "a three"} {
		if err := run(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := run("i 2 inserted"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"one", "inserted", "two", "three"}; !reflect.DeepEqual(a.lines, want) {
		t.Fatalf("after insert: %q", a.lines)
	}
	if err := run("r 4 THREE"); err != nil {
		t.Fatal(err)
	}
	if err := run("d 1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"inserted", "two", "THREE"}; !reflect.DeepEqual(a.lines, want) {
		t.Fatalf("after replace and delete: %q", a.lines)
	}
	if !a.dirty {
		t.Error("edits did not mark the text unsaved")
	}
	before := append([]string(nil), a.lines...)
	for _, bad := range []string{"i 0 x", "i 9 x", "i x y", "i", "r 4 x", "r -1 x", "d 0", "d 4", "d", "d two"} {
		err := run(bad)
		wantErr(t, err, "a line number from 1 to 3")
		if !reflect.DeepEqual(a.lines, before) {
			t.Fatalf("%q changed the text to %q", bad, a.lines)
		}
	}
	wantErr(t, run("z"), "unknown editor command :z")
	wantErr(t, run(""), "unknown editor command :")

	// :q refuses while there are unsaved changes; :w saves; then :q leaves.
	if done, err := a.editCommand("q"); done || err == nil || !strings.Contains(err.Error(), "unsaved changes") {
		t.Errorf(":q while dirty: %v %v", done, err)
	}
	if _, err := a.editCommand("w"); err != nil {
		t.Fatal(err)
	}
	if a.dirty {
		t.Error("still dirty after :w")
	}
	if done, err := a.editCommand("q"); !done || err != nil {
		t.Errorf(":q when clean: %v %v", done, err)
	}
	a.dirty = true
	if done, err := a.editCommand("q!"); !done || err != nil || a.dirty {
		t.Errorf(":q!: %v %v dirty %v", done, err, a.dirty)
	}
}

func TestEditorWriteNeedsAName(t *testing.T) {
	a, _ := newTestApp(t, "")
	a.lines, a.dirty = []string{"x"}, true
	done, err := a.editCommand("w")
	if done {
		t.Error(":w left the editor")
	}
	wantErr(t, err, "give it a name first")
	if !a.dirty {
		t.Error("a failed save cleared the unsaved mark")
	}
}

func TestEditorShowsAnEmptyDocumentAndEndsAtEndOfInput(t *testing.T) {
	a, out := newTestApp(t, "line one\n:i 5 nope\n")
	a.s.SetName("fresh")
	a.lines = []string{}
	if err := a.editor(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(empty: type a line to add it)", `editing "fresh" (0 lines)`, `editing "fresh" (1 lines, unsaved)`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if !reflect.DeepEqual(a.lines, []string{"line one"}) || !a.dirty {
		t.Errorf("lines %q dirty %v", a.lines, a.dirty)
	}
	if a.note != "a line number from 1 to 1" {
		t.Errorf("the failed :i is kept as a note for the next screen: %q", a.note)
	}
}

func TestRemovingTheOpenDocumentNeedsABang(t *testing.T) {
	a, _ := newTestApp(t, "")
	a.lines, a.dirty = []string{"keep me"}, true
	a.s.SetName("openone")
	if _, err := a.command("save"); err != nil {
		t.Fatal(err)
	}
	_, err := a.command("rm openone")
	wantErr(t, err, "is open in the editor")
	if !reflect.DeepEqual(a.lines, []string{"keep me"}) {
		t.Fatalf("the refused rm dropped the text: %q", a.lines)
	}
	if _, err := a.command("rm! openone"); err != nil {
		t.Fatal(err)
	}
	if a.lines != nil || a.dirty || a.s.Props().Docs.Current != "" {
		t.Errorf("rm! left the document open: lines %v dirty %v current %q", a.lines, a.dirty, a.s.Props().Docs.Current)
	}
	// Another document is not guarded.
	a.lines, a.dirty = []string{"x"}, true
	a.s.Detach()
	a.s.SetName("other")
	if _, err := a.command("save"); err != nil {
		t.Fatal(err)
	}
	a.s.Detach()
	a.s.SetName("third")
	if _, err := a.command("save"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.command("rm other"); err != nil {
		t.Errorf("rm of a document that is not open: %v", err)
	}
	if a.s.Name() != "third" || !reflect.DeepEqual(a.lines, []string{"x"}) {
		t.Errorf("removing another document disturbed the open one: %q %q", a.s.Name(), a.lines)
	}
	if got := a.s.Props().Docs.Docs; len(got) != 1 || got[0].Name != "third" {
		t.Errorf("documents left: %+v", got)
	}
}

func TestACapacityMeterShowsUsedOfTotal(t *testing.T) {
	out := session(t, "memory", "new d\nhello\n:w\n:q\nquit\n")
	if !strings.Contains(out, "of 1 MiB (1,048,576 bytes) used (0%)") {
		t.Errorf("no capacity line:\n%s", out)
	}
	if strings.Contains(out, "filling up") {
		t.Errorf("a nearly empty store warns:\n%s", out)
	}
}
