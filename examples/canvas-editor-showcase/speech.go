//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/speechcoach"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The Speech tab (roadmap 56): record the phrases of a set to build a personal collection of one's own
// speech, and dictate into the document through a recogniser the person names. The microphone is opened
// only when the person presses Record or Dictate, and closed when they stop. The recordings stay in this
// page (closing it discards them) until the person downloads them; nothing is sent anywhere except one
// recording to the recogniser address the person typed, when they press Dictate.

type speechUI struct {
	session  *speechcoach.Session
	data     speechcoach.Dataset
	rec      *recorder
	mode     string // "", "phrase" or "dictate": what the recording in progress is for
	message  string
	recogniz string // the address of the recogniser ("" = dictation off)
	confirm  bool   // "delete all" was pressed once
	render   func()
	host     speechHost

	// kept between visits (speechkeep.go): only when the person asked, and sealed
	key    func() (key []byte, v sealedVault, why string)
	keep   bool
	kept   map[string]bool // the recordings the vault is known to hold
	loaded bool            // the kept recordings were read this visit

	// what was learned from the person's corrections, and the last dictation they can be learned from
	corr *speechcoach.Corrections
	last lastDictation

	research researchState // the optional contribution (speechresearch.go)
}

// speechHost is what the Speech tab needs of the document: put text at the caret, read the selection and a
// stretch of text, replace a stretch.
type speechHost struct {
	Caret    func() int
	Insert   func(text string) bool
	Selected func() string
	Slice    func(from, to int) string
	Replace  func(from, to int, text string)
}

// lastDictation is the text the last dictation put in the document and where (a caret offset), so that earlier
// corrections can be offered on it and the person's correction of it can be learned.
type lastDictation struct {
	text     string
	from     int
	inserted bool
}

const speechKeepKey = "showcase:speech.keep"

func newSpeech(render func()) *speechUI {
	return &speechUI{session: speechcoach.NewSession(speechcoach.SetFor(locale)), render: render,
		keep: lsGet(speechKeepKey) == "on", kept: map[string]bool{}, corr: &speechcoach.Corrections{}}
}

// recorder captures the microphone as float samples until stop.
type recorder struct {
	stream, actx, src, node js.Value
	fn                      js.Func
	chunks                  [][]float32
	total                   int
	rate                    int
}

var errNoMic = errors.New("the microphone is not available, or it was not allowed")

func startRecorder(done func(*recorder, error)) {
	md := js.Global().Get("navigator").Get("mediaDevices")
	if md.IsUndefined() || md.IsNull() {
		done(nil, errNoMic)
		return
	}
	var onOK, onErr js.Func
	release := func() { onOK.Release(); onErr.Release() }
	onOK = js.FuncOf(func(_ js.Value, a []js.Value) any {
		defer release()
		ctor := js.Global().Get("AudioContext")
		if ctor.IsUndefined() {
			ctor = js.Global().Get("webkitAudioContext")
		}
		r := &recorder{stream: a[0], actx: ctor.New()}
		r.rate = int(r.actx.Get("sampleRate").Float())
		r.src = r.actx.Call("createMediaStreamSource", r.stream)
		r.node = r.actx.Call("createScriptProcessor", 4096, 1, 1)
		r.fn = js.FuncOf(func(_ js.Value, ev []js.Value) any {
			ch := ev[0].Get("inputBuffer").Call("getChannelData", 0)
			n := ch.Get("length").Int()
			raw := make([]byte, n*4)
			js.CopyBytesToGo(raw, js.Global().Get("Uint8Array").New(ch.Get("buffer"), ch.Get("byteOffset"), n*4))
			f := make([]float32, n)
			for i := range f {
				f[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			}
			r.chunks = append(r.chunks, f)
			r.total += n
			return nil
		})
		r.node.Set("onaudioprocess", r.fn)
		r.src.Call("connect", r.node)
		r.node.Call("connect", r.actx.Get("destination"))
		done(r, nil)
		return nil
	})
	onErr = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		done(nil, errNoMic)
		return nil
	})
	md.Call("getUserMedia", map[string]any{"audio": true}).Call("then", onOK).Call("catch", onErr)
}

