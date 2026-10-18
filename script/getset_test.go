package script

import (
	"errors"
	"strings"
	"testing"
)

// docHost is a stand-in editor for the get/set requests.
type docHost struct {
	host
	md     string
	sel    Selection
	format FormatInfo
	style  string
	meta   Meta
	images []ImageInfo
	setFmt []FormatPatch
	fail   error
}

func (d *docHost) Markdown() string              { return d.md }
func (d *docHost) SetMarkdown(m string) error    { d.md = m; return d.fail }
func (d *docHost) Selection() Selection          { return d.sel }
func (d *docHost) SetSelection(f, t int) error   { d.sel.From, d.sel.To = f, t; return d.fail }
func (d *docHost) Format() FormatInfo            { return d.format }
func (d *docHost) SetFormat(p FormatPatch) error { d.setFmt = append(d.setFmt, p); return d.fail }
func (d *docHost) Styles() []string              { return []string{"Normal", "Heading 1"} }
func (d *docHost) Style() string                 { return d.style }
func (d *docHost) SetStyle(n string) error       { d.style = n; return d.fail }
func (d *docHost) Images() []ImageInfo           { return d.images }
func (d *docHost) Meta() Meta                    { return d.meta }
func (d *docHost) SetImage(i int, p ImagePatch) error {
	if i < 0 || i >= len(d.images) {
		return errors.New("no such picture")
	}
	if p.Alt != nil {
		d.images[i].Alt = *p.Alt
	}
	return d.fail
}
func (d *docHost) SetMeta(p MetaPatch) error {
	if p.Title != nil {
		d.meta.Title = *p.Title
	}
	if p.Language != nil {
		d.meta.Language = *p.Language
	}
	return d.fail
}

func TestGetRequiresReadAndSetRequiresWrite(t *testing.T) {
	d := &docHost{md: "# Title\n\nsecret", meta: Meta{Title: "Report", Language: "en"}}
	s := &Session{Host: d, Allow: map[Permission]bool{Run: true, Insert: true}}
	for _, op := range []string{`"markdown"`, `"selection"`, `"format"`, `"styles"`, `"style"`, `"images"`, `"meta"`} {
		if r := ask(t, s, `{"id":1,"op":`+op+`}`); r.OK || r.Error != ErrDenied.Error() {
			t.Errorf("%s without Read: %+v", op, r)
		}
	}
	s.Allow[Read] = true
	if r := ask(t, s, `{"id":1,"op":"markdown"}`); !r.OK || r.Value != "# Title\n\nsecret" {
		t.Errorf("markdown with Read: %+v", r)
	}
	for _, req := range []string{`{"id":2,"op":"setMarkdown","text":"x"}`, `{"id":2,"op":"setSelection","data":{"from":0,"to":1}}`, `{"id":2,"op":"setFormat","data":{"bold":true}}`,
		`{"id":2,"op":"setStyle","name":"Heading 1"}`, `{"id":2,"op":"setImage","index":0,"data":{"alt":"a"}}`, `{"id":2,"op":"setMeta","data":{"title":"x"}}`} {
		if r := ask(t, s, req); r.OK || r.Error != ErrDenied.Error() {
			t.Errorf("%s without Write: %+v", req, r)
		}
	}
	if d.md != "# Title\n\nsecret" || d.meta.Title != "Report" || len(d.setFmt) != 0 || d.style != "" {
		t.Fatalf("nothing changed: %+v", d)
	}
}

