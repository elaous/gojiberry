//go:build js && wasm && (experimental || exp_map)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/osmmap"
	"github.com/elaous/gojiberry/xango"
)

// The map part of the Pictures tab (build tag exp_map, or experimental): a place named by an address, coordinates or a
// Plus Code becomes a picture of the OpenStreetMap tiles around it, with a marker, its alt text and the credit as its
// legend. Coordinates and Plus Codes are read here and ask nobody; an address is looked up with Nominatim, and the map
// is made of tiles from the tile server: both are the public OpenStreetMap ones unless the operator names others in
// map.json next to the page ({"nominatim": "https://...", "tiles": "https://.../{z}/{x}/{y}.png"}), or ?osm=<address>
// points both at a mirror (or, in tests, a stand-in): <address>/search and <address>/tiles/{z}/{x}/{y}.png.

// mapForm is what the form shows.
type mapForm struct {
	query string
	zoom  int // 0 means the default
	msg   string
	busy  bool
}

type mapHolder struct{ mapUI mapForm }

var mapSettings struct {
	loaded           bool
	nominatim, tiles string
}

func mapConfig() (string, string) {
	if mapSettings.loaded {
		return mapSettings.nominatim, mapSettings.tiles
	}
	mapSettings.loaded = true
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	if b := q.Call("get", "osm"); b.Truthy() {
		base := strings.TrimSuffix(b.String(), "/")
		mapSettings.nominatim, mapSettings.tiles = base, base+"/tiles/{z}/{x}/{y}.png"
		return mapSettings.nominatim, mapSettings.tiles
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, "map.json", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			if data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)); err == nil && resp.StatusCode == http.StatusOK {
				var cfg struct{ Nominatim, Tiles string }
				if json.Unmarshal(data, &cfg) == nil && mapAddressOK(cfg.Nominatim, false) && mapAddressOK(cfg.Tiles, true) {
					mapSettings.nominatim, mapSettings.tiles = cfg.Nominatim, cfg.Tiles
				}
			}
		}
	}
	return mapSettings.nominatim, mapSettings.tiles
}

// mapSources are the page's geocoder and tile source, kept for as long as the page is open so that the same address or
// tile is not asked for twice, and, for the public OpenStreetMap servers, with a gap between requests (their policy is
// light use: Nominatim one a second). A server the operator names in map.json gets the cache and no gap.
var mapSources struct {
	nom, tileURL string
	geo          *osmmap.PoliteGeocoder
	tiles        *osmmap.PoliteTiles
}

func mapClients(nom, tileURL string) (*osmmap.PoliteGeocoder, *osmmap.PoliteTiles) {
	s := &mapSources
	if s.geo == nil || s.nom != nom || s.tileURL != tileURL {
		var geoGap, tileGap time.Duration
		if nom == "" {
			geoGap = time.Second
		}
		if tileURL == "" {
			tileGap = 150 * time.Millisecond
		}
		s.nom, s.tileURL = nom, tileURL
		s.geo = &osmmap.PoliteGeocoder{Source: osmmap.Nominatim{Base: nom, UserAgent: "gojiberry-showcase"}, Gap: geoGap}
		s.tiles = &osmmap.PoliteTiles{Source: osmmap.Tiles{URL: tileURL, UserAgent: "gojiberry-showcase"}, Gap: tileGap}
	}
	return s.geo, s.tiles
}

// mapAddressOK: empty (the public server), https, or an address relative to the page; a tile address needs {z}, {x} and {y}.
func mapAddressOK(a string, tiles bool) bool {
	if a == "" {
		return true
	}
	if strings.ContainsAny(a, " \t\r\n\"'<>") || (strings.Contains(a, "://") && !strings.HasPrefix(a, "https://")) || strings.HasPrefix(a, "//") {
		return false
	}
	return !tiles || (strings.Contains(a, "{z}") && strings.Contains(a, "{x}") && strings.Contains(a, "{y}"))
}

func mapZoomLabel(z int) string {
	switch z {
	case 11:
		return T("map.zoomCity", "City")
	case 14:
		return T("map.zoomTown", "Neighbourhood")
	}
	return T("map.zoomStreet", "Street")
}

