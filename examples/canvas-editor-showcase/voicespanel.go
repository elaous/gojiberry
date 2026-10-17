//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/xango"
)

// The Reader voices tab: which engine reads aloud (the voices the browser offers, or voices downloaded into the page)
// and, for the downloaded ones, the catalogue for the document's language with each voice's size and licence, and the
// voices already on the device. Nothing is fetched from the catalogue until the person asks for it.

// megabytes writes a size in bytes in megabytes.
func megabytes(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// voicesPanel is the tab.
func voicesPanel(u *ui, renderUI func()) xango.Node {
	ensurePiper(renderUI)
	e := piperUI.engine
	lang := u.docLang
	if lang == "" {
		lang = "en"
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("voices.title", "Reader voices"))),
		para("voices-about", T("voices.about", "Reading aloud can use the voices the browser offers, or voices you download into this page. A downloaded voice runs here, on your device: the text is never sent anywhere."), false),
	}
	pickEngine := func(value string) {
		p := curPrefs
		p.Reader = value
		setPrefs(p)
		stopReading()
		renderUI()
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", T("voices.engine", "Voices to read with")), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin-bottom:0.6em")},
		components.Button(components.ButtonProps{Label: T("read.readerDevice", "Voices on this device"), Variant: variantOf(curPrefs.Reader != "piper"), OnClick: func(xango.Event) { pickEngine("") }}),
		components.Button(components.ButtonProps{Label: T("read.readerPiper", "Downloaded voices (Piper)"), Variant: variantOf(curPrefs.Reader == "piper"), OnClick: func(xango.Event) { pickEngine("piper") }}),
	))

	switch {
	case e == nil || !piperUI.checked:
		nodes = append(nodes, para("voices-wait", T("voices.looking", "Looking at the voices on this device…"), false))
	case !e.Available():
		nodes = append(nodes, para("voices-noruntime", T("voices.noRuntime", "This page has no runtime for downloaded voices, so none can be used here. The person who runs the page adds one (see docs/reader-voices.md)."), true))
	}
	if piperUI.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "voices-message"), xango.A("role", "status")}, xango.Text(piperUI.msg)))
	}
	if e != nil && piperUI.checked && e.Available() {
		nodes = append(nodes, downloadedVoices(u, e, lang, renderUI)...)
	}
	return xango.El("div", []xango.Attr{xango.A("id", "voices-panel")}, nodes...)
}

func variantOf(on bool) components.ButtonVariant {
	if on {
		return components.ButtonPrimary
	}
	return components.ButtonSecondary
}

