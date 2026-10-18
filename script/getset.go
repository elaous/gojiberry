package script

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The get/set requests: what a script can read of the document, and change, beyond running commands and inserting text.
// It covers what extended Markdown, text formatting, text styles, pictures and the document's metadata do, and nothing
// else. The host (the page) is a Document as well as a Host to offer them.
//
//	markdown                  the document as extended Markdown                       (Read)
//	setMarkdown  text         replace the document with the Markdown                  (Write)
//	selection                 {from, to, text}: rune offsets and the selected text    (Read)
//	setSelection data{from,to} select a range                                         (Write)
//	format                    the format of the selection (of the first selected character, or at the caret) (Read)
//	setFormat    data{bold,italic,underline,strike,color,background,family,size}  change the selection (Write)
//	styles                    the names of the paragraph styles                       (Read)
//	style                     the style of the paragraph at the caret                 (Read)
//	setStyle     name         apply a style to the selected paragraphs                (Write)
//	images                    [{index,alt,legend,credit,decorative,width,height}], never the picture itself (Read)
//	setImage     index, data{alt,legend,decorative}  describe a picture               (Write)
//	meta                      {title, language}                                       (Read)
//	setMeta      data{title,language}         change them                             (Write)

// Document is the editor as the get/set requests reach it. The host checks nothing about permission (Session does); it
// still refuses what it cannot do, with an error whose text the script reads.
type Document interface {
	Markdown() string
	SetMarkdown(md string) error
	Selection() Selection
	SetSelection(from, to int) error
	Format() FormatInfo
	SetFormat(FormatPatch) error
	Styles() []string
	Style() string
	SetStyle(name string) error
	Images() []ImageInfo
	SetImage(index int, p ImagePatch) error
	Meta() Meta
	SetMeta(MetaPatch) error
}

// Selection is a range of the text (rune offsets) and what it says.
type Selection struct {
	From int    `json:"from"`
	To   int    `json:"to"`
	Text string `json:"text"`
}

// FormatInfo is the format of some text.
type FormatInfo struct {
	Bold       bool    `json:"bold"`
	Italic     bool    `json:"italic"`
	Underline  bool    `json:"underline"`
	Strike     bool    `json:"strike"`
	Color      string  `json:"color,omitempty"`
	Background string  `json:"background,omitempty"`
	Family     string  `json:"family,omitempty"`
	Size       float64 `json:"size,omitempty"`
}

// FormatPatch changes some of a format: what is not there is left as it is; an empty colour, background or family
// takes it off.
type FormatPatch struct {
	Bold       *bool    `json:"bold"`
	Italic     *bool    `json:"italic"`
	Underline  *bool    `json:"underline"`
	Strike     *bool    `json:"strike"`
	Color      *string  `json:"color"`
	Background *string  `json:"background"`
	Family     *string  `json:"family"`
	Size       *float64 `json:"size"`
}

// ImageInfo describes a picture of the document (not its bytes).
type ImageInfo struct {
	Index      int     `json:"index"`
	Alt        string  `json:"alt"`
	Legend     string  `json:"legend"`
	Credit     string  `json:"credit"`
	Decorative bool    `json:"decorative"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
}

// ImagePatch changes a picture's description.
type ImagePatch struct {
	Alt        *string `json:"alt"`
	Legend     *string `json:"legend"`
	Decorative *bool   `json:"decorative"`
}

// Meta is the document's metadata.
type Meta struct {
	Title    string `json:"title"`
	Language string `json:"language"`
}

// MetaPatch changes some of it.
type MetaPatch struct {
	Title    *string `json:"title"`
	Language *string `json:"language"`
}

// Limits of the values a script may set.
const (
	MaxTitle = 200
	MaxAlt   = 1000
	MinSize  = 4.0
	MaxSize  = 400.0
)

var (
	reColor    = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{3,20})$`)
	reFamily   = regexp.MustCompile(`^[\p{L}\p{N} _-]{1,60}$`)
	reLanguage = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)
)

// Validate says whether the patch is within what a script may set.
func (p FormatPatch) Validate() error {
	for _, c := range []*string{p.Color, p.Background} {
		if c != nil && *c != "" && !reColor.MatchString(*c) {
			return errors.New("a colour is #rgb, #rrggbb or a colour name")
		}
	}
	if p.Family != nil && *p.Family != "" && !reFamily.MatchString(*p.Family) {
		return errors.New("a font family is letters, digits, spaces and dashes, up to 60 characters")
	}
	if p.Size != nil && (*p.Size != *p.Size || *p.Size < MinSize || *p.Size > MaxSize) {
		return errors.New("a font size is between 4 and 400")
	}
	return nil
}

