//go:build js && wasm

package main

import (
	"fmt"

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
		if value == "hosted" {
			ensureHosted(renderUI)
		}
		renderUI()
	}
	engines := []xango.Node{
		components.Button(components.ButtonProps{Label: T("read.readerDevice", "Voices on this device"), Variant: variantOf(curPrefs.Reader == ""), OnClick: func(xango.Event) { pickEngine("") }}),
		components.Button(components.ButtonProps{Label: T("read.readerPiper", "Downloaded voices (Piper)"), Variant: variantOf(curPrefs.Reader == "piper"), OnClick: func(xango.Event) { pickEngine("piper") }}),
	}
	if piperUI.cfg.Server != "" {
		engines = append(engines, components.Button(components.ButtonProps{Label: T("read.readerHosted", "Voices on a server"), Variant: variantOf(curPrefs.Reader == "hosted"), OnClick: func(xango.Event) { pickEngine("hosted") }}))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", T("voices.engine", "Voices to read with")), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin-bottom:0.6em")}, engines...))
	if piperUI.cfg.Server != "" {
		note := T("voices.hostedNote", "Voices on a server send the text to the server while it reads. Allow them with Read aloud > Allow voices that use the internet.")
		if hostedUI.msg != "" {
			note += " " + hostedUI.msg
		}
		nodes = append(nodes, para("voices-hosted-note", note, false))
	}

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

	return append(nodes, voiceSourcesSection(u, e, lang, renderUI)...)
}
