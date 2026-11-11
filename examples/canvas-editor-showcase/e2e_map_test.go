//go:build !(js && wasm) && (experimental || exp_map)

package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/osmmap"
)

func TestInsertAMapFromAPlusCodeAndFromAnAddress(t *testing.T) {
	var searches, tiles atomic.Int32
	site := e2etest.NewSite(t)
	site.Handle("/osm/search", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		_, _ = w.Write([]byte(`[{"lat":"48.8584","lon":"2.2945","display_name":"Eiffel Tower, Paris"}]`))
	}))
	site.Handle("/osm/tiles/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tiles.Add(1)
		_ = png.Encode(w, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	}))
	d := site.Open(t, "?lang=en&osm=/osm")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#map-query")
	d.Fill("#map-query", "7FG49Q00+")
	d.Click("#map-zoom-14")
	d.Click("#map-go")
	d.WaitFor("a map is in the picture list", `document.body.textContent.includes('Map of 7FG49Q00+')`)
	if searches.Load() != 0 || tiles.Load() == 0 {
		t.Fatalf("a Plus Code needs no search: searches %d, tiles %d", searches.Load(), tiles.Load())
	}
	d.Fill("#map-query", "Eiffel Tower, Paris")
	d.Click("#map-go")
	d.WaitFor("the address map", `document.body.textContent.includes('Map of Eiffel Tower, Paris')`)
	if searches.Load() != 1 {
		t.Fatalf("one search for the address: %d", searches.Load())
	}
	full := osmmap.EncodePlusCode(48.8584, 2.2945, 10)
	tilesBefore := tiles.Load()
	d.Fill("#map-query", full[4:]+" Eiffel Tower, Paris")
	d.Click("#map-go")
	d.WaitFor("the short code map", `document.body.textContent.includes('Map of `+full+`')`)
	if searches.Load() != 1 {
		t.Fatalf("the place named with the short code was answered from the page's memory: %d searches", searches.Load())
	}
	if tiles.Load() != tilesBefore {
		t.Errorf("the same tiles were asked for again: %d, then %d", tilesBefore, tiles.Load())
	}
	d.Click(`button[data-tab="transfer"]`)
	_, js := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="json"]`) })
	flat := string(js)
	d.OK(strings.Count(flat, "Map data © OpenStreetMap contributors (ODbL)") >= 2 && strings.Contains(flat, "openstreetmap.org/copyright"), "the credit and its page are kept with the maps")
	d.OK(bytes.Contains(js, []byte("data:image/png;base64,")), "the map is a picture in the document")
	d.Click(`button[data-tab="images"]`)
	d.Fill("#map-query", "95, 10") // looks like coordinates, but there is no such latitude
	d.Click("#map-go")
	d.WaitFor("it is refused", `document.getElementById('map-message') && document.getElementById('map-message').textContent.includes('Latitude')`)
	d.NoErrors()
}
