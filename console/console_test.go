package console

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

func TestScriptStopsAtTheFirstError(t *testing.T) {
	r := newRig(t, "mkdir A\nrm nothing\nmkdir B\n")
	r.sh.Script = true
	err := r.sh.Interact(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "nothing matches") {
		t.Fatalf("the error is returned with its line: %v", err)
	}
	if fs, _ := r.s.Folders(context.Background(), "m"); len(fs) != 1 {
		t.Errorf("nothing runs after the failure: %+v", fs)
	}
}

func TestScriptNeverReadsAnAnswerFromTheScript(t *testing.T) {
	r := newRig(t, "purge x\nmkdir After\n")
	r.sh.Script = true
	r.doc("x", "a", "")
	r.must("rm x")
	err := r.sh.Interact(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("a declined confirmation is an error in a script: %v", err)
	}
	if fs, _ := r.s.Folders(context.Background(), "m"); len(fs) != 0 {
		t.Errorf("the script stopped: %+v", fs)
	}
	if tr, _ := r.s.Trashed(context.Background(), "m"); len(tr) != 1 {
		t.Error("without -y a script's purge is answered no")
	}
	if !strings.Contains(r.out.String(), "needs -y") {
		t.Errorf("it says so: %q", r.out.String())
	}
	r2 := newRig(t, "purge x\n")
	r2.sh.Script, r2.sh.Yes = true, true
	r2.doc("x", "a", "")
	r2.must("rm x")
	if err := r2.sh.Interact(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if tr, _ := r2.s.Trashed(context.Background(), "m"); len(tr) != 0 {
		t.Error("-y purges")
	}
}

func TestMoveToParentRootAndDot(t *testing.T) {
	r := newRig(t, "")
	r.must("mkdir Work")
	r.must("mkdir Work/Sub")
	r.doc("doc", "a", r.folderID("Sub"))
	r.must("cd Work/Sub")
	r.must("mv doc ..")
	if got := r.names(r.folderID("Work")); len(got) != 1 || got[0] != "doc" {
		t.Fatalf("doc moved to the parent, not renamed: %v", got)
	}
	r.must("cd ..")
	r.must("mv doc /")
	r.must("cd /")
	if got := r.names(""); len(got) != 1 || got[0] != "doc" {
		t.Fatalf("moved to the root: %v", got)
	}
	r.must("mv doc .")
	if got := r.names(""); len(got) != 1 || got[0] != "doc" {
		t.Fatalf("a move onto its own folder is not a rename: %v", got)
	}
	if err := r.run("mv doc nowhere/.."); err == nil {
		t.Error("an unresolvable folder")
	}
	if err := r.run("mkdir .."); err == nil {
		t.Error("'..' is not a folder name")
	}
}

func TestRmdirDotLeavesCwdValid(t *testing.T) {
	r := newRig(t, "")
	r.must("mkdir Work")
	r.must("mkdir Work/Empty")
	r.must("cd Work/Empty")
	r.must("rmdir .")
	if out := r.must("pwd"); out != "/Work\n" {
		t.Fatalf("cwd is the parent: %q", out)
	}
	r.must("mkdir Full")
	r.doc("d", "x", r.folderID("Full"))
	r.sh.DryRun = true
	if err := r.run("rmdir Full"); err == nil {
		t.Error("a dry run does not claim a non-empty folder can go")
	}
	if err := r.run("mkdir Missing/Child"); err == nil {
		t.Error("a dry run does not claim a folder under a missing parent")
	}
}

func TestRmWithFoldersAndOverlappingPatterns(t *testing.T) {
	r := newRig(t, "n\n")
	r.doc("a", "x", "")
	r.doc("b", "y", "")
	r.must("mkdir F")
	r.must("rm '*'")
	if got := r.names(""); len(got) != 0 {
		t.Fatalf("the documents are trashed: %v", got)
	}
	if !strings.Contains(r.err.String(), "skipped F/") {
		t.Errorf("the folder is skipped with a note: %q", r.err.String())
	}
	if err := r.run("rm F"); err == nil {
		t.Error("only a folder matches")
	}
	out := r.must("purge a 'a*' b")
	if !strings.Contains(out, "Delete 2 documents") || strings.Count(out, "  a\n") != 1 {
		t.Errorf("the count is de-duplicated: %q", out)
	}
	for i := 0; i < 12; i++ {
		r.doc(fmt.Sprintf("n%02d", i), "x", "")
	}
	r.must("rm 'n*'")
	r.sh.In = strings.NewReader("")
	r.sh.in = nil
	out = r.must("purge 'n*'")
	if !strings.Contains(out, "Delete 12 documents") || !strings.Contains(out, "... and 2 more") {
		t.Errorf("up to ten names: %q", out)
	}
}

func TestPatternCharactersAreLiteral(t *testing.T) {
	r := newRig(t, "")
	r.doc("a[1]", "x", "")
	r.doc("a1", "x", "")
	r.doc(`b\c`, "x", "")
	if out := r.must("ls 'a[1]'"); out != "a[1]\n" {
		t.Errorf("[ ] are literal: %q", out)
	}
	if out := r.must(`ls 'b\c'`); out != "b\\c\n" {
		t.Errorf("a backslash in quotes is literal: %q", out)
	}
	if out := r.must("ls 'a?1*'"); out != "a[1]\n" {
		t.Errorf("? and * still work: %q", out)
	}
}

func TestSplitKeepsOtherBackslashes(t *testing.T) {
	got, err := Split(`import C:\docs\a.md 'x\y' "q\"r" a\\b`)
	want := []string{"import", `C:\docs\a.md`, `x\y`, `q"r`, `a\b`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%q %v", got, err)
	}
}

func TestFlagsNeedValues(t *testing.T) {
	r := newRig(t, "")
	r.doc("a", "x", "")
	for _, l := range []string{"import -d", "export a -o", "export a -f", "export -o"} {
		if err := r.run(l); err == nil {
			t.Errorf("%q should fail", l)
		}
	}
}

func TestPackAndExportDoNotClobber(t *testing.T) {
	r := newRig(t, "")
	dir := t.TempDir()
	a := r.doc("Same", "one", "")
	r.must("mkdir F")
	b := r.doc("Same", "two", r.folderID("F"))
	out := filepath.Join(dir, "out")
	r.must("export -f markdown -o " + out + " Same F/Same")
	files, _ := filepath.Glob(filepath.Join(out, "*.md"))
	if len(files) != 2 {
		t.Fatalf("two same-title documents give two files: %v (%s %s)", files, a, b)
	}
	if err := r.run("export -f markdown -o " + out + " Same"); err == nil {
		t.Error("an existing file is not replaced without --force")
	}
	r.must("export -f markdown -o " + out + " --force Same")
	pk := filepath.Join(dir, "p.opendoc")
	r.must("pack " + pk)
	before, _ := os.ReadFile(pk)
	if err := r.run("pack " + pk); err == nil {
		t.Error("pack does not replace a file")
	}
	if after, _ := os.ReadFile(pk); !bytes.Equal(before, after) {
		t.Error("the file is untouched")
	}
	r.must("pack --force " + pk)
	// A failure leaves no partial file and no temporary file.
	bad := filepath.Join(dir, "nodir", "x.opendoc")
	if err := r.run("pack " + bad); err == nil {
		t.Error("an unwritable path")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".docsh-*")); len(left) != 0 {
		t.Errorf("leftover %v", left)
	}
}

