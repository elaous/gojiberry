package transfer

import (
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

func TestDefaultFormatsRoundTrip(t *testing.T) {
	r := Default()
	doc := docfile.Document{Title: "My/Doc", Content: docfile.FromItems([]canvas.Item{canvas.Chars{Text: "hello\nworld"}})}
	for _, name := range []string{"json", "proto-text", "proto"} {
		file, data, mime, err := r.Export(name, doc)
		if err != nil || mime == "" || !strings.HasPrefix(file, "My-Doc.") {
			t.Fatalf("%s: %q %q %v", name, file, mime, err)
		}
		back, err := r.Import("", file, data)
		if err != nil || back.Title != "My/Doc" {
			t.Fatalf("%s: %+v %v", name, back, err)
		}
	}
	_, data, _, _ := r.Export("text", doc)
	if string(data) != "hello\nworld" {
		t.Fatalf("%q", data)
	}
	back, err := r.Import("text", "", data)
	if err != nil || len(back.Content) != 1 {
		t.Fatal(back, err)
	}
	_, md, _, _ := r.Export("markdown", docfile.Document{Content: docfile.FromItems([]canvas.Item{canvas.Chars{Text: "bold", Format: canvas.Format{FontWeight: 700}}})})
	if string(md) != "**bold**" {
		t.Fatalf("%q", md)
	}
	back, err = r.Import("", "notes.MD", []byte("# Title\n\ntext"))
	if err != nil || len(back.Content) == 0 {
		t.Fatalf("%+v %v", back, err)
	}
	if file, _, _, _ := r.Export("text", docfile.Document{}); file != "document.txt" {
		t.Fatal(file)
	}
}

type fake struct{ name string }

func (f fake) Format() Format {
	return Format{Name: f.name, Label: f.name, Exts: []string{"." + f.name}, MIME: "x/" + f.name}
}
func (fake) Import([]byte) (docfile.Document, error) { return docfile.Document{Title: "fake"}, nil }
func (fake) Export(docfile.Document) ([]byte, error) { return nil, errors.New("no") }

func TestRegistry(t *testing.T) {
	r := &Registry{}
	r.AddImporter(fake{"a"})
	r.AddImporter(fake{"b"})
	r.AddImporter(fake{"a"}) // replaces
	r.AddExporter(fake{"a"})
	r.AddExporter(fake{"a"})
	if len(r.Importers()) != 2 || len(r.Exporters()) != 1 {
		t.Fatal(len(r.Importers()), len(r.Exporters()))
	}
	if r.Accept() != ".a,.b" {
		t.Fatal(r.Accept())
	}
	if _, err := r.Import("nope", "", nil); !errors.Is(err, ErrUnknownFormat) {
		t.Fatal(err)
	}
	if _, err := r.Import("", "x.zzz", nil); !errors.Is(err, ErrUnknownFormat) {
		t.Fatal(err)
	}
	if d, err := r.Import("b", "", nil); err != nil || d.Title != "fake" {
		t.Fatal(err)
	}
	if _, _, _, err := r.Export("nope", docfile.Document{}); !errors.Is(err, ErrUnknownFormat) {
		t.Fatal(err)
	}
	if _, _, _, err := r.Export("a", docfile.Document{}); err == nil {
		t.Fatal("exporter error passes through")
	}
}

func TestMarkdownImportTitleImagesAndFormulas(t *testing.T) {
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAYAAAD0In+KAAAAD0lEQVR4nGP4z8Dwn4EBAA0GAf+hVvJzAAAAAElFTkSuQmCC"
	md := "\ufeff# The Report\n\nIntro ![chart](" + png + ") and ![far](https://example.org/x.png).\n\n| Qty | Total |\n|---|---|\n| 3 | =A2*2 |\n"
	imp, err := Default().ImporterFor("report.md")
	if err != nil {
		t.Fatal(err)
	}
	d, err := imp.Import([]byte(md))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "The Report" {
		t.Fatalf("title %q", d.Title)
	}
	var img, table bool
	text := ""
	for _, it := range d.Content {
		switch {
		case it.Image != nil:
			img = it.Image.Alt == "chart" && strings.HasPrefix(it.Image.URL, "data:image/png")
		case it.Table != nil:
			table = it.Table.Rows[1].Cells[1] == "=A2*2"
		case it.Chars != nil:
			text += it.Chars.Text
		}
	}
	if !img || !table || !strings.Contains(text, "[far]") || strings.Contains(text, "example.org") {
		t.Fatalf("image %v table %v text %q", img, table, text)
	}
	if _, err := imp.Import([]byte{0xff, 0xfe, 0x00}); err == nil {
		t.Fatal("not UTF-8")
	}
}

func TestMarkdownFootnotesBecomeFootnotes(t *testing.T) {
	md := "# Notes\n\nFirst claim[^a] and a second[^long], then the first again[^a], and an unknown[^zz].\n\n[^a]: The first note.\n[^long]: A longer note\n    that goes on.\n\n```\n[^a]: not a definition in code\n```\n"
	d, err := markdown{}.Import([]byte(md))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Footnotes) != 2 || d.Footnotes[0].Text != "The first note." || d.Footnotes[1].Text != "A longer note that goes on." {
		t.Fatalf("notes: %+v", d.Footnotes)
	}
	var marks []string
	text := ""
	for _, it := range d.Content {
		switch {
		case it.Footnote != nil:
			marks = append(marks, it.Footnote.ID)
			text += "<" + it.Footnote.ID + ">"
		case it.Chars != nil:
			text += it.Chars.Text
		}
	}
	if strings.Join(marks, ",") != "n1,n2,n1" {
		t.Fatalf("marks %v in %q", marks, text)
	}
	if !strings.Contains(text, "unknown[^zz]") || !strings.Contains(text, "[^a]: not a definition in code") || strings.Contains(text, "The first note.") {
		t.Fatalf("an unknown reference stays text, code is not read, definitions are not in the text: %q", text)
	}
}
