package piper

import (
	"context"
	"crypto/md5" //nolint:gosec // the catalogue publishes MD5 sums; this checks a download is whole, not that it is authentic
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/elaous/gojiberry/voice/read"
)

// Engine is the Piper adapter: read.Engine for the voices that are installed and read.Downloader for the ones that
// can be. It holds the ports it needs; fill them before use.
type Engine struct {
	Config  Config
	Fetcher Fetcher
	Store   Store
	Runtime Runtime
	Output  Output

	catalogue []CatalogueVoice
	loaded    bool
	installed map[string]bool
}

// New is an engine with the ports.
func New(cfg Config, f Fetcher, s Store, rt Runtime, out Output) *Engine {
	return &Engine{Config: cfg, Fetcher: f, Store: s, Runtime: rt, Output: out, installed: map[string]bool{}}
}

var (
	_ read.Engine     = (*Engine)(nil)
	_ read.Downloader = (*Engine)(nil)
)

// modelKey and configKey are where a voice's two files are kept.
func modelKey(id string) string  { return id + ".onnx" }
func configKey(id string) string { return id + ".onnx.json" }

// ID implements read.Engine.
func (e *Engine) ID() string { return "piper" }

// Label implements read.Engine.
func (e *Engine) Label() string { return "Downloaded voices (Piper)" }

// Available implements read.Engine: a Piper runtime is in the page.
func (e *Engine) Available() bool { return e.Runtime != nil && e.Runtime.Available() }

// Refresh learns which voices are in the Store (call it when the page starts, before listing Voices).
func (e *Engine) Refresh(done func()) {
	e.Store.Keys(func(keys []string) {
		has := map[string]bool{}
		for _, k := range keys {
			has[k] = true
		}
		e.installed = map[string]bool{}
		for k := range has {
			if id, ok := strings.CutSuffix(k, ".onnx"); ok && has[configKey(id)] {
				e.installed[id] = true
			}
		}
		if done != nil {
			done()
		}
	})
}

// Voices implements read.Engine: the installed voices (named by the catalogue when it is loaded, else by their ID).
func (e *Engine) Voices() []read.Voice {
	var out []read.Voice
	byID := map[string]CatalogueVoice{}
	for _, c := range e.catalogue {
		byID[c.ID] = c
	}
	for id := range e.installed {
		if c, ok := byID[id]; ok {
			out = append(out, c.asVoice())
			continue
		}
		lang := strings.ReplaceAll(strings.SplitN(id, "-", 2)[0], "_", "-")
		out = append(out, read.Voice{ID: id, Name: id, Lang: lang, Local: true})
	}
	sortVoices(out)
	return out
}

func sortVoices(v []read.Voice) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j].Key() < v[j-1].Key(); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// Installed implements read.Downloader.
func (e *Engine) Installed(id string) bool { return e.installed[id] }

// LoadCatalogue implements read.Downloader.
func (e *Engine) LoadCatalogue(ctx context.Context, done func(error)) {
	if e.loaded {
		done(nil)
		return
	}
	e.Fetcher.Fetch(ctx, e.Config.Catalogue, nil, func(data []byte, err error) {
		if err != nil {
			done(err)
			return
		}
		list, err := ParseCatalogue(data)
		if err == nil {
			e.catalogue, e.loaded = list, true
		}
		done(err)
	})
}

// Catalogue implements read.Downloader.
func (e *Engine) Catalogue(lang string) []read.Installable {
	var out []read.Installable
	for _, c := range ForLanguage(e.catalogue, lang) {
		card := ""
		if c.Card != "" {
			card = c.base(e.Config.Base) + c.Card
		}
		if c.LicenceURL != "" {
			card = c.LicenceURL
		}
		out = append(out, read.Installable{Voice: c.asVoice(), Size: c.Size(), Quality: c.Quality, Licence: card, Terms: c.Licence, Credit: c.Credit, Source: c.Source, Installed: e.installed[c.ID]})
	}
	return out
}

