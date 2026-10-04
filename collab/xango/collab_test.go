package xango_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/collab/ws"
	collabxango "github.com/elaous/gojiberry/collab/xango"
	"github.com/elaous/gojiberry/xango"
)

// painter draws nothing and measures 8px a rune.
type painter struct{}

func (painter) SetFillStyle(string)               {}
func (painter) SetStrokeStyle(string)             {}
func (painter) SetLineWidth(float64)              {}
func (painter) SetFont(string)                    {}
func (painter) SetTextAlign(string)               {}
func (painter) SetTextBaseline(string)            {}
func (painter) FillRect(x, y, w, h float64)       {}
func (painter) StrokeRect(x, y, w, h float64)     {}
func (painter) ClearRect(x, y, w, h float64)      {}
func (painter) BeginPath()                        {}
func (painter) MoveTo(x, y float64)               {}
func (painter) LineTo(x, y float64)               {}
func (painter) Stroke()                           {}
func (painter) FillText(string, float64, float64) {}
func (painter) MeasureTextWidth(t string) float64 { return 8 * float64(utf8.RuneCountInString(t)) }
func (painter) SetGlobalAlpha(float64)            {}
func (painter) Save()                             {}
func (painter) Restore()                          {}
func (painter) Translate(x, y float64)            {}
func (painter) ClipRect(x, y, w, h float64)       {}
func (painter) Rotate(float64)                    {}
func (painter) DrawImage(canvas.ImageSource, float64, float64, float64, float64, float64, float64, float64, float64) {
}

// user is one person's canvas with a Collab element, and a lock standing in for
// the UI goroutine.
type user struct {
	mu      sync.Mutex
	root    *cxango.Root
	state   collabxango.State
	redraws int
	props   collabxango.CollabProps
	items   []canvas.Item
	kids    func(u *user) []xango.Node // the wrapped elements; the default is one text flow
}

func newUser(t *testing.T, srv *httptest.Server, tr collab.Transport, header http.Header, items ...canvas.Item) *user {
	t.Helper()
	u := &user{items: items}
	u.root = cxango.NewRoot(canvas.NewStage(painter{}, 0, 0))
	u.root.Redraw = func() { u.redraws++ }
	u.props = collabxango.CollabProps{
		Transport: tr,
		Client:    client.Options{BaseURL: srv.URL, Doc: "d1", Header: header, Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond},
		Name:      "user", Interval: 5 * time.Millisecond,
		Sync:    func(f func()) { u.mu.Lock(); defer u.mu.Unlock(); f() },
		OnState: func(s collabxango.State) { u.state = s },
	}
	u.render(t)
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		_ = u.root.Render(cxango.Canvas(cxango.CanvasProps{Width: 600, Height: 400}))
	})
	return u
}

func (u *user) render(t *testing.T) {
	t.Helper()
	p := u.props
	p.Children = []xango.Node{cxango.TextFlow(cxango.TextFlowProps{Items: u.items, PageCount: 1, PageWidth: 300, PageHeight: 200, Margins: []float64{20}})}
	if u.kids != nil {
		p.Children = u.kids(u)
	}
	tree := cxango.Canvas(cxango.CanvasProps{Width: 600, Height: 400, Mode: canvas.ModeEdit, Children: []xango.Node{collabxango.Collab(p)}})
	if err := u.root.Render(tree); err != nil {
		t.Fatal(err)
	}
}

func (u *user) flow() *canvas.TextFlow { return u.root.Find(cxango.TagTextFlow, 0).(*canvas.TextFlow) }

func (u *user) text() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.flow().EditText()
}

func (u *user) insert(at int, s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.flow().Replace([]canvas.Edit{{From: at, To: at, Items: []canvas.Item{canvas.Chars{Text: s}}}})
}

func (u *user) get(f func(*user) bool) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return f(u)
}

func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// hubHasDocument reports whether the hub holds any content for document d1.
func hubHasDocument(srv *httptest.Server, role string) bool {
	req, _ := http.NewRequest("GET", srv.URL+"/docs/d1/snapshot", nil)
	req.Header.Set("X-Role", role)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.Contains(string(b), `"doc"`)
}

func eachTransport(t *testing.T, run func(t *testing.T, tr collab.Transport)) {
	t.Run("HTTP endpoints", func(t *testing.T) { run(t, collab.HTTPEndpoints()) })
	t.Run("WebSockets", func(t *testing.T) { run(t, ws.WebSockets()) })
}

