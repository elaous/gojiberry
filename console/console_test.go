package console

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

type rig struct {
	t   *testing.T
	sh  *Shell
	out *bytes.Buffer
	err *bytes.Buffer
	s   *saver.Saver
}

func newRig(t *testing.T, input string) *rig {
	t.Helper()
	s := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	r := &rig{t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, s: s}
	r.sh = New(s, "m", strings.NewReader(input), r.out, r.err)
	return r
}

func (r *rig) run(line string) error {
	r.t.Helper()
	r.out.Reset()
	r.err.Reset()
	return r.sh.Run(context.Background(), line)
}

func (r *rig) must(line string) string {
	r.t.Helper()
	if err := r.run(line); err != nil {
		r.t.Fatalf("%s: %v", line, err)
	}
	return r.out.String()
}

func (r *rig) doc(name, text, folder string) string {
	r.t.Helper()
	d := docfile.Document{Title: name, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	d.SetName(name)
	d.SetFolder(folder)
	e, err := r.s.Save(context.Background(), "m", docfile.JSON, d)
	if err != nil {
		r.t.Fatal(err)
	}
	return e.ID
}

func (r *rig) folderID(name string) string {
	fs, _ := r.s.Folders(context.Background(), "m")
	for _, f := range fs {
		if f.Name == name {
			return f.ID
		}
	}
	r.t.Fatalf("no folder %s", name)
	return ""
}

func (r *rig) names(folder string) []string {
	r.t.Helper()
	es, err := r.s.List(context.Background(), "m")
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		if e.Folder == folder {
			out = append(out, e.Name)
		}
	}
	return out
}

