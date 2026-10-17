//go:build js && wasm

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/xango"
)

// Read aloud: the selection or the document, said one sentence at a time, in the order and wording of the accessible
// copy. What says it is an engine behind the port of package voice/read: the voices the browser offers (the device
// engine below) or a downloaded Piper voice that runs in the page (readpiper.go); the Player of that package walks the
// sentences over whichever is chosen. Nothing is sent by this page unless the person allowed voices that use the
// internet; whether a voice runs on the device is the browser's (voices that run there are preferred, and the menu
// says so).

// readMark shows the sentence and the word being read in the document (nil: nowhere to show it); reveal says to scroll
// to the sentence (when a new one starts).
var readMark func(sentFrom, sentTo, wordFrom, wordTo int, reveal bool)

// readRender draws the page again; it is the renderUI of the latest menu or bar built.
var readRender = func() {}

// reader is the Player and the engines it can use.
var reader = newReader()

type readerSet struct {
	player *read.Player
	device *webSpeechEngine
}

func newReader() *readerSet {
	r := &readerSet{device: &webSpeechEngine{}}
	r.player = &read.Player{
		Voice:    r.voiceFor,
		Rate:     speechRate,
		OnPiece:  func(c read.Chunk, reveal bool) { showRead(c, -1, 0, reveal) },
		OnWord:   func(c read.Chunk, from, to int) { showRead(c, from, to, false) },
		OnChange: func() { readRender() },
		OnStop: func() {
			r.device.release()
			publishRead(-1, 0, -1, 0)
			if readMark != nil {
				readMark(-1, 0, -1, 0, false)
			}
		},
		OnError: func(err error) { piperUI.msg = readErrorWords(err); readRender() },
	}
	return r
}

// engine is what reads now: the downloaded voices when the person chose them and the page can run them, else the
// voices of the device.
func (r *readerSet) engine() read.Engine {
	if curPrefs.Reader == "piper" && piperUI.engine != nil && piperUI.engine.Available() {
		return piperUI.engine
	}
	return r.device
}

// voiceFor is the ID of the voice the person chose for the engine in use ("" for its own pick).
func (r *readerSet) voiceFor(string) string {
	if r.engine() == read.Engine(r.device) {
		return curPrefs.Voice
	}
	return curPrefs.PVoice
}

// readErrorWords says why reading stopped.
func readErrorWords(err error) string {
	switch {
	case errors.Is(err, errNoPiperRuntime):
		return T("read.errRuntime", "This page has no runtime for downloaded voices, so it cannot read with one.")
	case errors.Is(err, errPiperNotInstalled):
		return T("read.errNotInstalled", "That voice is not downloaded. Choose another, or download it in the Reader voices tab.")
	}
	return fmt.Sprintf(T("read.errFailed", "Reading aloud stopped: %s"), err.Error())
}

func speechAPI() js.Value {
	s := js.Global().Get("speechSynthesis")
	if s.IsUndefined() || s.IsNull() {
		return js.Undefined()
	}
	return s
}

func speechSupported() bool { return !speechAPI().IsUndefined() }

// speechRate is the speed the person chose: 0.75, 1 or 1.5.
func speechRate() float64 {
	switch curPrefs.Speed {
	case "slow":
		return 0.75
	case "fast":
		return 1.5
	}
	return 1
}

// networkVoices says whether the person allowed voices that may send the text to a speech service on the
// internet (Preferences > Typing and reading aloud). It is off unless chosen.
func networkVoices() bool { return curPrefs.Voices == "net" }

// webSpeechEngine is the adapter for the browser's speech synthesis (read.Engine): the voices the device has, and,
// when allowed, those that use a speech service.
type webSpeechEngine struct{ funcs []js.Func }

func (e *webSpeechEngine) ID() string      { return "device" }
func (e *webSpeechEngine) Label() string   { return "Voices on this device" }
func (e *webSpeechEngine) Available() bool { return speechSupported() }

// Voices implements read.Engine: the voices the browser offers.
func (e *webSpeechEngine) Voices() []read.Voice {
	api := speechAPI()
	if api.IsUndefined() {
		return nil
	}
	list := api.Call("getVoices")
	out := make([]read.Voice, 0, list.Length())
	for i := 0; i < list.Length(); i++ {
		v := list.Index(i)
		out = append(out, read.Voice{Name: v.Get("name").String(), Lang: v.Get("lang").String(), Local: v.Get("localService").Truthy()})
	}
	return out
}

// release lets go of the callbacks handed to the browser.
func (e *webSpeechEngine) release() {
	for _, f := range e.funcs {
		f.Release()
	}
	e.funcs = nil
}