func TestWrappingACanvasMakesItShared(t *testing.T) {
	eachTransport(t, func(t *testing.T, tr collab.Transport) {
		h := hub.New(hub.Config{SaveDelay: 10 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role { return hub.Role(r.Header.Get("X-Role")) }})
		srv := httptest.NewServer(h.Handler())
		t.Cleanup(srv.Close) // after the users' own cleanups, which disconnect them
		hdr := func(role string) http.Header { return http.Header{"X-Role": {role}} }

		ana := newUser(t, srv, tr, hdr("owner"), canvas.Chars{Text: "hello world"})
		wait(t, "Ana online", func() bool {
			return ana.get(func(u *user) bool { return u.state.Status == client.Online && u.state.Role == wire.Owner })
		})
		wait(t, "the hub to hold Ana's document", func() bool { return hubHasDocument(srv, "owner") })
		bo := newUser(t, srv, tr, hdr("editor"), canvas.Chars{Text: "local junk the document replaces"})
		wait(t, "Bo to get the document", func() bool { return bo.text() == "hello world" })

		ana.insert(5, ",")
		wait(t, "Bo to see the comma", func() bool { return bo.text() == "hello, world" })
		bo.insert(12, "!")
		wait(t, "Ana to see the bang", func() bool { return ana.text() == "hello, world!" })

		wait(t, "each to see the other", func() bool {
			return ana.get(func(u *user) bool { return len(u.state.Peers) == 1 }) && bo.get(func(u *user) bool { return len(u.state.Peers) == 1 })
		})

		// A viewer: the stage is held to the server's role, and the document arrives.
		vee := newUser(t, srv, tr, hdr("viewer"), canvas.Chars{Text: "x"})
		wait(t, "Vee to be a viewer with the text", func() bool {
			return vee.text() == "hello, world!" && vee.get(func(u *user) bool { return u.root.Stage.Role == canvas.RoleViewer })
		})
		if vee.get(func(u *user) bool { return u.redraws == 0 }) {
			t.Fatal("what arrived from the others must ask for a redraw")
		}
	})
}

func TestReRenderingDoesNotReconnectOrResetTheDocument(t *testing.T) {
	h := hub.New(hub.Config{})
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	ana := newUser(t, srv, nil, nil, canvas.Chars{Text: "seed"})
	wait(t, "online", func() bool { return ana.get(func(u *user) bool { return u.state.Status == client.Online }) })
	ana.insert(4, " grows")
	wait(t, "the edit to be accepted", func() bool { return ana.get(func(u *user) bool { return u.state.Pending == 0 }) })
	before := ana.get(func(u *user) bool { return u.state.Repairs == 0 })
	ana.mu.Lock()
	ana.render(t) // same props: nothing is re-applied, nothing reconnects
	ana.mu.Unlock()
	if got := ana.text(); got != "seed grows" {
		t.Fatalf("a re-render reset the shared text: %q", got)
	}
	if !before || !ana.get(func(u *user) bool { return u.state.Status == client.Online }) {
		t.Fatal("a re-render must keep the session")
	}
}

func TestUnknownFlowIsReported(t *testing.T) {
	srv := httptest.NewServer(hub.New(hub.Config{}).Handler())
	defer srv.Close()
	var got collabxango.State
	root := cxango.NewRoot(canvas.NewStage(painter{}, 0, 0))
	p := collabxango.CollabProps{Flow: "inserts", Client: client.Options{BaseURL: srv.URL, Doc: "d"}, OnState: func(s collabxango.State) { got = s },
		Children: []xango.Node{cxango.TextFlow(cxango.TextFlowProps{Text: "x", PageCount: 1, PageWidth: 300, PageHeight: 200})}}
	if err := root.Render(cxango.Canvas(cxango.CanvasProps{Width: 400, Height: 300, Children: []xango.Node{collabxango.Collab(p)}})); err != nil {
		t.Fatal(err)
	}
	if got.Err != collabxango.ErrFlowNotSupported {
		t.Errorf("unknown flow: %v", got.Err)
	}
}

