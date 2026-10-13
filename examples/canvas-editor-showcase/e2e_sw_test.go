//go:build !(js && wasm)

package main

import (
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/wasmserve"
)

// With the service worker that cmd/wasmdist writes, a repeat visit starts from the browser's copy of the
// program: it asks the server for the page but not for the 30 MB program.
func TestServiceWorkerKeepsTheAppForRepeatVisits(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	site := e2etest.NewSite(t)
	var wasmHits atomic.Int32
	files := http.FileServer(http.Dir(site.Dir()))
	site.Handle("/sw.js", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wasmserve.ServeServiceWorker(w, r, os.DirFS(site.Dir()), nil)
	}))
	var filesHits atomic.Int32
	site.Handle("/files.wasm", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filesHits.Add(1)
		http.FileServer(http.Dir(site.Dir())).ServeHTTP(w, r)
	}))
	site.Handle("/main.wasm", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wasmHits.Add(1)
		files.ServeHTTP(w, r)
	}))

	d := site.Open(t, "?lang=en")
	const ready = `(async () => {
		const reg = await navigator.serviceWorker.getRegistration();
		const keys = (await caches.keys()).filter(k => k.startsWith('gojiberry-app-'));
		if (!reg || !reg.active || keys.length !== 1) return false;
		return (await (await caches.open(keys[0])).keys()).length === 3;
	})()`
	for i := 0; i < 100 && !d.Bool(ready); i++ {
		d.Sleep(300)
	}
	d.OK(d.Bool(ready), "the worker is installed and has the app in its cache")
	d.OK(wasmHits.Load() >= 1, "the first visit fetched the program")

	d.Reload() // the worker now controls the page; the load after this one is served from its cache
	d.OK(d.Bool(`!!navigator.serviceWorker.controller`), "the page is controlled by the worker")
	before := wasmHits.Load()
	d.Reload()
	d.OK(wasmHits.Load() == before, "a repeat visit does not ask the server for the program")
	d.OK(d.Str(`document.getElementById('state').textContent`) != "", "and the app runs")

	// The worker keeps one cache, named for the version, and nothing of the API.
	d.OK(d.Int(`(async () => (await caches.keys()).filter(k => k.startsWith('gojiberry-app-')).length)()`) == 1, "one cache")
	d.OK(d.Int(`(async () => (await (await caches.open((await caches.keys())[0])).keys()).length)()`) == 3, "the cache holds the three files of the shell")

	// The second program (the file formats) is not fetched ahead: the first export fetches it, the worker
	// keeps it, and a later visit finds it in the cache.
	d.OK(filesHits.Load() == 0, "nothing of the file formats is downloaded before they are needed")
	export := func() {
		d.Click(`button[data-tab="transfer"]`)
		d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="docx"]`) })
	}
	export()
	d.OK(filesHits.Load() >= 1, "the first export fetched the file worker")
	for i := 0; i < 50 && d.Int(`(async () => (await (await caches.open((await caches.keys())[0])).keys()).length)()`) < 5; i++ {
		d.Sleep(200)
	}
	d.OK(d.Int(`(async () => (await (await caches.open((await caches.keys())[0])).keys()).length)()`) == 5, "and the worker kept it (five files now)")
	hits := filesHits.Load()
	d.Reload()
	export()
	d.OK(filesHits.Load() == hits, "a repeat visit does not fetch the file worker again")
	d.NoErrors()
}
