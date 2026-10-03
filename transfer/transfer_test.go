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