type webSpeech struct{ e *webSpeechEngine }

func (webSpeech) Cancel() {
	if api := speechAPI(); !api.IsUndefined() {
		api.Call("cancel")
	}
}
func (webSpeech) Pause() {
	if api := speechAPI(); !api.IsUndefined() {
		api.Call("pause")
	}
}
func (webSpeech) Resume() {
	if api := speechAPI(); !api.IsUndefined() {
		api.Call("resume")
	}
}

// Speak implements read.Engine.
func (e *webSpeechEngine) Speak(u read.Utterance, h read.Handler) read.Speech {
	utt := js.Global().Get("SpeechSynthesisUtterance").New(u.Text)
	utt.Set("rate", u.Rate)
	if u.Lang != "" {
		utt.Set("lang", u.Lang)
	}
	if v := pickVoice(u.Lang, u.Voice); !v.IsUndefined() {
		utt.Set("voice", v)
		if h.OnStart != nil {
			if v.Get("localService").Truthy() {
				h.OnStart(read.KindDevice)
			} else {
				h.OnStart(read.KindNetwork)
			}
		}
	} else if h.OnStart != nil {
		h.OnStart("")
	}
	// the engine says where it is in the text: the word being said is reported (browsers that do not send
	// boundary events leave the sentence marked)
	units := utf16Index(u.Text)
	boundary := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if h.OnWord == nil || len(args) == 0 || args[0].Get("name").String() != "word" {
			return nil
		}
		ci := args[0].Get("charIndex").Int()
		length := 0
		if l := args[0].Get("charLength"); l.Type() == js.TypeNumber {
			length = l.Int()
		}
		i := runeIndexAt(units, ci)
		if length > 0 {
			length = runeIndexAt(units, ci+length) - i
		}
		h.OnWord(i, length)
		return nil
	})
	end := js.FuncOf(func(js.Value, []js.Value) any {
		if h.OnEnd != nil {
			h.OnEnd() // an error ends the sentence too: reading goes on with the next
		}
		return nil
	})
	e.funcs = append(e.funcs, boundary, end)
	utt.Set("onboundary", boundary)
	utt.Set("onend", end)
	utt.Set("onerror", end)
	speechAPI().Call("speak", utt)
	return webSpeech{e}
}

// pickVoice is the voice for lang: the one the person named when it is one to offer, else one running on the device
// first, a network voice only when the person allowed them; undefined when there is none (the browser's default
// voice for the language is then used).
func pickVoice(lang, name string) js.Value {
	api := speechAPI()
	if lang == "" {
		lang = js.Global().Get("document").Get("documentElement").Get("lang").String()
	}
	if api.IsUndefined() || lang == "" {
		return js.Undefined()
	}
	voices := api.Call("getVoices")
	if name != "" {
		if v, ok := read.Find(reader.device.Voices(), lang, name, networkVoices()); ok {
			for i := 0; i < voices.Length(); i++ {
				if e := voices.Index(i); e.Get("name").String() == v.Name && e.Get("lang").String() == v.Lang {
					return e
				}
			}
		}
	}
	base := strings.ToLower(strings.SplitN(lang, "-", 2)[0])
	best, bestScore := js.Undefined(), 0
	for i := 0; i < voices.Length(); i++ {
		v := voices.Index(i)
		vl := strings.ToLower(v.Get("lang").String())
		score := 0
		switch {
		case vl == strings.ToLower(lang):
			score = 3
		case strings.SplitN(vl, "-", 2)[0] == base:
			score = 2
		}
		if score == 0 {
			continue
		}
		if !v.Get("localService").Truthy() {
			if !networkVoices() {
				continue // a voice that is not on the device is not picked unless the person allowed it
			}
			score-- // and a voice on the device is preferred to it for the same language
			if score == 0 {
				continue
			}
		}
		if score > bestScore {
			best, bestScore = v, score
		}
	}
	return best
}

