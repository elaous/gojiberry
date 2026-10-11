//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// Read aloud: the selection or the document, spoken by the browser's own speech synthesis one
// sentence at a time, in the order and wording of the accessible copy. Nothing is sent by this page;
// whether a voice runs on the device is the browser's (voices that run there are preferred, and the
// menu says so).

type readState struct {
	chunks  []components.SpeechChunk
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

// pickVoice is a voice for lang, one running on the device first; undefined when there is none
// (the browser's default voice for the language is then used).
func pickVoice(lang string) js.Value {
	api := speechAPI()
	if lang == "" {
		lang = js.Global().Get("document").Get("documentElement").Get("lang").String()
	}
	if api.IsUndefined() || lang == "" {
		return js.Undefined()
	}
	voices := api.Call("getVoices")
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
		if score == 0 || !v.Get("localService").Truthy() {
			continue // a voice that is not on the device is not picked
		}
		if score > bestScore {
			best, bestScore = v, score
		}
	}
	return best
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
}

// startReading speaks the chunks from the first.
func startReading(chunks []components.SpeechChunk, renderUI func()) {
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
	if v := pickVoice(c.Lang); !v.IsUndefined() {
		utt.Set("voice", v)
	}
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
			chunks := components.SpeechChunksOfText(flow.SelectedText(), u.docLang)
			u.announce(T("read.started", "Reading aloud"))
			startReading(chunks, renderUI)
		}},
		{Label: T("read.document", "Read the document aloud"), OnSelect: func() {
			chunks := components.SpeechChunks(u.a11yBlocks(flow, u.title), u.docLang)
			u.announce(T("read.started", "Reading aloud"))
			startReading(chunks, renderUI)
		}},
		{Label: T("read.stop", "Stop reading"), Disabled: !reading.playing, OnSelect: func() {
			stopReading()
			u.announce(T("read.stopped", "Stopped reading"))
			renderUI()
		}},
		{Separator: true},
		speed(T("read.slow", "Reading speed: slow"), "slow"),
		speed(T("read.normal", "Reading speed: normal"), ""),
		speed(T("read.fast", "Reading speed: fast"), "fast"),
		{Separator: true},
		{Label: T("read.note", "Only voices that run on this device are chosen"), Disabled: true},
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
	)
}