// AddVoices adds voices of another source (the hub, a repository the person added) to the catalogue, replacing the ones
// with the same ID. Nothing is fetched.
func (e *Engine) AddVoices(list []CatalogueVoice) {
	for _, v := range list {
		replaced := false
		for i := range e.catalogue {
			if e.catalogue[i].ID == v.ID {
				e.catalogue[i], replaced = v, true
			}
		}
		if !replaced {
			e.catalogue = append(e.catalogue, v)
		}
	}
}

// SourceVoices is the voices of one source for a language ("" lists every source's own: the engine's catalogue).
func (e *Engine) SourceVoices(source, lang string) []read.Installable {
	var own []CatalogueVoice
	for _, c := range e.catalogue {
		if c.Source == source {
			own = append(own, c)
		}
	}
	saved := e.catalogue
	e.catalogue = own
	defer func() { e.catalogue = saved }()
	return e.Catalogue(lang)
}

func (e *Engine) find(id string) (CatalogueVoice, bool) {
	for _, c := range e.catalogue {
		if c.ID == id {
			return c, true
		}
	}
	return CatalogueVoice{}, false
}

// Install implements read.Downloader: it fetches the voice's configuration and model from below the base address,
// checks each against the size and MD5 sum the catalogue gives, and only then keeps both.
func (e *Engine) Install(ctx context.Context, id string, progress func(done, total int64), done func(error)) {
	v, ok := e.find(id)
	if !ok {
		done(ErrUnknownVoice)
		return
	}
	total := v.Size()
	report := func(base int64) func(received, _ int64) {
		return func(received, _ int64) {
			if progress != nil {
				progress(base+received, total)
			}
		}
	}
	check := func(data []byte, size int64, sum string) error {
		if size > 0 && int64(len(data)) != size {
			return ErrBadDownload
		}
		if sum != "" {
			h := md5.Sum(data) //nolint:gosec // see the import
			if !strings.EqualFold(hex.EncodeToString(h[:]), sum) {
				return ErrBadDownload
			}
		}
		return nil
	}
	e.Fetcher.Fetch(ctx, v.base(e.Config.Base)+v.Config, report(0), func(cfg []byte, err error) {
		if err == nil {
			err = check(cfg, v.ConfigSize, v.ConfigMD5)
		}
		if err != nil {
			done(err)
			return
		}
		e.Fetcher.Fetch(ctx, v.base(e.Config.Base)+v.Model, report(int64(len(cfg))), func(model []byte, err error) {
			if err == nil {
				err = check(model, v.ModelSize, v.ModelMD5)
			}
			if err != nil {
				done(err)
				return
			}
			e.Store.Put(configKey(id), cfg, func(err error) {
				if err != nil {
					done(err)
					return
				}
				e.Store.Put(modelKey(id), model, func(err error) {
					if err != nil {
						e.Store.Delete(configKey(id), func(error) { done(err) })
						return
					}
					e.installed[id] = true
					done(nil)
				})
			})
		})
	})
}

// Remove implements read.Downloader.
func (e *Engine) Remove(id string) error {
	var first error
	for _, k := range []string{modelKey(id), configKey(id)} {
		e.Store.Delete(k, func(err error) {
			if err != nil && first == nil {
				first = err
			}
		})
	}
	delete(e.installed, id)
	return first
}

// Speak implements read.Engine: it reads the voice's files from the Store, has the runtime make the sound, plays it
// and reports the words by how far the sound has got (Piper does not say where it is, so a word is reached when the
// share of the sound played equals the share of the text before it).
func (e *Engine) Speak(u read.Utterance, h read.Handler) read.Speech {
	s := &speech{}
	fail := func(err error) {
		if !s.cancelled && h.OnError != nil {
			h.OnError(err)
		}
	}
	id := u.Voice
	if id == "" {
		id = e.pick(u.Lang)
	}
	switch {
	case !e.Available():
		fail(ErrNoRuntime)
		return s
	case id == "" || !e.installed[id]:
		fail(ErrNotInstalled)
		return s
	}
	e.Store.Get(modelKey(id), func(model []byte, okM bool) {
		e.Store.Get(configKey(id), func(cfg []byte, okC bool) {
			if s.cancelled {
				return
			}
			if !okM || !okC {
				fail(ErrNotInstalled)
				return
			}
			rate := u.Rate
			if rate <= 0 {
				rate = 1
			}
			s.cancelSynth = e.Runtime.Synthesize(model, cfg, u.Text, rate, func(a Audio, err error) {
				if s.cancelled {
					return
				}
				if err == nil && len(a.Samples) == 0 {
					err = errors.New("piper: the runtime made no sound")
				}
				if err != nil {
					fail(err)
					return
				}
				s.playback = Say(e.Output, a, u.Text, read.KindDownloaded, s.guard(h))
			})
		})
	})
	return s
}

