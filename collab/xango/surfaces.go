package xango

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab"
)

type finder interface {
	Find(tag string, n int) any
	Each(tag string, f func(key string, n int, v any))
}

func put(m map[string]string, id string, v any) {
	if j, err := json.Marshal(v); err == nil {
		m[id] = string(j)
	}
}

func pageRecord(id, kind string) (int, bool) {
	rest, ok := strings.CutPrefix(id, kind+":")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 0
}

// "null" where a page sets none, so removing a header reaches others
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

// a Key shares it under that key: others never shift it
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
	Merges []canvas.Merge
	Styles []styledCell
	X, Y   float64
	Anchor *canvas.Anchor
}

// a map with struct keys does not go through JSON
type styledCell struct {
	Row, Col int
	Style    canvas.CellStyle
}

type imageRec struct {
	X, Y, W, H float64
	Anchor     *canvas.Anchor
	Alpha      float64
}

var objectKinds = []struct{ kind, tag string }{
	{"float", cxango.TagFloatingText}, {"table", cxango.TagTable}, {"image", cxango.TagImage},
}

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
		rec := tableRec{Cells: tv.Table.Cells, Merges: tv.Table.Merges, X: tv.X, Y: tv.Y, Anchor: tv.Anchor}
		for pos, st := range tv.Table.Styles {
			rec.Styles = append(rec.Styles, styledCell{pos.Row, pos.Col, st})
		}
		slices.SortFunc(rec.Styles, func(a, b styledCell) int { // the same record every time for the same table
			if a.Row != b.Row {
				return a.Row - b.Row
			}
			return a.Col - b.Col
		})
		put(out, "table:"+ref(key, n), rec)
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
			if len(r.Cells) > 0 {
				r.Merges = fitting(r.Merges, len(r.Cells), len(r.Cells[0]))
				styles := map[canvas.CellPos]canvas.CellStyle{}
				for _, sc := range r.Styles {
					styles[canvas.CellPos{Row: sc.Row, Col: sc.Col}] = sc.Style
				}
				if !reflect.DeepEqual(tv.Table.Cells, r.Cells) || !reflect.DeepEqual(tv.Table.Merges, r.Merges) || !reflect.DeepEqual(nonNil(tv.Table.Styles), styles) {
					tv.Table.Cells, tv.Table.Merges, tv.Table.Styles = r.Cells, r.Merges, styles
					if len(styles) == 0 {
						tv.Table.Styles = nil
					}
				}
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

func nonNil(m map[canvas.CellPos]canvas.CellStyle) map[canvas.CellPos]canvas.CellStyle {
	if m == nil {
		return map[canvas.CellPos]canvas.CellStyle{}
	}
	return m
}

func fitting(ms []canvas.Merge, rows, cols int) []canvas.Merge {
	var out []canvas.Merge
	for _, m := range ms {
		if m.Row >= 0 && m.Col >= 0 && m.Rows >= 1 && m.Cols >= 1 && m.Row+m.Rows <= rows && m.Col+m.Cols <= cols && (m.Rows > 1 || m.Cols > 1) {
			out = append(out, m)
		}
	}
	return out
}