// stop closes the microphone and returns the take at 16 kHz.
func (r *recorder) stop() []int16 {
	r.node.Set("onaudioprocess", js.Null())
	r.src.Call("disconnect")
	r.node.Call("disconnect")
	tracks := r.stream.Call("getTracks")
	for i := 0; i < tracks.Length(); i++ {
		tracks.Index(i).Call("stop")
	}
	r.actx.Call("close")
	r.fn.Release()
	all := make([]float32, 0, r.total)
	for _, c := range r.chunks {
		all = append(all, c...)
	}
	return speechcoach.Resample(all, r.rate)
}

// toggle starts or stops the recording for a purpose.
func (s *speechUI) toggle(mode string) {
	if s.rec != nil { // stop
		pcm := s.rec.stop()
		m := s.mode
		s.rec, s.mode = nil, ""
		switch m {
		case "phrase":
			s.keepTake(pcm)
		case "dictate":
			s.dictate(pcm)
		}
		s.render()
		return
	}
	s.message = T("speech.starting", "Opening the microphone…")
	s.render()
	startRecorder(func(r *recorder, err error) {
		if err != nil {
			s.message = T("speech.noMic", "The microphone is not available, or it was not allowed.")
		} else {
			s.rec, s.mode = r, mode
			s.message = T("speech.recording", "Recording. Press the button again to stop.")
		}
		s.render()
	})
}

func (s *speechUI) keepTake(pcm []int16) {
	now := time.Now()
	t, err := s.session.Record(pcm, now)
	if err != nil {
		s.message = T("speech.empty", "Nothing was recorded. Try again.")
		return
	}
	s.data.Add(speechcoach.NewEntry(s.session.Set().Lang, s.session.Phrase(), s.session.Phrase(), speechcoach.Prompted, t.PCM, t.Hints, now))
	s.persist()
	s.message = T("speech.kept", "Saved this take. You can record it again, or go on.")
	if len(t.Hints) > 0 {
		s.message += " " + strings.Join(t.Hints, " ")
	}
}

func (s *speechUI) dictate(pcm []int16) {
	addr, err := speechcoach.CheckEndpoint(s.recogniz)
	if err != nil || len(pcm) == 0 {
		s.message = T("speech.noAddress", "Dictation needs the address of a recogniser. Type it above.")
		return
	}
	s.message = T("speech.sending", "Sending this one recording to your recogniser…")
	prompt := (&speechcoach.Vocabulary{}).Prompt(s.corr, 500) // the words the person has meant before, as a hint
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		text, err := (&speechcoach.Recogniser{URL: addr}).Transcribe(ctx, pcm, prompt, s.session.Set().Lang)
		switch {
		case err != nil:
			s.message = T("speech.failed", "The recogniser did not answer. Nothing was added.")
		case text == "":
			s.message = T("speech.nothingHeard", "The recogniser heard nothing.")
		default:
			from := 0
			if s.host.Caret != nil {
				from = s.host.Caret()
			}
			if s.host.Insert != nil && s.host.Insert(text) {
				s.last = lastDictation{text: text, from: from, inserted: true}
				s.message = T("speech.inserted", "Added to the document.")
			} else {
				s.last = lastDictation{}
				s.message = T("speech.notInserted", "Heard: %s. Switch to editing mode and put the caret where it should go, then dictate again.", text)
			}
		}
		s.render()
	}()
}

func (s *speechUI) export() {
	var b bytes.Buffer
	if _, err := s.data.Export(&b, nil); err != nil {
		s.message = T("speech.exportFailed", "The recordings could not be packed.")
		return
	}
	transfer.Download("my-speech.zip", "application/zip", b.Bytes())
}

