package gox_test

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
	cgox "github.com/elaous/gojiberry/canvas/gox"
	"github.com/elaous/gojiberry/collab"
	"github.com/elaous/gojiberry/collab/client"
	collabgox "github.com/elaous/gojiberry/collab/gox"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/collab/ws"
	"github.com/elaous/gojiberry/gox"
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
	root    *cgox.Root
	state   collabgox.State
	redraws int
	props   collabgox.CollabProps
	items   []canvas.Item
	kids    func(u *user) []gox.Node // the wrapped elements; the default is one text flow
}

func newUser(t *testing.T, srv *httptest.Server, tr collab.Transport, header http.Header, items ...canvas.Item) *user {
	t.Helper()
	u := &user{items: items}
	u.root = cgox.NewRoot(canvas.NewStage(painter{}, 0, 0))
	u.root.Redraw = func() { u.redraws++ }
	u.props = collabgox.CollabProps{
		Transport: tr,
		Client:    client.Options{BaseURL: srv.URL, Doc: "d1", Header: header, Retry: 10 * time.Millisecond, MaxRetry: 50 * time.Millisecond},
		Name:      "user", Interval: 5 * time.Millisecond,
		Sync:    func(f func()) { u.mu.Lock(); defer u.mu.Unlock(); f() },
		OnState: func(s collabgox.State) { u.state = s },
	}
	u.render(t)
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		_ = u.root.Render(cgox.Canvas(cgox.CanvasProps{Width: 600, Height: 400}))
	})
	return u
}

func (u *user) render(t *testing.T) {
	t.Helper()
	p := u.props
	p.Children = []gox.Node{cgox.TextFlow(cgox.TextFlowProps{Items: u.items, PageCount: 1, PageWidth: 300, PageHeight: 200, Margins: []float64{20}})}
	if u.kids != nil {
		p.Children = u.kids(u)
	}
	tree := cgox.Canvas(cgox.CanvasProps{Width: 600, Height: 400, Mode: canvas.ModeEdit, Children: []gox.Node{collabgox.Collab(p)}})
	if err := u.root.Render(tree); err != nil {
		t.Fatal(err)
	}
}

func (u *user) flow() *canvas.TextFlow { return u.root.Find(cgox.TagTextFlow, 0).(*canvas.TextFlow) }

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
	var got collabgox.State
	root := cgox.NewRoot(canvas.NewStage(painter{}, 0, 0))
	p := collabgox.CollabProps{Flow: "inserts", Client: client.Options{BaseURL: srv.URL, Doc: "d"}, OnState: func(s collabgox.State) { got = s },
		Children: []gox.Node{cgox.TextFlow(cgox.TextFlowProps{Text: "x", PageCount: 1, PageWidth: 300, PageHeight: 200})}}
	if err := root.Render(cgox.Canvas(cgox.CanvasProps{Width: 400, Height: 300, Children: []gox.Node{collabgox.Collab(p)}})); err != nil {
		t.Fatal(err)
	}
	if got.Err != collabgox.ErrFlowNotSupported {
		t.Errorf("unknown flow: %v", got.Err)
	}
}

// page is a canvas with furniture and objects: a text flow with a header and
// footer, a floating note and a table.
func page(text, header string) func(u *user) []gox.Node {
	return func(u *user) []gox.Node {
		tpl := func(pg gox.Node, _ int) gox.Node {
			return gox.Frag(cgox.Header(cgox.HeaderProps{Text: header}), pg, cgox.Footer(cgox.FooterProps{Text: "foot"}))
		}
		return []gox.Node{
			cgox.TextFlow(cgox.TextFlowProps{Text: text, PageCount: 1, PageWidth: 300, PageHeight: 200, Margins: []float64{20}, Pages: tpl}),
			cgox.FloatingText(cgox.FloatingTextProps{Text: "note", Width: 100, Height: 40}),
			cgox.Table(cgox.TableProps{Cells: [][]string{{"a", "b"}, {"c", "d"}}}),
		}
	}
}

func (u *user) furniture() (*canvas.PageSet, *canvas.FloatingText, *canvas.TableView) {
	return u.root.Find(cgox.TagPage, 0).(*canvas.PageSet), u.root.Find(cgox.TagFloatingText, 0).(*canvas.FloatingText), u.root.Find(cgox.TagTable, 0).(*canvas.TableView)
}

func TestFlows(t *testing.T) {
	cases := []struct {
		flow                     collabgox.Flow
		text, header, footer, ob bool // what reaches the other person
	}{
		{collabgox.FlowText, true, false, false, false},
		{collabgox.FlowHeader, false, true, false, false},
		{collabgox.FlowFooter, false, false, true, false},
		{collabgox.FlowObjects, false, false, false, true},
		{collabgox.FlowAll, true, true, true, true},
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
	var got collabgox.State
	root := cgox.NewRoot(canvas.NewStage(painter{}, 0, 0))
	p := collabgox.CollabProps{Client: client.Options{BaseURL: "http://127.0.0.1:1", Doc: "d"}, OnState: func(s collabgox.State) { got = s }}
	if err := root.Render(cgox.Canvas(cgox.CanvasProps{Width: 400, Height: 300, Children: []gox.Node{collabgox.Collab(p)}})); err != nil {
		t.Fatal(err)
	}
	if got.Err != collabgox.ErrNoFlow || !strings.Contains(got.Err.Error(), "TextFlow") {
		t.Fatalf("%v", got.Err)
	}
}
