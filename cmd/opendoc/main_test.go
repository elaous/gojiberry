package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

func do(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("opendoc %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestPackListVerifyUnpackSignRecover(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "Reports", "2026"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "notes.md"), []byte("# Notes\n\nhello"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "Reports", "q1.txt"), []byte("first quarter"), 0o644)
	// A .docx made by the library's own exporter.
	reg := registry()
	_, docx, _, err := reg.Export("docx", docfile.Document{Title: "Budget", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "numbers"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(src, "Reports", "2026", "budget.docx"), docx, 0o644)
	_ = os.WriteFile(filepath.Join(src, "junk.bin"), []byte{0xff, 0x00, 0x01}, 0o644)

	key := filepath.Join(dir, "key")
	_ = os.WriteFile(key, []byte(strings.TrimSpace(do(t, "keygen"))+"\n"), 0o600)
	pk := filepath.Join(dir, "work.opendoc")
	if out := do(t, "pack", "-sign", key, "-name", "Work", pk, src); !strings.Contains(out, "3 documents") {
		t.Fatalf("%s", out)
	}
	list := do(t, "list", pk)
	for _, want := range []string{"Work", "3 documents", "2 folders", "signed by", "valid: true", "notes", "q1", "budget", "2026"} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}
	if out := do(t, "verify", pk); !strings.Contains(out, "3 documents checked") || strings.Contains(out, "BAD") {
		t.Fatalf("%s", out)
	}
	un := filepath.Join(dir, "un")
	do(t, "unpack", pk, un)
	for _, p := range []string{"notes.open", "Reports/q1.open", "Reports/2026/budget.open"} {
		data, err := os.ReadFile(filepath.Join(un, p))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if _, err := docfile.Open.Unmarshal(data); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	// Sign again with a new key, in place.
	key2 := filepath.Join(dir, "key2")
	_ = os.WriteFile(key2, []byte(strings.TrimSpace(do(t, "keygen"))+"\n"), 0o600)
	do(t, "sign", "-key", key2, pk)
	if out := do(t, "verify", pk); !strings.Contains(out, "valid: true") {
		t.Fatalf("%s", out)
	}
	// A cut pack: verify fails, recover keeps what is whole.
	data, _ := os.ReadFile(pk)
	cutPath := filepath.Join(dir, "cut.opendoc")
	_ = os.WriteFile(cutPath, data[:len(data)-200], 0o644)
	if err := run([]string{"verify", cutPath}, &bytes.Buffer{}); err == nil {
		t.Fatal("a cut pack verified")
	}
	rec := filepath.Join(dir, "rec.opendoc")
	if out := do(t, "recover", cutPath, rec); !strings.Contains(out, "3 documents recovered") {
		t.Fatalf("%s", out)
	}
	if out := do(t, "verify", rec); strings.Contains(out, "BAD") {
		t.Fatalf("%s", out)
	}
}

func fails(t *testing.T, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	if err == nil {
		t.Fatalf("opendoc %v succeeded:\n%s", args, out.String())
	}
	return err
}

func TestShortIDsAndHostilePacksDoNotPanic(t *testing.T) {
	for _, id := range []string{"", "a", "abcdefgh", "abcdefghijkl", "../../etc/passwd"} {
		if s := shortID(id); len(s) > 8 || strings.ContainsAny(s, `/\`) {
			t.Errorf("shortID(%q) = %q", id, s)
		}
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"empty": nil, "junk": bytes.Repeat([]byte{0xff}, 400)} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, data, 0o644)
		for _, cmd := range []string{"unpack", "list", "verify", "recover"} {
			args := []string{cmd, p}
			if cmd == "unpack" || cmd == "recover" {
				args = append(args, filepath.Join(dir, "o-"+name+cmd))
			}
			_ = run(args, &bytes.Buffer{}) // an error, not a panic
		}
		if _, err := os.Stat(filepath.Join(dir, "o-"+name+"recover")); err == nil {
			t.Errorf("recover of %s left an output", name)
		}
	}
}

func TestRecoverNeverTruncatesTheSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.opendoc")
	_ = os.WriteFile(filepath.Join(dir, "n.md"), []byte("hello"), 0o644)
	do(t, "pack", src, filepath.Join(dir, "n.md"))
	before, _ := os.ReadFile(src)
	fails(t, "recover", src, src)
	fails(t, "recover", src, filepath.Join(dir, ".", "a.opendoc"))
	link := filepath.Join(dir, "link.opendoc")
	if err := os.Symlink(src, link); err == nil {
		fails(t, "recover", src, link)
	}
	if after, _ := os.ReadFile(src); !bytes.Equal(before, after) {
		t.Fatal("the source changed")
	}
}

func TestPackLeavesNoPartialOutputAndSkipsItself(t *testing.T) {
	dir := t.TempDir()
	// Nothing to pack from: an error, and no output file (nor a temporary one) is left.
	out := filepath.Join(dir, "out.opendoc")
	fails(t, "pack", out, filepath.Join(dir, "missing"))
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
	// The output inside the input folder is not packed into itself, even on a second run.
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "n.md"), []byte("# n"), 0o644)
	out = filepath.Join(src, "all.opendoc")
	for i := 0; i < 2; i++ {
		if got := do(t, "pack", out, src); !strings.Contains(got, "1 documents") || strings.Contains(got, "skipped") {
			t.Fatalf("run %d: %s", i, got)
		}
	}
	if ents, _ := os.ReadDir(src); len(ents) != 2 {
		t.Errorf("left behind: %v", ents)
	}
}

func TestKeygen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "k")
	got := do(t, "keygen", p)
	if !strings.Contains(got, "public key") {
		t.Fatal(got)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
	if _, err := readKey(p); err != nil {
		t.Error(err)
	}
	fails(t, "keygen", p) // never overwrites a key
	if seed := strings.TrimSpace(do(t, "keygen")); len(seed) != 64 {
		t.Errorf("%q", seed)
	}
}

func TestListEscapesNamesAndVerifyPinsTheKey(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "n.md"), []byte("# \x1b[31mred\x1b[0m title"), 0o644)
	kp := filepath.Join(dir, "k")
	pub := strings.TrimSpace(strings.TrimPrefix(do(t, "keygen", kp), kp+" written; public key"))
	pk := filepath.Join(dir, "p.opendoc")
	do(t, "pack", "-sign", kp, "-name", "evil\x1b[2J‮name", pk, src)
	list := do(t, "list", pk)
	if strings.ContainsAny(list, "\x1b‮") || !strings.Contains(list, `\u001b`) {
		t.Errorf("unescaped:\n%q", list)
	}
	if got := do(t, "verify", "-pubkey", pub, pk); strings.Contains(got, "BAD") {
		t.Error(got)
	}
	other := strings.Repeat("ab", 32)
	fails(t, "verify", "-pubkey", other, pk)
	fails(t, "verify", "-pubkey", "nothex", pk)
	if got := do(t, "verify", pk); !strings.Contains(got, "not checked") {
		t.Error(got)
	}
}

func TestEsc(t *testing.T) {
	if got := esc("a\n\x1b[0m‮b "); strings.ContainsAny(got, "\n\x1b‮ ") {
		t.Errorf("%q", got)
	}
	if esc("plain ünïcode") != "plain ünïcode" {
		t.Error("changed text that is fine")
	}
}

func TestSafeKeepsNamesWithinFileNameLimits(t *testing.T) {
	long := strings.Repeat("é", 300) // 600 bytes
	got := safe(long)
	if len(got)+len(".open") > 255 || len(got) > 240 || !utf8.ValidString(got) {
		t.Fatalf("%d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// A cut at the middle of a rune goes back to its start.
	if got := safe(strings.Repeat("a", 239) + "日本"); got != strings.Repeat("a", 239) {
		t.Errorf("%q", got)
	}
	if got := safe(strings.Repeat("a", 239) + ". x"); got != strings.Repeat("a", 239) {
		t.Errorf("a name does not end with a dot or space: %q", got)
	}
}

func TestUnpackLongNamesCollisionsAndExistingFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(src, 0o755)
	base := strings.Repeat("日", 82) // 246 bytes, over the 240 limit, and fine as a file name with .md
	_ = os.WriteFile(filepath.Join(src, base+"A.md"), []byte("first"), 0o644)
	_ = os.WriteFile(filepath.Join(src, base+"B.md"), []byte("second"), 0o644)
	pk := filepath.Join(dir, "p.opendoc")
	do(t, "pack", pk, src)
	un := filepath.Join(dir, "un")
	if out := do(t, "unpack", pk, un); !strings.Contains(out, "2 documents written") {
		t.Fatalf("%s", out)
	}
	entries, err := os.ReadDir(un)
	if err != nil || len(entries) != 2 {
		t.Fatalf("%v %v", entries, err)
	}
	for _, e := range entries {
		if len(e.Name()) > 255 || !utf8.ValidString(e.Name()) {
			t.Errorf("file name of %d bytes", len(e.Name()))
		}
	}
	// A second run does not overwrite anything, and says so with an error.
	victim := filepath.Join(un, entries[0].Name())
	_ = os.WriteFile(victim, []byte("mine"), 0o644)
	var out bytes.Buffer
	err = run([]string{"unpack", pk, un}, &out)
	if err == nil || !strings.Contains(out.String(), "0 documents written") {
		t.Fatalf("%v %q", err, out.String())
	}
	if got, _ := os.ReadFile(victim); string(got) != "mine" {
		t.Errorf("an existing file was overwritten: %q", got)
	}
	// With --force it is replaced.
	if out := do(t, "unpack", "--force", pk, un); !strings.Contains(out, "2 documents written") {
		t.Fatalf("%s", out)
	}
	if got, _ := os.ReadFile(victim); string(got) == "mine" {
		t.Errorf("--force did not overwrite")
	}
	// A directory in the way of a document is a failure for that document alone.
	un2 := filepath.Join(dir, "un2")
	_ = os.MkdirAll(filepath.Join(un2, entries[0].Name()), 0o755)
	out.Reset()
	if err := run([]string{"unpack", "--force", pk, un2}, &out); err == nil || !strings.Contains(out.String(), "1 documents written") {
		t.Fatalf("%v %q", err, out.String())
	}
}

func TestRecoverWarnsLoudlyAboutEncryptedRecordsItCannotAttribute(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	w, err := pack.NewWriter(&buf, pack.Meta{ID: "p", Name: "Mixed", Created: 1700000000000000000})
	if err != nil {
		t.Fatal(err)
	}
	d := docfile.Document{Title: "one", Updated: 1700000000000000000, Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x"}}}}
	d.SetID(docfile.NewID())
	d.SetName("one")
	if err := w.Add(d); err != nil {
		t.Fatal(err)
	}
	if err := w.AddEncrypted(pack.Entry{ID: docfile.NewID(), Name: "Confidential document"}, []byte("SEALED envelope bytes")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(nil); err != nil {
		t.Fatal(err)
	}
	whole := filepath.Join(dir, "whole.opendoc")
	_ = os.WriteFile(whole, buf.Bytes(), 0o644)
	// Intact index: nothing lost, no warning.
	if out := do(t, "recover", whole, filepath.Join(dir, "a.opendoc")); strings.Contains(out, "WARNING") {
		t.Errorf("%s", out)
	}
	cut := filepath.Join(dir, "cut.opendoc")
	_ = os.WriteFile(cut, buf.Bytes()[:buf.Len()-10], 0o644)
	out := do(t, "recover", cut, filepath.Join(dir, "b.opendoc"))
	if !strings.Contains(out, "WARNING: 1 encrypted") {
		t.Errorf("no warning: %s", out)
	}
}
