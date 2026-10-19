// Package textimport opens common text formats as they are, for reading: a JSON, YAML, TOML, INI or log file as a code
// block with its language, a CSV or TSV file as a table. Nothing is converted into another form of the same content: the
// text stays as written (JSON is indented when it is valid, nothing else is touched), and there is no way back to the file
// from the document; it is a copy to read, search, quote and print.
//
// It reads nothing it should not: a file over MaxBytes, one with NUL bytes (a binary file), or one that is not text in
// UTF-8, UTF-16 with a byte order mark, or Latin-1 is refused with a reason.
package textimport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// Limits.
const (
	MaxBytes = 5 << 20 // the largest file read
	MaxRows  = 5000    // rows of a table shown; the rest is said
	MaxCols  = 200
)

// Kinds of file.
const (
	JSON  = "json"
	JSONL = "jsonl"
	JSON5 = "json5"
	CSV   = "csv"
	TSV   = "tsv"
	YAML  = "yaml"
	TOML  = "toml"
	INI   = "ini"
	Log   = "log"
	XML   = "xml"
	// Text is any text file as it is (a code block, not painted); HTMLSource is an HTML file shown as its source, not
	// rendered (the HTML importer reads a page as a document).
	Text       = "anytext"
	HTMLSource = "htmlsource"
)

// Language is the code language a kind is shown with ("" for none).
var Language = map[string]string{JSON: "json", JSONL: "json", JSON5: "json5", YAML: "yaml", TOML: "toml", INI: "ini", Log: "", XML: "xml", Text: "", HTMLSource: "html"}

// Decode is the text of a file: UTF-8 (a byte order mark is dropped), UTF-16 with a byte order mark, or, when it is
// neither, Latin-1 if it has no control characters other than line breaks and tabs.
func Decode(data []byte) (string, error) {
	if len(data) > MaxBytes {
		return "", fmt.Errorf("textimport: the file is larger than %d MB", MaxBytes>>20)
	}
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}), bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		le := data[0] == 0xff
		u := make([]uint16, 0, len(data)/2)
		for i := 2; i+1 < len(data); i += 2 {
			if le {
				u = append(u, uint16(data[i])|uint16(data[i+1])<<8)
			} else {
				u = append(u, uint16(data[i])<<8|uint16(data[i+1]))
			}
		}
		return clean(string(utf16.Decode(u)))
	case bytes.Contains(data, []byte{0}):
		return "", errors.New("textimport: the file has NUL bytes: it is not text")
	case utf8.Valid(data):
		return clean(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))
	}
	runes := make([]rune, len(data)) // Latin-1: every byte is the character of that number
	for i, b := range data {
		runes[i] = rune(b)
	}
	return clean(string(runes))
}

func clean(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\f' || r == 0x7f {
			return "", errors.New("textimport: the file has control characters: it is not text")
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), nil
}

// Import reads data as kind (one of the constants above). The document says which format it is a reading copy of
// (docfile.SourceFormatKey), so the editor can open it for reading.
func Import(kind string, data []byte) (docfile.Document, error) {
	d, err := read(kind, data)
	if err == nil {
		d.Set(docfile.SourceFormatKey, kind)
		if src, derr := Decode(data); derr == nil {
			if msg := Problem(kind, src); msg != "" {
				d.Set(docfile.SourceProblemKey, msg)
			}
		}
	}
	return d, err
}

func read(kind string, data []byte) (docfile.Document, error) {
	src, err := Decode(data)
	if err != nil {
		return docfile.Document{}, err
	}
	switch kind {
	case CSV, TSV:
		delim := ','
		if kind == TSV {
			delim = '\t'
		}
		if d, ok := table(src, delim); ok {
			return d, nil
		}
		return code(src, "", ""), nil // not a table after all: the text as it is
	case JSON:
		var out bytes.Buffer
		if json.Valid([]byte(src)) && json.Indent(&out, []byte(strings.TrimSpace(src)), "", "  ") == nil {
			src = out.String()
		}
	case JSONL, JSON5, YAML, TOML, INI, Log, XML, Text, HTMLSource:
	default:
		return docfile.Document{}, fmt.Errorf("textimport: unknown kind %q", kind)
	}
	paint := Language[kind]
	switch kind {
	case Log:
		paint = "log"
	case HTMLSource:
		paint = "xml" // tags, attributes and comments are painted as in XML
	}
	return code(src, Language[kind], paint), nil
}

// MaxPaint is the largest text that is painted (a piece of formatting for each change of colour: a larger file is shown
// in one colour).
const MaxPaint = 256 << 10

// code is the text as a code block: a paragraph of the Code style with the language, in a monospace font on the usual
// background (so a document that does not carry the style still looks like code).
func code(src, lang, paint string) docfile.Document {
	src = strings.TrimRight(src, "\n") + "\n"
	mono := canvas.Format{FontFamily: "monospace", Background: "#eceef1"}
	items := []canvas.Item{canvas.ParaFormat{Style: "Code", CodeLang: lang}}
	pieces := []span{{src, normal}}
	if len(src) <= MaxPaint {
		pieces = highlight(paint, src)
	}
	for _, s := range pieces {
		f := mono
		if c := classColour[s.c]; c != "" {
			f.Color = c
			if classBold[s.c] {
				f.FontWeight = 700
			}
		}
		items = append(items, canvas.Chars{Text: s.text, Format: f})
	}
	return docfile.Document{Content: docfile.FromItems(items)}
}

// table is a CSV or TSV file as a table (the first row is the header), at most MaxRows x MaxCols.
func table(src string, delim rune) (docfile.Document, bool) {
	r := csv.NewReader(strings.NewReader(src))
	r.Comma, r.LazyQuotes, r.FieldsPerRecord, r.ReuseRecord = delim, true, -1, false
	var rows [][]string
	total, cols := 0, 1
	for {
		rec, err := r.Read()
		if err != nil {
			if errors.Is(err, errEOF) {
				break
			}
			return docfile.Document{}, false
		}
		total++
		if len(rows) >= MaxRows {
			continue
		}
		if len(rec) > MaxCols {
			rec = rec[:MaxCols]
		}
		for i, c := range rec {
			c = strings.Join(strings.Fields(strings.ReplaceAll(c, "\n", " ")), " ")
			if strings.HasPrefix(strings.TrimSpace(c), "=") { // a cell of a file is not a formula
				c = "\\" + c
			}
			rec[i] = c
		}
		cols = max(cols, len(rec))
		rows = append(rows, rec)
	}
	if len(rows) == 0 {
		return docfile.Document{}, false
	}
	items := []canvas.Item{canvas.Inline{Object: canvas.NewTableObject(canvas.NewTableFrom(rows)), Block: true}}
	if total > len(rows) {
		items = append(items, canvas.Chars{Text: fmt.Sprintf("Showing the first %d of %d rows.\n", len(rows), total)})
	} else {
		items = append(items, canvas.Chars{Text: "\n"})
	}
	return docfile.Document{Content: docfile.FromItems(items)}, true
}