// page is a canvas with furniture and objects: a text flow with a header and
// footer, a floating note and a table.
func page(text, header string) func(u *user) []xango.Node {
	return func(u *user) []xango.Node {
		tpl := func(pg xango.Node, _ int) xango.Node {
			return xango.Frag(cxango.Header(cxango.HeaderProps{Text: header}), pg, cxango.Footer(cxango.FooterProps{Text: "foot"}))
		}
		return []xango.Node{
			cxango.TextFlow(cxango.TextFlowProps{Text: text, PageCount: 1, PageWidth: 300, PageHeight: 200, Margins: []float64{20}, Pages: tpl}),
			cxango.FloatingText(cxango.FloatingTextProps{Text: "note", Width: 100, Height: 40}),
			cxango.Table(cxango.TableProps{Cells: [][]string{{"a", "b"}, {"c", "d"}}}),
		}
	}
}

func (u *user) furniture() (*canvas.PageSet, *canvas.FloatingText, *canvas.TableView) {
	return u.root.Find(cxango.TagPage, 0).(*canvas.PageSet), u.root.Find(cxango.TagFloatingText, 0).(*canvas.FloatingText), u.root.Find(cxango.TagTable, 0).(*canvas.TableView)
}

func TestFlows(t *testing.T) {
	cases := []struct {
		flow                     collabxango.Flow
		text, header, footer, ob bool // what reaches the other person
	}{
		{collabxango.FlowText, true, false, false, false},
		{collabxango.FlowHeader, false, true, false, false},
		{collabxango.FlowFooter, false, false, true, false},
		{collabxango.FlowObjects, false, false, false, true},
		{collabxango.FlowAll, true, true, true, true},
	}
	for _, c := range cases {
		t.Run(string(c.flow), func(t *testing.T) {
			srv := httptest.NewServer(hub.New(hub.Config{}).Handler())
			t.Cleanup(srv.Close)
			ana := newUser(t, srv, nil, nil)
			ana.props.Flow, ana.props.Name, ana.kids = c.flow, "Ana", page("hello", "Report")
			ana.render(t)
			wait(t, "Ana online", func() bool { return ana.get(func(u *user) bool { return u.state.Status == client.Online }) })
			time.Sleep(50 * time.Millisecond) // her content reaches the hub
			bo := newUser(t, srv, nil, nil)
			bo.props.Flow, bo.props.Name, bo.kids = c.flow, "Bo", page("hello", "Report")
			bo.render(t)
			wait(t, "Bo online", func() bool { return bo.get(func(u *user) bool { return u.state.Status == client.Online }) })

			ana.mu.Lock()
			ps, ft, tv := ana.furniture()
			ps.Pages[0].Header = &canvas.HeaderFooter{Text: "Ana's header"}
			ps.Pages[0].Footer = &canvas.HeaderFooter{Text: "Ana's footer"}
			ft.X, ft.Y = 111, 222
			ft.SetText("Ana's note")
			tv.Table.SetCell(1, 1, "Z")
			ana.flow().Replace([]canvas.Edit{{From: 0, To: 0, Items: []canvas.Item{canvas.Chars{Text: "ANA "}}}})
			ana.mu.Unlock()

			arrives := func(what string, want bool, cond func() bool) {
				t.Helper()
				if want {
					wait(t, what, func() bool { return bo.get(func(*user) bool { return cond() }) })
					return
				}
				time.Sleep(300 * time.Millisecond)
				if bo.get(func(*user) bool { return cond() }) {
					t.Errorf("%s reached Bo although flow %q does not share it", what, c.flow)
				}
			}
			bps, bft, btv := bo.furniture()
			arrives("the text", c.text, func() bool { return strings.HasPrefix(bo.flow().EditText(), "ANA ") })
			arrives("the header", c.header, func() bool { return bps.Pages[0].Header != nil && bps.Pages[0].Header.Text == "Ana's header" })
			arrives("the footer", c.footer, func() bool { return bps.Pages[0].Footer != nil && bps.Pages[0].Footer.Text == "Ana's footer" })
			arrives("the note", c.ob, func() bool { return bft.Text() == "Ana's note" && bft.X == 111 && bft.Y == 222 })
			arrives("the table cell", c.ob, func() bool { return btv.Table.Cell(1, 1) == "Z" })
		})
	}
}

func TestNoFlowInsideIsReported(t *testing.T) {
	var got collabxango.State
	root := cxango.NewRoot(canvas.NewStage(painter{}, 0, 0))
	p := collabxango.CollabProps{Client: client.Options{BaseURL: "http://127.0.0.1:1", Doc: "d"}, OnState: func(s collabxango.State) { got = s }}
	if err := root.Render(cxango.Canvas(cxango.CanvasProps{Width: 400, Height: 300, Children: []xango.Node{collabxango.Collab(p)}})); err != nil {
		t.Fatal(err)
	}
	if got.Err != collabxango.ErrNoFlow || !strings.Contains(got.Err.Error(), "TextFlow") {
		t.Fatalf("%v", got.Err)
	}
}

