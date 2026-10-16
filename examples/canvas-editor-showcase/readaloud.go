//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/xango"
)

// Read aloud: the selection or the document, spoken by the browser's own speech synthesis one
// sentence at a time, in the order and wording of the accessible copy. Nothing is sent by this page;
// whether a voice runs on the device is the browser's (voices that run there are preferred, and the
// menu says so).

type readState struct {
	// mark shows the sentence and the word being read in the document (nil: nowhere to show it); reveal says to
	// scroll to the sentence (when a new one starts).
	mark    func(sentFrom, sentTo, wordFrom, wordTo int, reveal bool)
	voice   string // what is speaking: "device", "network" or "" (the browser's own choice)
	chunks  []read.Chunk
	pos     int
	playing bool
	paused  bool
	gen     int // bumps on every start and stop, so a late callback of an old run is ignored
	cbs     []js.Func
}

var reading readState

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

// pickVoice is a voice for lang, one running on the device first, a network voice only when the person allowed
// them; undefined when there is none (the browser's default voice for the language is then used).
func pickVoice(lang string) js.Value {
	api := speechAPI()
	if lang == "" {
		lang = js.Global().Get("document").Get("documentElement").Get("lang").String()
	}
	if api.IsUndefined() || lang == "" {
		return js.Undefined()
	}
	voices := api.Call("getVoices")
	if name := curPrefs.Voice; name != "" {
		if v, ok := read.Find(engineVoices(), lang, name, networkVoices()); ok {
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

// engineVoices lists the voices the browser offers.
func engineVoices() []read.Voice {
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

// voiceMenu is the choice of reader: the browser's own pick, or one of the voices offered for the document's language,
// each described by its accent and gender (American, British; female, male).
func voiceMenu(u *ui, renderUI func()) components.MenuItem {
	choose := func(name string) func() {
		return func() {
			p := curPrefs
			p.Voice = name
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
	items := []components.MenuItem{{Label: T("read.voiceAutomatic", "Automatic"), Checked: curPrefs.Voice == "", OnSelect: choose("")}}
	list := read.ForLanguage(engineVoices(), lang, networkVoices())
	for _, v := range list {
		items = append(items, components.MenuItem{Label: read.Label(v), Checked: curPrefs.Voice == v.Name, OnSelect: choose(v.Name)})
	}
	if len(list) == 0 {
		items = append(items, components.MenuItem{Label: T("read.voiceNone", "No other voice for this language on this device"), Disabled: true})
	}
	return components.MenuItem{Label: T("read.voice", "Reader voice"), Items: items}
}

func stopReading() {
	reading.gen++
	reading.playing, reading.paused = false, false
	for _, f := range reading.cbs {
		f.Release()
	}
	reading.cbs = nil
	if api := speechAPI(); !api.IsUndefined() {
		api.Call("cancel")
	}
	reading.voice = ""
	publishRead(-1, 0, -1, 0)
	if reading.mark != nil {
		reading.mark(-1, 0, -1, 0, false)
	}
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
	if reading.mark != nil {
		reading.mark(sf, st, wordFrom, wordTo, reveal)
	}
}

// startReading speaks the chunks from the first.
func startReading(chunks []read.Chunk, renderUI func()) {
	stopReading()
	if len(chunks) == 0 || !speechSupported() {
		return
	}
	reading.chunks, reading.pos, reading.playing = chunks, 0, true
	speakCurrent(renderUI)
}

func speakCurrent(renderUI func()) {
	if !reading.playing || reading.pos >= len(reading.chunks) {
		stopReading()
		renderUI()
		return
	}
	gen := reading.gen
	c := reading.chunks[reading.pos]
	utt := js.Global().Get("SpeechSynthesisUtterance").New(c.Text)
	utt.Set("rate", speechRate())
	if c.Lang != "" {
		utt.Set("lang", c.Lang)
	}
	reading.voice = ""
	if v := pickVoice(c.Lang); !v.IsUndefined() {
		utt.Set("voice", v)
		reading.voice = "network"
		if v.Get("localService").Truthy() {
			reading.voice = "device"
		}
	}
	showRead(c, -1, 0, true)
	// the engine says where it is in the text: the word being said is marked in the document (browsers that do
	// not send boundary events leave the sentence marked)
	units := utf16Index(c.Text)
	boundary := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if gen != reading.gen || !reading.playing || len(args) == 0 || args[0].Get("name").String() != "word" {
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
		if from, to, ok := c.WordAt(i, length); ok {
			showRead(c, from, to, false)
		}
		return nil
	})
	reading.cbs = append(reading.cbs, boundary)
	utt.Set("onboundary", boundary)
	next := js.FuncOf(func(js.Value, []js.Value) any {
		if gen == reading.gen && reading.playing {
			reading.pos++
			speakCurrent(renderUI)
		}
		return nil
	})
	reading.cbs = append(reading.cbs, next)
	utt.Set("onend", next)
	utt.Set("onerror", next)
	speechAPI().Call("speak", utt)
	renderUI()
}

func pauseReading(renderUI func()) {
	if !reading.playing {
		return
	}
	if reading.paused {
		speechAPI().Call("resume")
	} else {
		speechAPI().Call("pause")
	}
	reading.paused = !reading.paused
	renderUI()
}

// skipReading moves to the previous (-1) or next (1) sentence.
func skipReading(by int, renderUI func()) {
	if !reading.playing {
		return
	}
	p := reading.pos + by
	if p < 0 {
		p = 0
	}
	reading.gen++ // the cancelled utterance's end callback is then ignored
	speechAPI().Call("cancel")
	reading.pos, reading.paused = p, false
	speakCurrent(renderUI)
}

// readMenu is the read-aloud entries of the Tools-like menu.
func readMenu(u *ui, flow *canvas.TextFlow, renderUI func()) []components.MenuItem {
	if !speechSupported() {
		return []components.MenuItem{{Label: T("read.unsupported", "Reading aloud is not offered by this browser"), Disabled: true}}
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
		{Label: T("read.stop", "Stop reading"), Disabled: !reading.playing, OnSelect: func() {
			stopReading()
			u.announce(T("read.stopped", "Stopped reading"))
			renderUI()
		}},
		{Separator: true},
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
	if !reading.playing || reading.pos >= len(reading.chunks) {
		return xango.Frag()
	}
	pauseLabel := T("read.pause", "Pause")
	if reading.paused {
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
		xango.El("mark", []xango.Attr{xango.A("class", "gj-readbar-now"), xango.A("id", "read-now")}, xango.Text(reading.chunks[reading.pos].Text)),
		xango.El("span", []xango.Attr{xango.A("class", "gj-readbar-voice"), xango.A("id", "read-voice")}, xango.Text(voiceWords(reading.voice))),
	)
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
	}
	return T("read.voiceDefault", "The browser's own voice")
}