// voiceMenu is the choice of reader: the browser's own pick, or one of the voices offered for the document's language,
// each described by its accent and gender (American, British; female, male).
func voiceMenu(u *ui, renderUI func()) components.MenuItem {
	piperOn := reader.engine() != read.Engine(reader.device)
	choose := func(name string) func() {
		return func() {
			p := curPrefs
			if piperOn {
				p.PVoice = name
			} else {
				p.Voice = name
			}
			setPrefs(p)
			if name == "" {
				u.announce(T("read.voiceAuto", "Reader voice: the browser's choice"))
			} else {
				u.announce(fmt.Sprintf(T("read.voiceChosen", "Reader voice: %s"), name))
			}
			renderUI()
		}
	}
	lang := u.docLang
	if lang == "" {
		lang = js.Global().Get("document").Get("documentElement").Get("lang").String()
	}
	chosen, voices, allowNet := curPrefs.Voice, reader.device.Voices(), networkVoices()
	if piperOn {
		chosen, voices, allowNet = curPrefs.PVoice, reader.engine().Voices(), true
	}
	items := []components.MenuItem{{Label: T("read.voiceAutomatic", "Automatic"), Checked: chosen == "", OnSelect: choose("")}}
	list := read.ForLanguage(voices, lang, allowNet)
	for _, v := range list {
		items = append(items, components.MenuItem{Label: read.Label(v), Checked: chosen == v.Key(), OnSelect: choose(v.Key())})
	}
	if len(list) == 0 {
		items = append(items, components.MenuItem{Label: T("read.voiceNone", "No other voice for this language on this device"), Disabled: true})
	}
	return components.MenuItem{Label: T("read.voice", "Reader voice"), Items: items}
}

// stopReading ends the reading.
func stopReading() { reader.player.Stop() }

// startReading says the chunks from the first, with the engine the person chose.
func startReading(chunks []read.Chunk, renderUI func()) {
	readRender = renderUI
	reader.player.Engine = reader.engine()
	reader.player.Start(chunks)
}

func pauseReading(renderUI func()) { readRender = renderUI; reader.player.Pause() }

// skipReading moves to the previous (-1) or next (1) sentence.
func skipReading(by int, renderUI func()) { readRender = renderUI; reader.player.Skip(by) }

// readerChoice is the entry that picks the engine.
func readerMenu(u *ui, renderUI func()) components.MenuItem {
	pick := func(value, say string) func() {
		return func() {
			p := curPrefs
			p.Reader = value
			setPrefs(p)
			stopReading()
			u.announce(say)
			if value == "piper" {
				ensurePiper(renderUI)
			}
			renderUI()
		}
	}
	piperOK := piperUI.engine != nil && piperUI.engine.Available()
	return components.MenuItem{Label: T("read.reader", "Reader"), Items: []components.MenuItem{
		{Label: T("read.readerDevice", "Voices on this device"), Checked: curPrefs.Reader != "piper", OnSelect: pick("", T("read.readerDeviceSaid", "Reader: voices on this device"))},
		{Label: T("read.readerPiper", "Downloaded voices (Piper)"), Checked: curPrefs.Reader == "piper", OnSelect: pick("piper", T("read.readerPiperSaid", "Reader: downloaded voices")), Disabled: piperUI.checked && !piperOK},
		{Separator: true},
		{Label: T("read.voicesTab", "Reader voices…"), OnSelect: func() { u.tab = "voices"; ensurePiper(renderUI); renderUI() }},
	}}
}

// readMenu is the read-aloud entries of the Tools-like menu.
func readMenu(u *ui, flow *canvas.TextFlow, renderUI func()) []components.MenuItem {
	readRender = renderUI
	if !speechSupported() && curPrefs.Reader != "piper" {
		return []components.MenuItem{{Label: T("read.unsupported", "Reading aloud is not offered by this browser"), Disabled: true}, readerMenu(u, renderUI)}
	}
	speed := func(label, value string) components.MenuItem {
		return components.MenuItem{Label: label, Checked: curPrefs.Speed == value, OnSelect: func() {
			p := curPrefs
			p.Speed = value
			setPrefs(p)
			u.announce(label)
			renderUI()
		}}
	}
	return []components.MenuItem{
		{Label: T("read.selection", "Read the selection aloud"), Disabled: flow == nil || !flow.HasSelection(), OnSelect: func() {
			from, _ := flow.Cursors()[0].Range()
			chunks := read.OfText(flow.SelectedText(), u.docLang, from)
			u.announce(T("read.started", "Reading aloud"))
			startReading(chunks, renderUI)
		}},
		{Label: T("read.document", "Read the document aloud"), OnSelect: func() {
			chunks := read.Chunks(u.a11yBlocks(flow, u.title), u.docLang)
			u.announce(T("read.started", "Reading aloud"))
			startReading(chunks, renderUI)
		}},
		{Label: T("read.stop", "Stop reading"), Disabled: !reader.player.Playing(), OnSelect: func() {
			stopReading()
			u.announce(T("read.stopped", "Stopped reading"))
			renderUI()
		}},
		{Separator: true},
		readerMenu(u, renderUI),
		voiceMenu(u, renderUI),
		speed(T("read.slow", "Reading speed: slow"), "slow"),
		speed(T("read.normal", "Reading speed: normal"), ""),
		speed(T("read.fast", "Reading speed: fast"), "fast"),
		{Separator: true},
		{Label: T("read.network", "Allow voices that use the internet"), Checked: networkVoices(), OnSelect: func() {
			p := curPrefs
			p.Voices = onOff(!networkVoices(), "net")
			setPrefs(p)
			if networkVoices() {
				u.announce(T("read.networkOn", "Voices that use the internet are allowed: the browser may send the text it reads to a speech service"))
			} else {
				u.announce(T("read.networkOff", "Only voices that run on this device are chosen"))
			}
			renderUI()
		}},
		{Label: voiceNote(), Disabled: true},
	}
}