// pick is an installed voice for a language: the first of the language (voices are listed by name).
func (e *Engine) pick(lang string) string {
	base := strings.ToLower(strings.SplitN(strings.ReplaceAll(lang, "_", "-"), "-", 2)[0])
	var best string
	for _, v := range e.Voices() {
		vb := strings.ToLower(strings.SplitN(v.Lang, "-", 2)[0])
		if base != "" && vb != base {
			continue
		}
		if strings.EqualFold(v.Lang, strings.ReplaceAll(lang, "_", "-")) {
			return v.Key()
		}
		if best == "" {
			best = v.Key()
		}
	}
	return best
}

type speech struct {
	cancelled   bool
	cancelSynth func()
	playback    Playback
}

func (s *speech) Cancel() {
	s.cancelled = true
	if s.cancelSynth != nil {
		s.cancelSynth()
	}
	if s.playback != nil {
		s.playback.Stop()
	}
}

func (s *speech) Pause() {
	if s.playback != nil {
		s.playback.Pause()
	}
}

func (s *speech) Resume() {
	if s.playback != nil {
		s.playback.Resume()
	}
}

// wordMark is where a word starts in the text (in runes) and its length; at is that start as a count of runes.
type wordMark struct {
	index, length int
	at            int
}

type wordMarks []wordMark

// total is the length of the text in runes.
func (w wordMarks) total() int {
	if len(w) == 0 {
		return 0
	}
	last := w[len(w)-1]
	return last.index + last.length
}

// wordStarts lists the words of the text split at spaces.
func wordStarts(text string) wordMarks {
	var out wordMarks
	rs := []rune(text)
	for i := 0; i < len(rs); {
		for i < len(rs) && rs[i] == ' ' {
			i++
		}
		start := i
		for i < len(rs) && rs[i] != ' ' {
			i++
		}
		if i > start {
			out = append(out, wordMark{index: start, length: i - start, at: start})
		}
	}
	return out
}

// Say plays the sound of an utterance and reports to h as it goes: OnStart with kind, OnWord as the sound reaches each
// word (the engines that make the sound whole do not say where they are, so a word is reached when the share of the
// sound played equals the share of the text before it) and OnEnd. It is the part of reading aloud that every engine
// that hands over sound shares.
func Say(out Output, a Audio, text, kind string, h read.Handler) Playback {
	if h.OnStart != nil {
		h.OnStart(kind)
	}
	marks := wordStarts(text)
	next := 0
	dur := a.Duration()
	onTime := func(pos time.Duration) {
		if h.OnWord == nil || dur <= 0 {
			return
		}
		frac := float64(pos) / float64(dur)
		for next < len(marks) && float64(marks[next].at) <= frac*float64(marks.total()) {
			h.OnWord(marks[next].index, marks[next].length)
			next++
		}
	}
	return out.Play(a, onTime, func() {
		if h.OnEnd != nil {
			h.OnEnd()
		}
	})
}

// guard is h without the calls that come after the speech was cancelled.
func (s *speech) guard(h read.Handler) read.Handler {
	return read.Handler{
		OnStart: func(k string) {
			if !s.cancelled && h.OnStart != nil {
				h.OnStart(k)
			}
		},
		OnWord: func(i, n int) {
			if !s.cancelled && h.OnWord != nil {
				h.OnWord(i, n)
			}
		},
		OnEnd: func() {
			if !s.cancelled && h.OnEnd != nil {
				h.OnEnd()
			}
		},
		OnError: h.OnError,
	}
}
