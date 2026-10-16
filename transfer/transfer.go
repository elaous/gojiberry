// Package transfer is the seam for importing documents from, and
// exporting them to, other file formats: an Importer turns a file into a
// docfile.Document, an Exporter turns a Document into a file, and a
// Registry lists what is available so a menu can offer it (see
// components.Transfer).
//
// It starts with the formats the library already writes (JSON, protobuf),
// plain text and Markdown; add Word, PDF, HTML and the like by
// registering more.
package transfer

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// Format describes a file format.
type Format struct {
	Name  string   // id ("markdown")
	Label string   // menu text ("Markdown")
	Exts  []string // extensions with the dot, the first is used for exports
	MIME  string
}

// Importer reads a file.
type Importer interface {
	Format() Format
	Import(data []byte) (docfile.Document, error)
}

// Exporter writes a file.
type Exporter interface {
	Format() Format
	Export(doc docfile.Document) ([]byte, error)
}

// Registry holds the importers and exporters on offer.
type Registry struct {
	importers []Importer
	exporters []Exporter
}

// ErrUnknownFormat is returned when no importer or exporter fits.
var ErrUnknownFormat = errors.New("transfer: unknown format")

// AddImporter and AddExporter register a format; one with the same name
// replaces the earlier one.
func (r *Registry) AddImporter(i Importer) {
	for k, o := range r.importers {
		if o.Format().Name == i.Format().Name {
			r.importers[k] = i
			return
		}
	}
	r.importers = append(r.importers, i)
}

// AddExporter registers an exporter.
func (r *Registry) AddExporter(e Exporter) {
	for k, o := range r.exporters {
		if o.Format().Name == e.Format().Name {
			r.exporters[k] = e
			return
		}
	}
	r.exporters = append(r.exporters, e)
}

// Importers lists the registered importers, in registration order.
func (r *Registry) Importers() []Importer { return append([]Importer(nil), r.importers...) }

// Exporters lists the registered exporters.
func (r *Registry) Exporters() []Exporter { return append([]Exporter(nil), r.exporters...) }

// Accept is the value for an <input type=file accept=...>: every
// extension an importer reads.
func (r *Registry) Accept() string {
	var exts []string
	for _, i := range r.importers {
		exts = append(exts, i.Format().Exts...)
	}
	sort.Strings(exts)
	return strings.Join(exts, ",")
}

// ImporterFor picks the importer for a file name by its extension.
func (r *Registry) ImporterFor(filename string) (Importer, error) {
	ext := strings.ToLower(path.Ext(filename))
	for _, i := range r.importers {
		for _, e := range i.Format().Exts {
			if e == ext {
				return i, nil
			}
		}
	}
	return nil, ErrUnknownFormat
}

// Import reads data as the named format ("" picks by filename).
func (r *Registry) Import(name, filename string, data []byte) (docfile.Document, error) {
	if name == "" {
		i, err := r.ImporterFor(filename)
		if err != nil {
			return docfile.Document{}, err
		}
		return i.Import(data)
	}
	for _, i := range r.importers {
		if i.Format().Name == name {
			return i.Import(data)
		}
	}
	return docfile.Document{}, ErrUnknownFormat
}

// Export writes doc as the named format and returns the file name (from
// the title) with its bytes and media type.
func (r *Registry) Export(name string, doc docfile.Document) (filename string, data []byte, mime string, err error) {
	for _, e := range r.exporters {
		f := e.Format()
		if f.Name != name {
			continue
		}
		data, err = e.Export(doc)
		title := strings.TrimSpace(doc.Title)
		if title == "" {
			title = "document"
		}
		return strings.NewReplacer("/", "-", "\\", "-").Replace(title) + f.Exts[0], data, f.MIME, err
	}
	return "", nil, "", ErrUnknownFormat
}

// Default returns a registry with the built-in formats: JSON, protobuf
// text and binary, plain text and Markdown.
func Default() *Registry {
	r := &Registry{}
	for _, c := range docfile.Formats() {
		r.AddImporter(codec{c})
		r.AddExporter(codec{c})
	}
	r.AddImporter(markdown{})
	r.AddExporter(markdown{})
	r.AddImporter(text{})
	r.AddExporter(text{})
	return r
}