// readBar is the controls and the sentence being spoken, shown while reading.
func readBar(u *ui, renderUI func()) xango.Node {
	readRender = renderUI
	cur, ok := reader.player.Current()
	if !ok {
		return xango.Frag()
	}
	pauseLabel := T("read.pause", "Pause")
	if reader.player.Paused() {
		pauseLabel = T("read.resume", "Resume")
	}
	btn := func(label string, f func()) xango.Node {
		return components.Button(components.ButtonProps{Label: label, Variant: components.ButtonSecondary, OnClick: func(xango.Event) { f() }})
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-readbar"), xango.A("id", "read-bar"), xango.A("role", "group"), xango.A("aria-label", T("read.bar", "Reading aloud"))},
		btn(T("read.prev", "Previous sentence"), func() { skipReading(-1, renderUI) }),
		btn(pauseLabel, func() { pauseReading(renderUI) }),
		btn(T("read.next", "Next sentence"), func() { skipReading(1, renderUI) }),
		btn(T("read.stop", "Stop reading"), func() { stopReading(); u.announce(T("read.stopped", "Stopped reading")); renderUI() }),
		xango.El("mark", []xango.Attr{xango.A("class", "gj-readbar-now"), xango.A("id", "read-now")}, xango.Text(cur.Text)),
		xango.El("span", []xango.Attr{xango.A("class", "gj-readbar-voice"), xango.A("id", "read-voice")}, xango.Text(voiceWords(reader.player.Kind()))),
	)
}

// publishRead puts the sentence and the word being read on the root element (data-gj-read-sentence and
// data-gj-read-word, "from-to" in stream offsets), for the page's own tests and for tools that watch it.
func publishRead(sf, st, wf, wt int) {
	root := js.Global().Get("document").Get("documentElement")
	set := func(name string, from, to int) {
		if from < 0 {
			root.Call("removeAttribute", name)
			return
		}
		root.Call("setAttribute", name, strconv.Itoa(from)+"-"+strconv.Itoa(to))
	}
	set("data-gj-read-sentence", sf, st)
	set("data-gj-read-word", wf, wt)
}

// showRead marks the chunk being read (and the word of it, when wordFrom is not negative).
func showRead(c read.Chunk, wordFrom, wordTo int, reveal bool) {
	sf, st, ok := c.Span()
	if !ok {
		sf, st, wordFrom, wordTo = -1, 0, -1, 0
	}
	publishRead(sf, st, wordFrom, wordTo)
	if readMark != nil {
		readMark(sf, st, wordFrom, wordTo, reveal)
	}
}

// utf16Index lists, for each rune of s, the index of its first UTF-16 unit: the engine counts characters that way.
func utf16Index(s string) []int {
	var at []int
	n := 0
	for _, r := range s {
		at = append(at, n)
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return at
}

// runeIndexAt is the index of the rune that holds UTF-16 unit u (the last rune if u is past the text).
func runeIndexAt(units []int, u int) int {
	for i := len(units) - 1; i >= 0; i-- {
		if units[i] <= u {
			return i
		}
	}
	return 0
}

// voiceNote says which voices are chosen, in words.
func voiceNote() string {
	if networkVoices() {
		return T("read.noteNetwork", "Voices on this device first; others may send the text to a speech service")
	}
	return T("read.note", "Only voices that run on this device are chosen")
}

// voiceWords says what is speaking.
func voiceWords(kind string) string {
	switch kind {
	case "device":
		return T("read.voiceDevice", "Voice on this device")
	case "network":
		return T("read.voiceNetwork", "Voice that uses the internet")
	case "downloaded":
		return T("read.voiceDownloaded", "Downloaded voice, running in this page")
	}
	return T("read.voiceDefault", "The browser's own voice")
}