func TestSplit(t *testing.T) {
	for line, want := range map[string][]string{
		`ls`:                        {"ls"},
		`mv 'Report *' Archive/`:    {"mv", "Report *", "Archive/"},
		`tag +draft "Q1 plan" a\ b`: {"tag", "+draft", "Q1 plan", "a b"},
		`x ''`:                      {"x", ""},
	} {
		got, err := Split(line)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q %v", line, got, err)
		}
	}
	for _, bad := range []string{`ls 'open`, `ls \`} {
		if _, err := Split(bad); err == nil {
			t.Errorf("%q", bad)
		}
	}
}

func TestMoveByPatternAndDryRun(t *testing.T) {
	r := newRig(t, "")
	r.doc("Report Q1", "a", "")
	r.doc("Report Q2", "b", "")
	r.doc("Notes", "c", "")
	r.must("mkdir Archive")
	r.sh.DryRun = true
	out := r.must("mv 'Report *' Archive/")
	if !strings.Contains(out, "would move Report Q1, Report Q2 into Archive/") || len(r.names("")) != 3 {
		t.Fatalf("a dry run says and does nothing: %q %v", out, r.names(""))
	}
	r.sh.DryRun = false
	r.must("mv 'report *' Archive/") // any case
	if got := r.names(""); len(got) != 1 || got[0] != "Notes" {
		t.Fatalf("%v", got)
	}
	r.must("cd Archive")
	if out := r.must("pwd"); out != "/Archive\n" {
		t.Errorf("%q", out)
	}
	if out := r.must("ls"); out != "Report Q1\nReport Q2\n" {
		t.Errorf("%q", out)
	}
	r.must("mv 'Report Q1' 'Q1 plan'")
	if out := r.must("ls"); out != "Q1 plan\nReport Q2\n" {
		t.Errorf("%q", out)
	}
	if err := r.run("mv 'Report Q2' Q1 plan"); err == nil {
		t.Error("unquoted words are several arguments: the last is a destination and the others must match")
	}
}

func TestRenameAndFolders(t *testing.T) {
	r := newRig(t, "")
	r.doc("Draft", "a", "")
	r.must("mv Draft Final")
	if got := r.names(""); len(got) != 1 || got[0] != "Final" {
		t.Fatalf("%v", got)
	}
	r.must("mkdir Work")
	r.must("mkdir Work/Reports")
	r.must("mv Final 'Work/Reports/Final report'")
	r.must("cd Work/Reports")
	if out := r.must("ls"); out != "Final report\n" {
		t.Fatalf("%q", out)
	}
	r.must("cd ../..")
	r.must("mv Work/Reports Reports") // a folder moved up to the top, same name
	if out := r.must("ls"); out != "Reports/\nWork/\n" {
		t.Fatalf("%q", out)
	}
	r.must("mv Reports 'Old reports'")
	if out := r.must("ls -l"); !strings.Contains(out, "Old reports/") {
		t.Fatalf("%q", out)
	}
}

func TestTagAddAndRemove(t *testing.T) {
	r := newRig(t, "")
	r.doc("a.docx", "x", "")
	r.doc("b.docx", "y", "")
	r.doc("c.txt", "z", "")
	r.must("tag +draft +q1 *.docx")
	r.must("tag -q1 b.docx")
	es, _ := r.s.List(context.Background(), "m")
	got := map[string]string{}
	for _, e := range es {
		got[e.Name] = strings.Join(e.Tags, ",")
	}
	if got["a.docx"] != "draft,q1" || got["b.docx"] != "draft" || got["c.txt"] != "" {
		t.Fatalf("%v", got)
	}
	if err := r.run("tag +x nomatch*"); err == nil {
		t.Error("a pattern that matches nothing is an error")
	}
	if err := r.run("tag nomatch"); err == nil {
		t.Error("no tags given")
	}
}

func TestRemoveRestoreAndPurgeAsks(t *testing.T) {
	r := newRig(t, "n\ny\n")
	r.doc("old 1", "x", "")
	r.doc("old 2", "y", "")
	r.doc("keep", "z", "")
	r.must("rm 'old *'")
	if got := r.names(""); len(got) != 1 || got[0] != "keep" {
		t.Fatalf("trashed documents leave the list: %v", got)
	}
	if out := r.must("trash"); !strings.Contains(out, "old 1") || !strings.Contains(out, "old 2") {
		t.Fatalf("%q", out)
	}
	r.must("restore 'old 1'")
	// purge asks; the first answer is no.
	out := r.must("purge 'old *'")
	if !strings.Contains(out, "[y/N]") || !strings.Contains(out, "left alone") {
		t.Fatalf("%q", out)
	}
	if tr, _ := r.s.Trashed(context.Background(), "m"); len(tr) != 1 {
		t.Fatalf("a no leaves it: %d", len(tr))
	}
	// A dry run asks nothing and changes nothing.
	r.sh.DryRun = true
	if out := r.must("purge 'old *'"); !strings.Contains(out, "would delete for good") || strings.Contains(out, "[y/N]") {
		t.Fatalf("%q", out)
	}
	r.sh.DryRun = false
	// The second answer is yes.
	out = r.must("purge 'old *'")
	if !strings.Contains(out, "delete for good old 2") {
		t.Fatalf("%q", out)
	}
	if tr, _ := r.s.Trashed(context.Background(), "m"); len(tr) != 0 {
		t.Fatalf("%d", len(tr))
	}
	r.sh.Yes = true
	r.must("rm keep")
	r.must("purge keep") // -y: no question
	if err := r.run("rm Missing"); err == nil {
		t.Error("nothing matches")
	}
}

func TestCopyMkdirRmdir(t *testing.T) {
	r := newRig(t, "")
	r.doc("plan", "x", "")
	r.must("mkdir Work")
	r.must("cp plan Work/")
	r.must("cp plan 'plan v2'")
	if got := r.names(""); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := r.names(r.folderID("Work")); len(got) != 1 {
		t.Fatalf("a copy into the folder: %v", got)
	}
	if err := r.run("mkdir Work"); err == nil {
		t.Error("exists")
	}
	r.must("mkdir Empty")
	r.must("rmdir Empty")
	if err := r.run("rmdir Work"); err == nil {
		t.Error("a folder with a document in it stays")
	}
	if err := r.run("cd Nowhere"); err == nil {
		t.Error("no such folder")
	}
}

func TestImportExportAndPack(t *testing.T) {
	r := newRig(t, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.must("mkdir In")
	r.must("import -d In " + filepath.Join(dir, "*.md"))
	r.must("cd In")
	if out := r.must("ls -l"); !strings.Contains(out, "notes") {
		t.Fatalf("%q", out)
	}
	out := filepath.Join(dir, "out")
	r.must("export -f markdown -o " + out + " notes")
	files, _ := filepath.Glob(filepath.Join(out, "*.md"))
	if len(files) != 1 {
		t.Fatalf("%v", files)
	}
	if data, err := os.ReadFile(files[0]); err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("%v", err)
	}
	r.must("cd /")
	pk := filepath.Join(dir, "all.opendoc")
	r.must("pack " + pk)
	other := newRig(t, "")
	if out := other.must("unpack " + pk); !strings.Contains(out, "1 documents, 1 folders") {
		t.Fatalf("%q", out)
	}
	other.must("cd In")
	if out := other.must("ls"); out != "notes\n" {
		t.Fatalf("%q", out)
	}
	// A dry run packs nothing.
	r.sh.DryRun = true
	nope := filepath.Join(dir, "nope.opendoc")
	r.must("pack " + nope)
	if _, err := os.Stat(nope); err == nil {
		t.Error("a dry run wrote a file")
	}
}

func TestIntegrityCommands(t *testing.T) {
	r := newRig(t, "n\n")
	r.doc("a", "x", "")
	if out := r.must("check"); !strings.Contains(out, "1 documents checked") {
		t.Fatalf("%q", out)
	}
	if out := r.must("repair"); !strings.Contains(out, "agrees") {
		t.Fatalf("%q", out)
	}
	if out := r.must("rebuild"); !strings.Contains(out, "left alone") {
		t.Fatalf("a no leaves it: %q", out)
	}
	r.sh.Yes = true
	if out := r.must("rebuild"); !strings.Contains(out, "rebuild the catalog") {
		t.Fatalf("%q", out)
	}
	if out := r.must("usage"); !strings.Contains(out, "1 documents") {
		t.Fatalf("%q", out)
	}
	if err := r.run("frobnicate"); err == nil {
		t.Error("an unknown command")
	}
}

func TestInteractiveSession(t *testing.T) {
	r := newRig(t, "mkdir Work\ncd Work\npwd\nrm nothing\nexit\nmkdir Never\n")
	if err := r.sh.Interact(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.out.String(), "/> ") || !strings.Contains(r.out.String(), "/Work\n") || !strings.Contains(r.err.String(), "nothing matches") {
		t.Fatalf("%q / %q", r.out.String(), r.err.String())
	}
	fs, _ := r.s.Folders(context.Background(), "m")
	if len(fs) != 1 {
		t.Errorf("commands after exit are not run: %+v", fs)
	}
}
