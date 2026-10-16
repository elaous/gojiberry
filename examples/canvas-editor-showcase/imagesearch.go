//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/imagerepo"
	"github.com/elaous/gojiberry/xango"
)

// imageFind is the state of the "find a picture online" part of the Pictures tab.
type imageFind struct {
	query   string
	results []imagerepo.Result
	msg     string
	busy    bool
	page    int // the page of results last read
	more    bool
}

var imageLib *imagerepo.Library

// images is the register of image repositories. Nothing is on until the person turns a repository on, and nothing
// is asked of it until they search. ?imagerepo=<address> points both at a mirror (or, in tests, a stand-in).
func images() *imagerepo.Library {
	if imageLib != nil {
		return imageLib
	}
	imageLib = imagerepo.New()
	o, c := imagerepo.Openverse{}, imagerepo.Commons{}
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	if b := q.Call("get", "imagerepo"); b.Truthy() {
		o.Base, c.Base = b.String(), b.String()
	}
	imageLib.Register(o)
	imageLib.Register(c)
	go loadOperatorRepos(imageLib, q)
	return imageLib
}

// loadOperatorRepos adds the operator's own list of repositories (a JSON array, see imagerepo.ParseConfig) from
// image-repos.json next to the page, or from the address in ?imagerepos=. No list is fine; a bad one is ignored and said so.
func loadOperatorRepos(lib *imagerepo.Library, q js.Value) {
	addr := "image-repos.json"
	explicit := false
	if a := q.Call("get", "imagerepos"); a.Truthy() {
		addr, explicit = a.String(), true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	rs, err := imagerepo.ParseConfig(data)
	if err != nil {
		if explicit {
			imageListError = err.Error()
		}
		return
	}
	lib.RegisterAll(rs)
	if imageRender != nil {
		imageRender()
	}
}

var (
	imageListError string
	imageRender    func() // set by the panel, so a list that arrives late is drawn
)

// imageSearchSection is the "Find a picture" part of the Pictures tab: pick the repositories to use, search, read the
// author and licence of each result, and insert one with its credit as the legend.
func imageSearchSection(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) []xango.Node {
	lib := images()
	imageRender = renderUI
	f := &u.imgFind
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("imgfind.title", "Find a picture online"))),
		para("imgfind-about", T("imgfind.about", "Public-domain and Creative Commons pictures. Nothing is asked of a repository until you turn it on and search."), false),
	}
	for i, name := range lib.Names() {
		name := name
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("imgfind-repo-%d", i))}, components.Checkbox(components.CheckboxProps{
			Label: name, Checked: lib.Enabled(name),
			OnChange: func(on bool) { lib.Enable(name, on); renderUI() }})))
	}
	enabled := firstEnabled(lib)
	nodes = append(nodes,
		lineInput("imgfind-query", T("imgfind.query", "Search for a picture"), f.query, T("imgfind.hint", "for example: harbour at dawn"), func(v string) { f.query = v }),
		btn("imgfind-go", T("imgfind.go", "Search"), func() { u.searchImages(lib, 1, renderUI) }, enabled == "" || f.busy))
	if enabled == "" {
		nodes = append(nodes, para("imgfind-none", T("imgfind.none", "Turn a repository on to search."), false))
	}
	if f.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "imgfind-message"), xango.A("role", "status")}, xango.Text(f.msg)))
	}
	var items []xango.Node
	for i, r := range f.results {
		i, r := i, r
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("imgfind-result-%d", i))},
			xango.El("img", []xango.Attr{xango.A("src", r.Thumb), xango.A("alt", r.Title), xango.A("style", "max-width:120px;max-height:90px;display:block"),
				xango.A("referrerpolicy", "no-referrer")}),
			xango.El("span", []xango.Attr{xango.A("id", fmt.Sprintf("imgfind-credit-%d", i))}, xango.Text(r.Credit())),
			btn(fmt.Sprintf("imgfind-insert-%d", i), T("imgfind.insert", "Insert with its credit"), func() { u.insertFound(flow, r, redraw, renderUI) }, !canEdit)))
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...))
	}
	if f.more && !f.busy {
		nodes = append(nodes, btn("imgfind-more", T("imgfind.more", "More results"), func() { u.searchImages(lib, f.page+1, renderUI) }, false))
	}
	if imageListError != "" {
		nodes = append(nodes, para("imgfind-listerror", T("imgfind.listError", "The operator's list of repositories could not be read: ")+imageListError, true))
	}
	return nodes
}

func firstEnabled(lib *imagerepo.Library) string {
	for _, n := range lib.Names() {
		if lib.Enabled(n) {
			return n
		}
	}
	return ""
}

// searchImages asks every repository that is on and lists what they found; page 1 starts over, later pages add to the list.
func (u *ui) searchImages(lib *imagerepo.Library, page int, renderUI func()) {
	f := &u.imgFind
	f.busy, f.msg = true, T("imgfind.searching", "Searching…")
	if page == 1 {
		f.results = nil
	}
	renderUI()
	query := f.query
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var found []imagerepo.Result
		var failed []string
		for _, n := range lib.Names() {
			if !lib.Enabled(n) {
				continue
			}
			res, err := lib.Search(ctx, n, query, page)
			if err != nil {
				failed = append(failed, n)
				continue
			}
			found = append(found, res...)
		}
		f.busy, f.page, f.more = false, page, len(found) > 0
		f.results = append(f.results, found...)
		switch {
		case len(failed) > 0 && len(f.results) == 0:
			f.msg = T("imgfind.failed", "The search did not work: ") + strings.Join(failed, ", ")
		case len(f.results) == 0:
			f.msg = T("imgfind.nothing", "Nothing found.")
		default:
			f.msg = T("imgfind.found", "%d pictures found. Read the licence before you insert one.", len(f.results))
		}
		renderUI()
	}()
}

// insertFound fetches the chosen picture and puts it at the caret with its credit as the legend; the author,
// licence and page are also kept with the picture in the saved file.
func (u *ui) insertFound(flow func() *canvas.TextFlow, r imagerepo.Result, redraw, renderUI func()) {
	f := &u.imgFind
	fl := flow()
	if fl == nil {
		return
	}
	f.msg = T("imgfind.fetching", "Fetching the picture…")
	renderUI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		data, mime, err := images().Fetch(ctx, r)
		if err != nil {
			f.msg = T("imgfind.fetchFailed", "The picture could not be fetched (too large, not a picture, or the repository did not answer).")
			renderUI()
			return
		}
		f.msg = ""
		u.insertBytes(fl, mime, data, redraw, renderUI, func(im *canvas.Image) {
			im.Legend, im.Credit, im.SourceURL = r.Credit(), r.Credit(), r.Source
		})
	}()
}
