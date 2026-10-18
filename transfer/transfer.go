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
	"regexp"
	"sort"
	"strconv"
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
		if c.Ext() == ".json" {
			r.AddImporter(wrapJSON(codec{c}))
		} else {
			r.AddImporter(codec{c})
		}
		r.AddExporter(codec{c})
	}
	registerTextFormats(r)
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
	src, notes := takeFootnotes(src)
	md := canvas.Markdown{Images: dataImage}
	d := docfile.Document{Title: firstHeading(src), Content: markFootnotes(docfile.FromItems(md.Transform(src)), notes)}
	for _, n := range notes.list {
		d.Footnotes = append(d.Footnotes, docfile.Footnote{ID: n.id, Text: n.text})
	}
	return d, nil
}

// Footnotes in Markdown: a reference [^label] in the text and a definition "[^label]: the note" on a line of its own
// (indented lines after it continue the note). Each reference with a definition becomes a footnote mark, numbered by
// the order of the references; a reference with no definition stays as the text it was.
const (
	noteOpen  = "\ue000"
	noteClose = "\ue001"
)

var (
	reNoteDef = regexp.MustCompile(`^\[\^([^\]\s]+)\]:[ \t]*(.*)$`)
	reNoteRef = regexp.MustCompile(`\[\^([^\]\s]+)\]`)
)

type mdNote struct{ id, text string }

type mdNotes struct {
	defs map[string]string
	list []mdNote // in the order the references come
}

// takeFootnotes removes the definitions from the source and returns it with the references marked.
func takeFootnotes(src string) (string, *mdNotes) {
	n := &mdNotes{defs: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var kept []string
	fenced := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
		}
		if m := reNoteDef.FindStringSubmatch(l); m != nil && !fenced {
			text := strings.TrimSpace(m[2])
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "    ") || strings.HasPrefix(lines[i+1], "\t")) {
				i++
				text += " " + strings.TrimSpace(lines[i])
			}
			n.defs[m[1]] = strings.TrimSpace(text)
			continue
		}
		kept = append(kept, l)
	}
	if len(n.defs) == 0 {
		return src, n
	}
	seen := map[string]string{} // the label's footnote id in the document: n1, n2, ... by order of reference
	fenced = false
	for i, l := range kept {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue // code is not read for notes
		}
		kept[i] = reNoteRef.ReplaceAllStringFunc(l, func(ref string) string {
			id := reNoteRef.FindStringSubmatch(ref)[1]
			text, ok := n.defs[id]
			if !ok {
				return ref
			}
			if _, ok := seen[id]; !ok { // a note referred to twice is one note
				seen[id] = "n" + strconv.Itoa(len(n.list)+1)
				n.list = append(n.list, mdNote{seen[id], text})
			}
			return noteOpen + seen[id] + noteClose
		})
	}
	out := strings.Join(kept, "\n")
	return out, n
}

// markFootnotes turns the markers left in the text into footnote marks.
func markFootnotes(items []docfile.Item, n *mdNotes) []docfile.Item {
	if len(n.list) == 0 {
		return items
	}
	var out []docfile.Item
	for _, it := range items {
		if it.Chars == nil || !strings.Contains(it.Chars.Text, noteOpen) {
			out = append(out, it)
			continue
		}
		rest := it.Chars.Text
		for {
			i := strings.Index(rest, noteOpen)
			if i < 0 {
				break
			}
			j := strings.Index(rest[i:], noteClose)
			if j < 0 {
				break
			}
			if i > 0 {
				out = append(out, docfile.Item{Chars: &docfile.Chars{Text: rest[:i], Format: it.Chars.Format}})
			}
			out = append(out, docfile.Item{Footnote: &docfile.FootnoteMark{ID: rest[i+len(noteOpen) : i+j]}})
			rest = rest[i+j+len(noteClose):]
		}
		if rest != "" {
			out = append(out, docfile.Item{Chars: &docfile.Chars{Text: rest, Format: it.Chars.Format}})
		}
	}
	return out
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

// MarkdownSkipped counts what a Markdown file holds that Import leaves out or only keeps as text.
type MarkdownSkipped struct {
	RemoteImages int // pictures given by an address that is not a data: URL (not fetched; their alternative text stays)
	CellMarks    int // table cells whose emphasis, code or links are shown as plain text (a cell holds text only)
	HTML         int // lines with HTML tags, which are shown as the text they are
}

var (
	reMDImage = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)`)
	reMDHTML  = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9]*(\s[^>]*)?/?>`)
)

// InspectMarkdown counts what Import leaves out of a Markdown file (code blocks are not read).
func InspectMarkdown(data []byte) MarkdownSkipped {
	var s MarkdownSkipped
	fenced := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range reMDImage.FindAllStringSubmatch(line, -1) {
			if !strings.HasPrefix(m[1], "data:image/") {
				s.RemoteImages++
			}
		}
		if reMDHTML.MatchString(line) {
			s.HTML++
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "|") {
			// a table row: the cells that have marks
			for _, c := range strings.Split(strings.Trim(t, "|"), "|") {
				if c = strings.TrimSpace(c); strings.TrimLeft(c, ":-") != "" && canvas.StripMarkdown(c) != c {
					s.CellMarks++
				}
			}
		}
	}
	return s
}