func downloadedVoices(u *ui, e interface {
	read.Engine
	read.Downloader
}, lang string, renderUI func()) []xango.Node {
	var nodes []xango.Node
	// the voices on the device
	nodes = append(nodes, xango.El("h5", nil, xango.Text(T("voices.have", "Downloaded voices on this device"))))
	have := e.Voices()
	if len(have) == 0 {
		nodes = append(nodes, para("voices-none", T("voices.noneYet", "No voice is downloaded yet."), false))
	}
	var haveItems []xango.Node
	for i, v := range have {
		v := v
		haveItems = append(haveItems, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("voices-have-%d", i))},
			xango.El("strong", nil, xango.Text(read.Label(v))),
			xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px;color:#6b7280")}, xango.Text(v.Lang)),
			btn(fmt.Sprintf("voices-use-%d", i), T("voices.use", "Use it"), func() {
				p := curPrefs
				p.PVoice, p.Reader = v.Key(), "piper"
				setPrefs(p)
				renderUI()
			}, curPrefs.PVoice == v.Key() && curPrefs.Reader == "piper"),
			btn(fmt.Sprintf("voices-remove-%d", i), T("voices.remove", "Remove it"), func() {
				if err := e.Remove(v.Key()); err != nil {
					piperUI.msg = err.Error()
				} else {
					if curPrefs.PVoice == v.Key() {
						p := curPrefs
						p.PVoice = ""
						setPrefs(p)
					}
					piperUI.msg = fmt.Sprintf(T("voices.removed", "%s was removed."), v.Name)
				}
				renderUI()
			}, false)))
	}
	if len(haveItems) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("id", "voices-have")}, haveItems...))
	}

	// the catalogue
	host := ""
	if cu, err := url.Parse(piperUI.cfg.Catalogue); err == nil {
		host = cu.Host
		if host == "" {
			host = T("voices.thisServer", "this server")
		}
	}
	nodes = append(nodes, xango.El("h5", nil, xango.Text(fmt.Sprintf(T("voices.forLang", "Voices to download for %s"), lang))))
	list := e.Catalogue(lang)
	if len(list) == 0 {
		nodes = append(nodes,
			para("voices-contact", fmt.Sprintf(T("voices.contact", "Showing the list asks %s for its catalogue of voices. Nothing else is sent."), host), false),
			btn("voices-load", T("voices.load", "Show the voices to download"), func() {
				piperUI.catLoading, piperUI.catErr = true, ""
				renderUI()
				e.LoadCatalogue(context.Background(), func(err error) {
					piperUI.catLoading = false
					if err != nil {
						piperUI.catErr = err.Error()
					}
					renderUI()
				})
			}, piperUI.catLoading))
		if piperUI.catErr != "" {
			nodes = append(nodes, para("voices-caterr", fmt.Sprintf(T("voices.catErr", "The list could not be fetched: %s"), piperUI.catErr), true))
		}
		return nodes
	}
	var items []xango.Node
	for i, c := range list {
		i, c := i, c
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(read.Label(c.Voice))),
			xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px;color:#6b7280")}, xango.Text(fmt.Sprintf("%s, %s, %s", c.Lang, c.Quality, megabytes(c.Size)))),
		}
		if c.Licence != "" {
			kids = append(kids, xango.El("a", []xango.Attr{xango.A("href", c.Licence), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer"), xango.A("id", fmt.Sprintf("voices-licence-%d", i)),
				xango.A("style", "margin-left:6px")}, xango.Text(T("voices.licence", "Whose voice and what terms"))))
		}
		switch {
		case c.Installed:
			kids = append(kids, para(fmt.Sprintf("voices-has-%d", i), T("voices.installed", "Downloaded."), false))
		case piperUI.busy == c.Key():
			pct := 0
			if piperUI.total > 0 {
				pct = int(piperUI.done * 100 / piperUI.total)
			}
			kids = append(kids,
				xango.El("span", []xango.Attr{xango.A("id", "voices-progress"), xango.A("role", "status")}, xango.Text(fmt.Sprintf(T("voices.progress", "Downloading… %d%%"), pct))),
				btn("voices-cancel", T("voices.cancel", "Cancel"), func() {
					if piperUI.cancel != nil {
						piperUI.cancel()
					}
				}, false))
		default:
			kids = append(kids, btn(fmt.Sprintf("voices-download-%d", i), fmt.Sprintf(T("voices.download", "Download (%s)"), megabytes(c.Size)), func() {
				ctx, cancel := context.WithCancel(context.Background())
				piperUI.busy, piperUI.done, piperUI.total, piperUI.cancel, piperUI.msg = c.Key(), 0, c.Size, cancel, ""
				renderUI()
				e.Install(ctx, c.Key(), func(done, total int64) {
					piperUI.done, piperUI.total = done, total
					renderUI()
				}, func(err error) {
					cancel()
					piperUI.busy, piperUI.cancel = "", nil
					if err != nil {
						piperUI.msg = fmt.Sprintf(T("voices.failed", "The voice could not be downloaded: %s"), err.Error())
					} else {
						piperUI.msg = fmt.Sprintf(T("voices.done", "%s is downloaded and can be used."), c.Name)
						u.announce(piperUI.msg)
					}
					renderUI()
				})
			}, piperUI.busy != ""))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("voices-item-%d", i))}, kids...))
	}
	return append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("id", "voices-list")}, items...))
}
