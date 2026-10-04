package xango

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab"
)

// The surfaces: parts of the wrapped canvas other than the main text, shared
// as one record per element (see collab.Surface). They look their elements up
// in the wrapper's children each time, so they keep working when a re-render
// replaces an element.

// finder is what a surface needs of the wrapper.
type finder interface {
	Find(tag string, n int) any
	Each(tag string, f func(key string, n int, v any))
}

func put(m map[string]string, id string, v any) {
	if j, err := json.Marshal(v); err == nil {
		m[id] = string(j)
	}
}

// pageRecord splits "header:3" into 3.
func pageRecord(id, kind string) (int, bool) {
	rest, ok := strings.CutPrefix(id, kind+":")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 0
}

// furniture shares the pages' headers (or footers and page numbers). Each
// page has a record, "null" where it sets none, so that removing a header
// reaches the others too.
type furniture struct {
	in     finder
	footer bool
}

func (f furniture) pages() *canvas.PageSet {
	ps, _ := f.in.Find(cxango.TagPage, 0).(*canvas.PageSet)
	return ps
}

func (f furniture) kinds() (hf, num string) {
	if f.footer {
		return "footer", "pagenum"
	}
	return "header", ""
}

func (f furniture) Owns(id string) bool {
	hf, num := f.kinds()
	if _, ok := pageRecord(id, hf); ok {
		return true
	}
	if num == "" {
		return false
	}
	_, ok := pageRecord(id, num)
	return ok
}

func (f furniture) Read() map[string]string {
	out := map[string]string{}
	ps := f.pages()
	if ps == nil {
		return out
	}
	hf, num := f.kinds()
	for i, pg := range ps.Pages {
		if f.footer {
			put(out, hf+":"+strconv.Itoa(i), pg.Footer)
			put(out, num+":"+strconv.Itoa(i), pg.PageNumber)
		} else {
			put(out, hf+":"+strconv.Itoa(i), pg.Header)
		}
	}
	return out
}

func (f furniture) Write(recs map[string]string) {
	ps := f.pages()
	if ps == nil {
		return
	}
	hf, num := f.kinds()
	for id, v := range recs {
		if i, ok := pageRecord(id, hf); ok && i < len(ps.Pages) {
			var h *canvas.HeaderFooter
			if json.Unmarshal([]byte(v), &h) != nil {
				continue
			}
			if f.footer {
				ps.Pages[i].Footer = h
			} else {
				ps.Pages[i].Header = h
			}
		} else if i, ok := pageRecord(id, num); ok && i < len(ps.Pages) {
			var n *canvas.PageNumberConfig
			if json.Unmarshal([]byte(v), &n) == nil {
				ps.Pages[i].PageNumber = n
			}
		}
	}
}

// objects shares floating text, tables and images. An element with a Key is
// shared under it ("float:intro"), so adding or removing others never shifts
// it; one without a Key goes by its place among the unkeyed ones of its kind
// ("float:#0"), which is only stable while the others do not change. Floating
// text is shared as plain text plus position, size and anchor, a table by its
// cells, an image by where it is and how big (its picture is not sent).
type objects struct{ in finder }

type floatRec struct {
	Text    string
	X, Y    float64
	W, H    float64
	Anchor  *canvas.Anchor
	Opacity float64
}

type tableRec struct {
	Cells  [][]string
	X, Y   float64
	Anchor *canvas.Anchor
}

type imageRec struct {
	X, Y, W, H float64
	Anchor     *canvas.Anchor
	Alpha      float64
}

var objectKinds = []struct{ kind, tag string }{
	{"float", cxango.TagFloatingText}, {"table", cxango.TagTable}, {"image", cxango.TagImage},
}

// ref names an element: its key, or "#n" by place among the unkeyed.
func ref(key string, n int) string {
	if key != "" {
		return key
	}
	return "#" + strconv.Itoa(n)
}

func (objects) Owns(id string) bool {
	for _, k := range objectKinds {
		if strings.HasPrefix(id, k.kind+":") {
			return true
		}
	}
	return false
}

// find returns the element of the kind that the reference names, or nil.
func (o objects) find(tag, want string) any {
	var found any
	o.in.Each(tag, func(key string, n int, v any) {
		if found == nil && ref(key, n) == want {
			found = v
		}
	})
	return found
}

func (o objects) Read() map[string]string {
	out := map[string]string{}
	o.in.Each(cxango.TagFloatingText, func(key string, n int, v any) {
		ft := v.(*canvas.FloatingText)
		put(out, "float:"+ref(key, n), floatRec{ft.Text(), ft.X, ft.Y, ft.Width, ft.Height, ft.Anchor, ft.Opacity})
	})
	o.in.Each(cxango.TagTable, func(key string, n int, v any) {
		tv := v.(*canvas.TableView)
		put(out, "table:"+ref(key, n), tableRec{tv.Table.Cells, tv.X, tv.Y, tv.Anchor})
	})
	o.in.Each(cxango.TagImage, func(key string, n int, v any) {
		im := v.(*canvas.Image)
		put(out, "image:"+ref(key, n), imageRec{im.X, im.Y, im.W, im.H, im.Anchor, im.Alpha})
	})
	return out
}

func (o objects) Write(recs map[string]string) {
	for id, v := range recs {
		kind, want, _ := strings.Cut(id, ":")
		switch kind {
		case "float":
			ft, _ := o.find(cxango.TagFloatingText, want).(*canvas.FloatingText)
			var r floatRec
			if ft == nil || json.Unmarshal([]byte(v), &r) != nil {
				continue
			}
			if ft.Text() != r.Text {
				ft.SetText(r.Text)
			}
			ft.X, ft.Y, ft.Width, ft.Height, ft.Anchor, ft.Opacity = r.X, r.Y, r.W, r.H, r.Anchor, r.Opacity
		case "table":
			tv, _ := o.find(cxango.TagTable, want).(*canvas.TableView)
			var r tableRec
			if tv == nil || json.Unmarshal([]byte(v), &r) != nil {
				continue
			}
			if !reflect.DeepEqual(tv.Table.Cells, r.Cells) && len(r.Cells) > 0 {
				tv.Table.Cells = r.Cells
			}
			tv.X, tv.Y, tv.Anchor = r.X, r.Y, r.Anchor
		case "image":
			im, _ := o.find(cxango.TagImage, want).(*canvas.Image)
			var r imageRec
			if im == nil || json.Unmarshal([]byte(v), &r) != nil {
				continue
			}
			im.X, im.Y, im.W, im.H, im.Anchor, im.Alpha = r.X, r.Y, r.W, r.H, r.Anchor, r.Alpha
		}
	}
}

// sharing is what a Flow shares: the text flow by default, the pages'
// headers, footers or objects on their own, or everything.
func (f Flow) sharing(in finder) (collab.Sharing, bool) {
	switch f {
	case "", FlowText:
		return collab.Sharing{}, true
	case FlowHeader:
		return collab.Sharing{NoText: true, Surfaces: []collab.Surface{furniture{in: in}}}, true
	case FlowFooter:
		return collab.Sharing{NoText: true, Surfaces: []collab.Surface{furniture{in: in, footer: true}}}, true
	case FlowObjects:
		return collab.Sharing{NoText: true, Surfaces: []collab.Surface{objects{in}}}, true
	case FlowAll:
		return collab.Sharing{Surfaces: []collab.Surface{furniture{in: in}, furniture{in: in, footer: true}, objects{in}}}, true
	}
	return collab.Sharing{}, false
}
