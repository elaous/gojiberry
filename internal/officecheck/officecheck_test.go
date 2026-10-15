// Package officecheck tests the importers and exporters against LibreOffice, a program that was not written
// with them: files it writes are imported, and files they write are opened by it. The tests are skipped when
// LibreOffice is not installed (GOJIBERRY_SOFFICE names the program; GOJIBERRY_REQUIRE_SOFFICE=1 makes its
// absence a failure).
package officecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/odtimport"
)

func soffice(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("GOJIBERRY_SOFFICE")
	if bin == "" {
		bin, _ = exec.LookPath("soffice")
	}
	if bin == "" {
		if os.Getenv("GOJIBERRY_REQUIRE_SOFFICE") == "1" {
			t.Fatal("LibreOffice is required and not installed")
		}
		t.Skip("LibreOffice is not installed")
	}
	return bin
}

// convert has LibreOffice convert files to format (a --convert-to argument) into a new directory and returns
// the directory.
func convert(t *testing.T, bin, infilter, format string, files ...string) string {
	t.Helper()
	out := t.TempDir()
	args := []string{"--headless", "--norestore", "-env:UserInstallation=file://" + filepath.Join(t.TempDir(), "profile")}
	if infilter != "" {
		args = append(args, "--infilter="+infilter)
	}
	args = append(args, "--convert-to", format, "--outdir", out)
	args = append(args, files...)
	if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil || strings.Contains(string(b), "Error") {
		t.Fatalf("LibreOffice %s: %v\n%s", format, err, b)
	}
	return out
}

func corpus(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/corpus/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	return files
}

// words is the text of a document as a list of words, whatever its layout.
func words(d docfile.Document) []string {
	var b strings.Builder
	for _, it := range d.Content {
		switch {
		case it.Chars != nil:
			b.WriteString(it.Chars.Text)
		case it.Table != nil:
			for _, r := range it.Table.Rows {
				for _, c := range r.Cells {
					b.WriteString(" " + c + " ")
				}
			}
		}
		b.WriteString(" ")
	}
	return normal(strings.Fields(b.String()))
}

// normal takes the punctuation off the ends of words (a full stop may be a word of its own in a result).
func normal(ws []string) []string {
	var out []string
	for _, w := range ws {
		if w = strings.Trim(w, ".,;:!?\u060c"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func stats(d docfile.Document) (headings, lists, tables int) {
	for _, it := range d.Content {
		switch {
		case it.Chars != nil && it.Chars.Format.Scale > 1:
			headings++
		case it.Para != nil && it.Para.List.Kind != 0:
			lists++
		case it.Table != nil:
			tables++
		}
	}
	return
}

// Documents written by LibreOffice (from the corpus of HTML sources) are read by the Word and OpenDocument
// importers: every word of the source is in the result, in order, and the structure is there.
func TestImportersReadWhatLibreOfficeWrites(t *testing.T) {
	bin := soffice(t)
	src := corpus(t)
	for _, format := range []string{"docx", "odt"} {
		dir := convert(t, bin, "HTML (StarWriter)", map[string]string{"docx": "docx:MS Word 2007 XML", "odt": "odt:writer8"}[format], src...)
		for _, f := range src {
			name := strings.TrimSuffix(filepath.Base(f), ".html")
			data, err := os.ReadFile(filepath.Join(dir, name+"."+format))
			if err != nil {
				t.Errorf("%s.%s: %v", name, format, err)
				continue
			}
			var doc docfile.Document
			if format == "docx" {
				doc, err = docximport.Import(data)
			} else {
				doc, err = odtimport.Import(data)
			}
			if err != nil {
				t.Errorf("%s.%s: import: %v", name, format, err)
				continue
			}
			want := htmlWords(t, f)
			got := words(doc)
			if i := missing(want, got); i >= 0 {
				t.Errorf("%s.%s: word %d %q of the source is not in the result, in order\n got: %q", name, format, i, want[i], got)
			}
			h, l, tb := stats(doc)
			t.Logf("%s.%s: %d words, %d headings, %d list items, %d tables", name, format, len(got), h, l, tb)
			switch name {
			case "memo":
				if h < 2 || l < 6 || tb != 1 {
					t.Errorf("memo.%s: %d headings, %d list items, %d tables", format, h, l, tb)
				}
			case "report":
				if h < 4 || tb != 1 {
					t.Errorf("report.%s: %d headings, %d tables", format, h, tb)
				}
			}
		}
	}
}

// htmlWords is the text of an HTML source, as words, with the tags taken out.
func htmlWords(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if i := strings.Index(s, "<body"); i >= 0 {
		s = s[i:]
	}
	var out strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
			out.WriteString(" ")
		case r == '>':
			in = false
		case !in:
			out.WriteRune(r)
		}
	}
	return normal(strings.Fields(strings.NewReplacer("&amp;", "&", "&lt;", "<").Replace(out.String())))
}

// missing returns the index of the first word of want that is not found, in order, in got; -1 if all are.
func missing(want, got []string) int {
	j := 0
	for i, w := range want {
		for j < len(got) && got[j] != w {
			j++
		}
		if j == len(got) {
			return i
		}
		j++
	}
	return -1
}

// The files the exporters write open in LibreOffice and read as the document does: converted to text, the
// words of the document are there, in order.
func TestLibreOfficeOpensWhatTheExportersWrite(t *testing.T) {
	bin := soffice(t)
	c := func(text string, f docfile.Format) docfile.Item {
		return docfile.Item{Chars: &docfile.Chars{Text: text, Format: f}}
	}
	doc := docfile.Document{Title: "Open me", Content: []docfile.Item{
		c("A heading", docfile.Format{Scale: 2}), c("\n", docfile.Format{}),
		c("Some ", docfile.Format{}), c("bold", docfile.Format{FontWeight: 700}), c(" and ", docfile.Format{}),
		c("a link", docfile.Format{Link: "https://example.com/x?a=1&b=2"}), c(" in H", docfile.Format{}), c("2", docfile.Format{Subscript: true}), c("O\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 2}}}, c("one\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 2}}}, c("two\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 1}}}, c("bullet\n", docfile.Format{}),
		{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"alpha", "beta"}}, {Cells: []string{"gamma", "delta"}}}}},
		c("\nThe end of ünïcode text", docfile.Format{}),
	}}
	want := htmlTextWords("A heading Some bold and a link in H2O one two bullet alpha beta gamma delta The end of ünïcode text")
	snap := export.FromDocument(doc)
	exporters := map[string]export.Exporter{"docx": export.DOCX{}, "odt": export.ODT{}, "rtf": export.RTF{}}
	dir := t.TempDir()
	for ext, ex := range exporters {
		data, err := ex.Export(snap)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		f := filepath.Join(dir, "doc."+ext)
		if err := os.WriteFile(f, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// one directory per format: LibreOffice names the output after the input
		out := convert(t, bin, "", "txt:Text (encoded):UTF8", f)
		txt, err := os.ReadFile(filepath.Join(out, "doc.txt"))
		if err != nil {
			t.Errorf("%s: LibreOffice did not open the file: %v", ext, err)
			continue
		}
		got := normal(strings.Fields(strings.TrimPrefix(string(txt), "\ufeff")))
		if i := missing(want, got); i >= 0 {
			t.Errorf("%s: word %d %q is not in what LibreOffice reads, in order\n got: %q", ext, i, want[i], got)
		}
	}
}

func htmlTextWords(s string) []string { return normal(strings.Fields(s)) }