type codec struct{ c docfile.Codec }

func (c codec) Format() Format {
	return Format{Name: c.c.Name(), Label: c.c.Label(), Exts: []string{c.c.Ext()}, MIME: c.c.MIME()}
}
func (c codec) Import(b []byte) (docfile.Document, error) { return c.c.Unmarshal(b) }
func (c codec) Export(d docfile.Document) ([]byte, error) { return c.c.Marshal(d) }

type markdown struct{}

func (markdown) Format() Format {
	return Format{Name: "markdown", Label: "Markdown", Exts: []string{".md", ".markdown"}, MIME: "text/markdown"}
}

// MaxMarkdownBytes is the largest Markdown file read.
const MaxMarkdownBytes = 16 << 20

// Import reads Markdown as the editor's own Markdown does (see canvas.Markdown): headings, emphasis, lists, quotes,
// code, rules, links and pipe tables, where a table cell that starts with = stays a formula. The title is the first
// heading. A picture whose address is a data: URL is kept with its alternative text; one on a server is not fetched
// (an importer makes no request for the author of a file), so it stays as its alternative text in brackets.
func (markdown) Import(b []byte) (docfile.Document, error) {
	if len(b) > MaxMarkdownBytes {
		return docfile.Document{}, errors.New("transfer: the Markdown file is too large")
	}
	if !utf8.Valid(b) {
		return docfile.Document{}, errors.New("transfer: the Markdown file is not UTF-8 text")
	}
	src := strings.TrimPrefix(string(b), "\ufeff")
	md := canvas.Markdown{Images: dataImage}
	return docfile.Document{Title: firstHeading(src), Content: docfile.FromItems(md.Transform(src))}, nil
}

// firstHeading is the text of the first "# " heading outside a code fence.
func firstHeading(src string) string {
	fenced := false
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "# ") {
			return canvas.StripMarkdown(strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "# "), "# ")))
		}
	}
	return ""
}

// dataImage makes an image of a data: URL of a picture the editor shows; any other address gives nil.
func dataImage(url, alt string) canvas.InlineObject {
	if !strings.HasPrefix(url, "data:image/png;base64,") && !strings.HasPrefix(url, "data:image/jpeg;base64,") &&
		!strings.HasPrefix(url, "data:image/gif;base64,") && !strings.HasPrefix(url, "data:image/webp;base64,") {
		return nil
	}
	w, h := 280.0, 210.0
	if _, payload, ok := strings.Cut(url, ","); ok {
		if raw, err := base64.StdEncoding.DecodeString(payload); err == nil {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
				w, h = float64(cfg.Width), float64(cfg.Height)
				if m := max(w, h); m > 280 {
					w, h = w*280/m, h*280/m
				}
			}
		}
	}
	im := canvas.NewImage(dataSource{url, w, h}, 0, 0)
	im.Placement = canvas.PlaceInline
	im.W, im.H, im.Alt = w, h, alt
	return im
}

// dataSource is a picture known only by its data: URL.
type dataSource struct {
	url  string
	w, h float64
}

func (d dataSource) Size() (float64, float64) { return d.w, d.h }
func (d dataSource) URL() string              { return d.url }

func (markdown) Export(d docfile.Document) ([]byte, error) {
	return []byte(canvas.MarkdownFromItems(docfile.ToItems(d.Content))), nil
}

type text struct{}

func (text) Format() Format {
	return Format{Name: "text", Label: "Plain text", Exts: []string{".txt"}, MIME: "text/plain"}
}

func (text) Import(b []byte) (docfile.Document, error) {
	return docfile.Document{Content: docfile.FromItems([]canvas.Item{canvas.Chars{Text: string(b)}})}, nil
}

func (text) Export(d docfile.Document) ([]byte, error) {
	var sb strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	return []byte(sb.String()), nil
}