func TestScriptRebuildDeclinedIsAnError(t *testing.T) {
	r := newRig(t, "rebuild\nmkdir After\n")
	r.sh.Script = true
	err := r.sh.Interact(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("%v", err)
	}
	if fs, _ := r.s.Folders(context.Background(), "m"); len(fs) != 0 {
		t.Errorf("the script stopped: %+v", fs)
	}
}

func TestScriptImportOfUnsupportedFileIsAnError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "x.unknownext")
	if err := os.WriteFile(bad, []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, "")
	if err := r.run("import " + bad); err != nil {
		t.Skipf("this file is importable here: %v", err)
	}
	if !strings.Contains(r.err.String(), "skipped") {
		t.Errorf("interactively it is skipped with a warning: %q", r.err.String())
	}
	r.sh.Script = true
	if err := r.run("import " + bad); err == nil {
		t.Error("in a script an unsupported file is an error")
	}
}

func TestUnpackReportsSignatureAndFailures(t *testing.T) {
	r := newRig(t, "")
	r.doc("a", "text", "")
	pk := filepath.Join(t.TempDir(), "p.opendoc")
	r.must("pack " + pk)
	other := newRig(t, "")
	out := other.must("unpack " + pk)
	if !strings.Contains(out, "not signed") {
		t.Errorf("it says whether the pack is signed: %q", out)
	}
	// A store that cannot save: every document fails, and that is an error.
	broken := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: failingSave{docstore.NewMemoryAdapter()}}}}
	b := &rig{t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, s: broken}
	b.sh = New(broken, "m", strings.NewReader(""), b.out, b.err)
	b.sh.Script = true
	if err := b.run("unpack " + pk); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("a failed document is an error: %v", err)
	}
	if !strings.Contains(b.err.String(), "failed:") {
		t.Errorf("and is listed: %q", b.err.String())
	}
}

type failingSave struct{ docstore.Adapter }

// Save fails for documents; the catalog (its index and folders) still saves.
func (f failingSave) Save(ctx context.Context, id string, data []byte) error {
	if strings.HasPrefix(id, "catalog:") {
		return f.Adapter.Save(ctx, id, data)
	}
	return errors.New("disk full")
}