func TestGetAndSetWork(t *testing.T) {
	d := &docHost{md: "text", sel: Selection{From: 1, To: 3, Text: "ex"}, format: FormatInfo{Bold: true, Color: "#112233"},
		style: "Normal", meta: Meta{Title: "Report", Language: "en"}, images: []ImageInfo{{Index: 0, Alt: "old", Width: 100}}}
	s := &Session{Host: d, Allow: map[Permission]bool{Read: true, Write: true}}
	if r := ask(t, s, `{"id":1,"op":"selection"}`); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := ask(t, s, `{"id":2,"op":"setMarkdown","text":"# New"}`); !r.OK || d.md != "# New" {
		t.Errorf("%+v %q", r, d.md)
	}
	if r := ask(t, s, `{"id":3,"op":"setSelection","data":{"from":2,"to":5}}`); !r.OK || d.sel.From != 2 || d.sel.To != 5 {
		t.Errorf("%+v %+v", r, d.sel)
	}
	if r := ask(t, s, `{"id":4,"op":"setFormat","data":{"bold":false,"color":"#abc","size":18,"family":"Lato"}}`); !r.OK || len(d.setFmt) != 1 || *d.setFmt[0].Bold != false || *d.setFmt[0].Size != 18 {
		t.Errorf("%+v %+v", r, d.setFmt)
	}
	if r := ask(t, s, `{"id":5,"op":"setStyle","name":"Heading 1"}`); !r.OK || d.style != "Heading 1" {
		t.Errorf("%+v", r)
	}
	if r := ask(t, s, `{"id":6,"op":"setImage","index":0,"data":{"alt":"A harbour"}}`); !r.OK || d.images[0].Alt != "A harbour" {
		t.Errorf("%+v", r)
	}
	if r := ask(t, s, `{"id":7,"op":"setImage","index":4,"data":{"alt":"x"}}`); r.OK || r.Error != "no such picture" {
		t.Errorf("a picture that is not there: %+v", r)
	}
	if r := ask(t, s, `{"id":8,"op":"setMeta","data":{"title":"Annual report","language":"pt-BR"}}`); !r.OK || d.meta.Title != "Annual report" || d.meta.Language != "pt-BR" {
		t.Errorf("%+v %+v", r, d.meta)
	}
	if r := ask(t, s, `{"id":9,"op":"styles"}`); !r.OK {
		t.Errorf("%+v", r)
	}
	d.fail = errors.New("the document is read-only")
	if r := ask(t, s, `{"id":10,"op":"setStyle","name":"Normal"}`); r.OK || r.Error != "the document is read-only" {
		t.Errorf("the host's refusal is the script's error: %+v", r)
	}
}

func TestWhatAScriptMaySetIsChecked(t *testing.T) {
	d := &docHost{images: []ImageInfo{{}}}
	s := &Session{Host: d, Allow: map[Permission]bool{Read: true, Write: true}}
	long := strings.Repeat("x", MaxAlt+1)
	for _, req := range []string{
		`{"id":1,"op":"setFormat","data":{"color":"red; background:url(x)"}}`,
		`{"id":1,"op":"setFormat","data":{"background":"url(http://evil)"}}`,
		`{"id":1,"op":"setFormat","data":{"family":"a{b}"}}`,
		`{"id":1,"op":"setFormat","data":{"size":1000}}`,
		`{"id":1,"op":"setFormat","data":{"size":2}}`,
		`{"id":1,"op":"setFormat","data":{"bogus":true}}`,
		`{"id":1,"op":"setFormat"}`,
		`{"id":1,"op":"setImage","data":{"alt":"x"}}`,
		`{"id":1,"op":"setImage","index":0,"data":{"alt":"` + long + `"}}`,
		`{"id":1,"op":"setMeta","data":{"language":"not a tag"}}`,
		`{"id":1,"op":"setMeta","data":{"title":"` + strings.Repeat("t", MaxTitle+1) + `"}}`,
		`{"id":1,"op":"setStyle","name":"  "}`,
	} {
		if r := ask(t, s, req); r.OK {
			t.Errorf("should be refused: %.80s", req)
		}
	}
	if len(d.setFmt) != 0 || d.images[0].Alt != "" {
		t.Fatalf("nothing reached the host: %+v", d)
	}
	for _, req := range []string{`{"id":1,"op":"setFormat","data":{"color":""}}`, `{"id":1,"op":"setFormat","data":{"color":"#aabbcc","background":"yellow"}}`, `{"id":1,"op":"setMeta","data":{"language":""}}`} {
		if r := ask(t, s, req); !r.OK {
			t.Errorf("should be accepted: %s: %+v", req, r)
		}
	}
}

func TestAHostThatIsNotADocumentKnowsNoGetSet(t *testing.T) {
	s := &Session{Host: &host{}, Allow: map[Permission]bool{Read: true, Write: true}}
	if r := ask(t, s, `{"id":1,"op":"markdown"}`); r.OK || r.Error != ErrUnknown.Error() {
		t.Fatalf("%+v", r)
	}
}