func (s *speechUI) panel(host speechHost) xango.Node {
	s.host = host
	s.loadKept()
	set := s.session.Set()
	prog := s.session.Progress()
	rec := s.rec != nil
	recLabel := T("speech.record", "Record this phrase")
	if rec && s.mode == "phrase" {
		recLabel = T("speech.stop", "Stop recording")
	}
	dictLabel := T("speech.dictate", "Dictate into the document")
	if rec && s.mode == "dictate" {
		dictLabel = T("speech.stopDictate", "Stop and send")
	}
	p := func(id, text string) xango.Node {
		return xango.El("p", []xango.Attr{xango.A("id", id), xango.A("style", "font-size:13px")}, xango.Text(text))
	}
	row := func(kids ...xango.Node) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin:6px 0")}, kids...)
	}
	del := T("speech.delete", "Delete all my recordings")
	if s.confirm {
		del = T("speech.deleteSure", "Really delete them all")
	}
	other := rec && s.mode == "dictate"
	phraseBusy := rec && s.mode == "phrase"
	return xango.El("section", []xango.Attr{xango.A("id", "speech"), xango.A("aria-labelledby", "speech-heading")},
		xango.El("h3", []xango.Attr{xango.A("id", "speech-heading")}, xango.Text(T("speech.heading", "Speech"))),
		p("speech-intro", T("speech.intro", "Say the phrases below to build a collection of your own speech, and dictate into the document. The microphone is on only while you record. Your recordings stay in this page, and are gone when you close it, unless you download them.")),
		xango.El("h4", nil, xango.Text(set.Title)),
		p("speech-progress", T("speech.progress", "Phrase %d of %d. Recorded: %d. Skipped: %d.", s.session.Index()+1, s.session.Len(), prog.Recorded, prog.Skipped)),
		xango.El("p", []xango.Attr{xango.A("id", "speech-phrase"), xango.A("style", "font-size:18px;margin:8px 0")}, xango.Text(s.session.Phrase())),
		row(
			btn("speech-record", recLabel, func() { s.toggle("phrase") }, other),
			btn("speech-discard", T("speech.discard", "Throw away this take"), func() { s.session.Discard(); s.message = ""; s.render() }, rec),
			btn("speech-prev", T("speech.prev", "Previous"), func() { s.session.Prev(); s.message = ""; s.render() }, phraseBusy),
			btn("speech-skip", T("speech.skip", "Skip"), func() { s.session.Skip(); s.message = ""; s.render() }, phraseBusy),
			btn("speech-next", T("speech.next", "Next"), func() { s.session.Next(); s.message = ""; s.render() }, phraseBusy),
		),
		xango.El("h4", nil, xango.Text(T("speech.dictHeading", "Dictate"))),
		p("speech-dict-intro", T("speech.dictIntro", "Dictation sends one recording to a recogniser at the address you type (for example the voice server you run), only when you press stop. Leave it empty to keep dictation off.")),
		xango.El("label", []xango.Attr{xango.A("style", "display:block;font-size:13px")},
			xango.Text(T("speech.address", "Recogniser address")+" "),
			xango.El("input", []xango.Attr{xango.A("id", "speech-address"), xango.A("type", "url"), xango.A("value", s.recogniz), xango.A("placeholder", "https://…/transcribe"),
				xango.A("style", "width:100%;box-sizing:border-box"),
				xango.On("input", func(ev xango.Event) { s.recogniz = xango.EventValue[string](ev); s.render() })})),
		row(btn("speech-dictate", dictLabel, func() { s.toggle("dictate") }, phraseBusy || (!other && strings.TrimSpace(s.recogniz) == ""))),
		s.dictationAftermath(),
		xango.El("h4", nil, xango.Text(T("speech.mine", "My recordings"))),
		p("speech-count", Tn("speech.count", "%d recording, %s seconds.", "%d recordings, %s seconds.", s.data.Len(), strconv.FormatFloat(s.seconds(), 'f', 1, 64))),
		row(
			btn("speech-download", T("speech.download", "Download my recordings"), s.export, s.data.Len() == 0),
			btn("speech-delete", del, func() {
				if !s.confirm {
					s.confirm = true
				} else {
					s.data.Clear()
					s.persist()
					s.confirm, s.message = false, T("speech.deleted", "All recordings were deleted.")
				}
				s.render()
			}, s.data.Len() == 0),
		),
		s.keepNode(),
		s.researchNode(),
		xango.El("p", []xango.Attr{xango.A("id", "speech-message"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("style", "font-size:13px")}, xango.Text(s.message)),
	)
}

func (s *speechUI) seconds() float64 {
	var t float64
	for _, e := range s.data.List() {
		t += e.Seconds
	}
	return t
}
