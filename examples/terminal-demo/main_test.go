package main

import (
	"bytes"
	"strings"
	"testing"
)

// session runs the demo on a script of lines and returns everything it printed.
func session(t *testing.T, store, script string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(store, strings.NewReader(script), &out, false); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestWriteSaveListAndReopen(t *testing.T) {
	out := session(t, "memory", strings.Join([]string{
		"new Shopping list", // enters the editor
		"milk",              // a bare line is appended
		":a bread",
		":i 1 coffee",
		":r 2 oat milk",
		":w",
		":q",
		"tag +home 'Shopping list'",
		"mkdir Lists",
		"mv 'Shopping list' Lists/",
		"cd Lists",
		"open 'Shopping list'",
		":q",
		"quit",
	}, "\n")+"\n")
	for _, want := range []string{
		`editing "Shopping list"`,
		"  1  coffee", "  2  oat milk", "  3  bread",
		"== /Lists/ ==",
		"Shopping list", "[home]",
		"-- Shopping list saved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	// Reopened after the move, the text is what was saved.
	if i := strings.LastIndex(out, "editing"); !strings.Contains(out[i:], "coffee") || !strings.Contains(out[i:], "bread") {
		t.Errorf("the reopened document has its text:\n%s", out[i:])
	}
}

func TestUnsavedTextIsNotThrownAwayByAccident(t *testing.T) {
	out := session(t, "memory", "new Draft\nhello\n:q\n:q!\nquit\n")
	if !strings.Contains(out, "unsaved changes: :w to save, or :q! to throw them away") {
		t.Errorf("%s", out)
	}
	if strings.Contains(out, "Draft  ") && strings.Contains(out[strings.LastIndex(out, "== /"):], "Draft") {
		t.Errorf("a discarded draft was saved:\n%s", out)
	}
}

func TestTheFileWorkIsTheConsolesAndDryRunStillWorks(t *testing.T) {
	out := session(t, "memory", "new a\nx\n:w\n:q\nrm a\ntrash\nrestore a\nusage\nfrobnicate\nopen nothing\nquit\n")
	for _, want := range []string{"trash a", "a  (trashed", "restore a", "1 documents", `unknown command "frobnicate"`, `no document called "nothing"`} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestACapacityWarningShowsAsText(t *testing.T) {
	big := strings.Repeat("x", 900<<10)
	out := session(t, "memory", "new big\n:a "+big+"\n:w\n:q\nquit\n")
	if !strings.Contains(out, "filling up: pack what you need now") {
		t.Errorf("no warning in:\n%s", out[len(out)-400:])
	}
}

func TestFilesStoreAndBadStore(t *testing.T) {
	dir := t.TempDir()
	session(t, "files:"+dir, "new kept\nhi\n:w\n:q\nquit\n")
	if out := session(t, "files:"+dir, "quit\n"); !strings.Contains(out, "kept") {
		t.Errorf("a second run sees the saved document:\n%s", out)
	}
	if err := run("ftp:x", strings.NewReader(""), &bytes.Buffer{}, false); err == nil {
		t.Error("an unknown store")
	}
}