var mapZooms = []int{17, 14, 11}

// mapSection is the form in the Pictures tab.
func mapSection(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) []xango.Node {
	f := &u.mapUI
	if f.zoom == 0 {
		f.zoom = 17
	}
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("map.title", "Insert a map"))),
		para("map-about", T("map.about", "From an address, coordinates (48.8584, 2.2945) or a Plus Code (8FVC9G8F+6W). The map is made of OpenStreetMap tiles; an address is looked up on OpenStreetMap's Nominatim."), false),
		lineInput("map-query", T("map.query", "Address, coordinates or Plus Code"), f.query, T("map.hint", "for example: Eiffel Tower, Paris"), func(v string) { f.query = v }),
	}
	var zooms []xango.Node
	for _, z := range mapZooms {
		z := z
		zooms = append(zooms, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("id", fmt.Sprintf("map-zoom-%d", z)), xango.A("class", "gj-keybindings-button"),
			xango.A("aria-pressed", fmt.Sprint(f.zoom == z)), xango.On("click", func(xango.Event) { f.zoom = z; renderUI() })}, xango.Text(mapZoomLabel(z))))
	}
	nodes = append(nodes,
		xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", T("map.zoom", "How close")), xango.A("style", "display:flex;gap:4px;margin:6px 0")}, zooms...),
		btn("map-go", T("map.go", "Insert the map"), func() { u.insertMap(flow, redraw, renderUI) }, !canEdit || f.busy || strings.TrimSpace(f.query) == ""))
	if f.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "map-message"), xango.A("role", "status")}, xango.Text(f.msg)))
	}
	return nodes
}

// insertMap finds the place, draws the map and puts it at the caret.
func (u *ui) insertMap(flow func() *canvas.TextFlow, redraw, renderUI func()) {
	f := &u.mapUI
	fl := flow()
	if fl == nil {
		return
	}
	query, zoom := strings.TrimSpace(f.query), f.zoom
	f.busy, f.msg = true, T("map.working", "Making the map…")
	renderUI()
	go func() {
		nom, tiles := mapConfig()
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		place, ok, err := osmmap.Locate(query)
		if err != nil {
			f.busy, f.msg = false, T("map.badCoords", "Latitude must be between -90 and 90 and longitude between -180 and 180.")
			renderUI()
			return
		}
		geo, tileSource := mapClients(nom, tiles)
		if !ok { // a short Plus Code with the name of a place near it ("9G8F+6W Mountain View")
			var short bool
			if place, short, err = osmmap.LocateShort(ctx, geo, query); short {
				if err != nil {
					f.busy, f.msg = false, T("map.notFound", "No such place was found. Try another address, coordinates or a Plus Code.")
					renderUI()
					return
				}
				ok = true
			}
		}
		if !ok {
			places, err := geo.Search(ctx, query)
			if err != nil || len(places) == 0 {
				f.busy = false
				f.msg = T("map.notFound", "No such place was found. Try another address, coordinates or a Plus Code.")
				if err != nil {
					f.msg = T("map.lookupFailed", "The address could not be looked up (the service did not answer).")
				}
				renderUI()
				return
			}
			place = places[0]
		}
		data, err := osmmap.Render(ctx, tileSource, place.Lat, place.Lng, zoom, 640, 400)
		if err != nil {
			f.busy, f.msg = false, T("map.renderFailed", "The map could not be drawn (the tile server did not answer).")
			renderUI()
			return
		}
		f.busy, f.msg = false, ""
		name := place.Name
		if place.Kind != "address" {
			name = fmt.Sprintf("%.5f, %.5f", place.Lat, place.Lng)
			if place.Kind == "pluscode" {
				name = place.Name + " (" + name + ")"
			}
		}
		u.insertBytes(fl, "image/png", data, redraw, renderUI, func(im *canvas.Image) {
			im.Alt = T("map.alt", "Map of %s", name)
			im.Legend, im.Credit, im.SourceURL = osmmap.Credit, osmmap.Credit, "https://www.openstreetmap.org/copyright"
			im.W, im.H = 320, 200 // the map is made at twice this size, so it stays sharp
		})
	}()
}