// Validate says whether the patch is within what a script may set.
func (p ImagePatch) Validate() error {
	for _, s := range []*string{p.Alt, p.Legend} {
		if s != nil && utf8.RuneCountInString(*s) > MaxAlt {
			return errors.New("a description or legend is at most 1000 characters")
		}
	}
	return nil
}

// Validate says whether the patch is within what a script may set.
func (p MetaPatch) Validate() error {
	if p.Title != nil && utf8.RuneCountInString(*p.Title) > MaxTitle {
		return errors.New("a title is at most 200 characters")
	}
	if p.Language != nil && *p.Language != "" && !reLanguage.MatchString(*p.Language) {
		return errors.New("a language is a tag like en, fr or pt-BR")
	}
	return nil
}

// handleDocument answers the get/set requests; handled is false for a request that is not one of them.
func (s *Session) handleDocument(d Document, req Request) (out []byte, handled bool) {
	fail := func(err error) ([]byte, bool) { return reply(Response{ID: req.ID, Error: err.Error()}), true }
	ok := func(v any) ([]byte, bool) { return reply(Response{ID: req.ID, OK: true, Value: v}), true }
	need := func(p Permission) error {
		if !s.Allow[p] {
			return ErrDenied
		}
		return nil
	}
	data := func(v any) error {
		if len(req.Data) == 0 {
			return errors.New("missing data")
		}
		dec := json.NewDecoder(strings.NewReader(string(req.Data)))
		dec.DisallowUnknownFields()
		return dec.Decode(v)
	}
	switch req.Op {
	case "markdown", "selection", "format", "styles", "style", "images", "meta":
		if err := need(Read); err != nil {
			return fail(err)
		}
	case "setMarkdown", "setSelection", "setFormat", "setStyle", "setImage", "setMeta":
		if err := need(Write); err != nil {
			return fail(err)
		}
	default:
		return nil, false
	}
	switch req.Op {
	case "markdown":
		md := d.Markdown()
		if len(md) > MaxText {
			return fail(ErrTooLarge)
		}
		return ok(md)
	case "setMarkdown":
		if len(req.Text) > MaxText {
			return fail(ErrTooLarge)
		}
		if err := d.SetMarkdown(req.Text); err != nil {
			return fail(err)
		}
		return ok(true)
	case "selection":
		sel := d.Selection()
		if len(sel.Text) > MaxText {
			sel.Text = sel.Text[:MaxText]
		}
		return ok(sel)
	case "setSelection":
		var r struct{ From, To int }
		if err := data(&r); err != nil {
			return fail(err)
		}
		if err := d.SetSelection(r.From, r.To); err != nil {
			return fail(err)
		}
		return ok(true)
	case "format":
		return ok(d.Format())
	case "setFormat":
		var p FormatPatch
		if err := data(&p); err != nil {
			return fail(err)
		}
		if err := p.Validate(); err != nil {
			return fail(err)
		}
		if err := d.SetFormat(p); err != nil {
			return fail(err)
		}
		return ok(true)
	case "styles":
		return ok(d.Styles())
	case "style":
		return ok(d.Style())
	case "setStyle":
		if strings.TrimSpace(req.Name) == "" {
			return fail(errors.New("name the style"))
		}
		if err := d.SetStyle(req.Name); err != nil {
			return fail(err)
		}
		return ok(true)
	case "images":
		return ok(d.Images())
	case "setImage":
		var p ImagePatch
		if req.Index == nil {
			return fail(errors.New("say which picture (index)"))
		}
		if err := data(&p); err != nil {
			return fail(err)
		}
		if err := p.Validate(); err != nil {
			return fail(err)
		}
		if err := d.SetImage(*req.Index, p); err != nil {
			return fail(err)
		}
		return ok(true)
	case "meta":
		return ok(d.Meta())
	case "setMeta":
		var p MetaPatch
		if err := data(&p); err != nil {
			return fail(err)
		}
		if err := p.Validate(); err != nil {
			return fail(err)
		}
		if err := d.SetMeta(p); err != nil {
			return fail(err)
		}
		return ok(true)
	}
	return nil, false
}