// A viewer cannot change the page furniture, so the session must not try to
// share what it holds locally (the hub would refuse it and the session would
// rebuild itself over and over).
func TestViewerDoesNotPushSurfaces(t *testing.T) {
	srv := httptest.NewServer(hub.New(hub.Config{Roles: func(r *http.Request, _ string) hub.Role {
		return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}}).Handler())
	t.Cleanup(srv.Close)
	mk := func(role string) *user {
		u := newUser(t, srv, nil, http.Header{"Authorization": {"Bearer " + role}})
		u.props.Flow, u.kids = collabxango.FlowAll, page("hello", "Report")
		u.render(t)
		wait(t, role+" online", func() bool {
			return u.get(func(u *user) bool { return u.state.Status == client.Online && u.state.Role != "" })
		})
		return u
	}
	ana := mk("owner")
	time.Sleep(50 * time.Millisecond)
	vee := mk("viewer")

	vee.mu.Lock()
	ps, ft, _ := vee.furniture()
	ps.Pages[0].Header = &canvas.HeaderFooter{Text: "Vee's header"}
	ft.X = 321
	vee.mu.Unlock()
	time.Sleep(400 * time.Millisecond)

	if vee.get(func(u *user) bool { return u.state.Repairs != 0 || u.state.Err != nil }) {
		t.Errorf("the viewer's session was rejected and repaired: %+v", vee.state)
	}
	ana.mu.Lock()
	aps, aft, _ := ana.furniture()
	h, x := aps.Pages[0].Header, aft.X
	ana.mu.Unlock()
	if h == nil || h.Text != "Report" || x == 321 {
		t.Errorf("a viewer's local change reached the owner: header %+v, x %v", h, x)
	}
}

// Objects with a Key are shared under it, so a peer whose tree has other
// objects (or the same ones in another order) still gets the right one.
func TestKeyedObjectsDoNotShift(t *testing.T) {
	srv := httptest.NewServer(hub.New(hub.Config{}).Handler())
	t.Cleanup(srv.Close)
	tree := func(keys ...string) func(u *user) []xango.Node {
		return func(u *user) []xango.Node {
			kids := []xango.Node{cxango.TextFlow(cxango.TextFlowProps{Text: "hello", PageCount: 1, PageWidth: 300, PageHeight: 200})}
			for _, k := range keys {
				kids = append(kids, cxango.FloatingText(cxango.FloatingTextProps{Key: k, Text: "note " + k, Width: 100, Height: 40}))
			}
			return kids
		}
	}
	ana := newUser(t, srv, nil, nil)
	ana.props.Flow, ana.kids = collabxango.FlowObjects, tree("a", "b")
	ana.render(t)
	wait(t, "Ana online", func() bool { return ana.get(func(u *user) bool { return u.state.Status == client.Online }) })
	time.Sleep(50 * time.Millisecond)
	bo := newUser(t, srv, nil, nil)
	bo.props.Flow, bo.kids = collabxango.FlowObjects, tree("b") // no "a": Bo's first note is Ana's second
	bo.render(t)
	wait(t, "Bo online", func() bool { return bo.get(func(u *user) bool { return u.state.Status == client.Online }) })

	ana.mu.Lock()
	notes := func(u *user) (a, b *canvas.FloatingText) {
		u.root.Find(cxango.TagFloatingText, 0)
		a, _ = u.root.Find(cxango.TagFloatingText, 0).(*canvas.FloatingText)
		b, _ = u.root.Find(cxango.TagFloatingText, 1).(*canvas.FloatingText)
		return a, b
	}
	a, b := notes(ana)
	a.SetText("A moved on")
	b.SetText("B moved on")
	ana.mu.Unlock()

	wait(t, "Bo's note b to change", func() bool {
		return bo.get(func(u *user) bool { n, _ := notes(u); return n.Text() == "B moved on" })
	})
	if got := bo.get(func(u *user) bool { n, _ := notes(u); return n.Text() != "A moved on" }); !got {
		t.Error("Ana's note a landed on Bo's first note")
	}
}
