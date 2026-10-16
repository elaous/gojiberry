package spell

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Dictionaries on demand. The editor carries English only; a document in another language gets its dictionary from
// a server that hosts Hunspell files (spell/dictserver, or any static host laid out the same way): Remote lists
// what is there, resolves a language tag to a dictionary ("de-AT" to "de_AT", else "de_DE") and loads it once.
// Loading is the expensive step (a big dictionary is a few megabytes and its affixes are expanded), so the result is
// kept; a failed load is not.

// Language is a dictionary a server offers.
type Language struct {
	Lang  string `json:"lang"`           // the dictionary's name, "de_DE"
	Name  string `json:"name,omitempty"` // for people, "Deutsch (Deutschland)"
	Bytes int64  `json:"bytes"`
}

// Remote loads dictionaries from a server. Make one and keep it: it caches what it loaded.
type Remote struct {
	// BaseURL is the address the dictionaries are under ("https://host/dict"), without a trailing slash.
	BaseURL string
	// HTTP is the client to use (http.DefaultClient when nil).
	HTTP *http.Client
	// MaxBytes refuses a file bigger than this once decompressed (64 MiB if 0).
	MaxBytes int64
	// Options say how much of the Hunspell format is used.
	Options HunspellOptions

	mu     sync.Mutex
	langs  []Language
	loaded map[string]*Dictionary
	flight map[string]*loadCall
}

type loadCall struct {
	done chan struct{}
	dict *Dictionary
	err  error
}

// ErrNoDictionary is returned when the server has no dictionary for a language.
var ErrNoDictionary = errors.New("spell: no dictionary for this language")

func (r *Remote) max() int64 {
	if r.MaxBytes > 0 {
		return r.MaxBytes
	}
	return 64 << 20
}

func (r *Remote) get(ctx context.Context, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(r.BaseURL, "/")+"/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}
	hc := r.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoDictionary
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("spell: the dictionary server answered %s", resp.Status)
	}
	// The transport undoes Content-Encoding it asked for itself; a gzip body that arrives still compressed (a static
	// host that stores .gz and says nothing) is recognised by its first bytes.
	data, err := io.ReadAll(io.LimitReader(resp.Body, r.max()+1))
	if err != nil {
		return nil, err
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		data, err = io.ReadAll(io.LimitReader(zr, r.max()+1))
		if err != nil {
			return nil, err
		}
	}
	if int64(len(data)) > r.max() {
		return nil, fmt.Errorf("spell: the dictionary file %s is over %d MiB", name, r.max()>>20)
	}
	return data, nil
}

// Languages lists what the server offers (asked once, then remembered).
func (r *Remote) Languages(ctx context.Context) ([]Language, error) {
	r.mu.Lock()
	if r.langs != nil {
		l := r.langs
		r.mu.Unlock()
		return l, nil
	}
	r.mu.Unlock()
	data, err := r.get(ctx, "index.json")
	if err != nil {
		return nil, err
	}
	var langs []Language
	if err := json.Unmarshal(data, &langs); err != nil {
		return nil, errors.New("spell: the dictionary list is not readable")
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i].Lang < langs[j].Lang })
	r.mu.Lock()
	r.langs = langs
	r.mu.Unlock()
	return langs, nil
}

// Resolve picks the dictionary for a language tag out of those on offer: the exact one ("de-AT" and "de_AT" are the
// same), else the one for the same language with the same script or region first, else the first of that language
// ("de" gives "de_DE", the alphabetically first).
func Resolve(tag string, offered []Language) (string, bool) {
	tag = strings.ReplaceAll(strings.TrimSpace(tag), "-", "_")
	if tag == "" {
		return "", false
	}
	primary, _, _ := strings.Cut(tag, "_")
	primary = strings.ToLower(primary)
	best := ""
	for _, l := range offered {
		if strings.EqualFold(l.Lang, tag) {
			return l.Lang, true
		}
		lp, _, _ := strings.Cut(strings.ReplaceAll(l.Lang, "-", "_"), "_")
		if strings.EqualFold(lp, primary) && (best == "" || l.Lang < best) {
			best = l.Lang
		}
	}
	return best, best != ""
}

// Load returns the dictionary of lang (a name from Languages, "de_DE"), fetching and reading it the first time.
// Several callers asking for the same one wait for one load. A load that fails is not remembered.
func (r *Remote) Load(ctx context.Context, lang string) (*Dictionary, error) {
	if !validLang(lang) {
		return nil, ErrNoDictionary
	}
	r.mu.Lock()
	if d := r.loaded[lang]; d != nil {
		r.mu.Unlock()
		return d, nil
	}
	if call := r.flight[lang]; call != nil {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.dict, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &loadCall{done: make(chan struct{})}
	if r.flight == nil {
		r.flight = map[string]*loadCall{}
	}
	r.flight[lang] = call
	r.mu.Unlock()

	// The load goes on if the caller that started it gives up: another may be waiting for it.
	go func() {
		d, err := r.fetch(context.WithoutCancel(ctx), lang)
		r.mu.Lock()
		if err == nil {
			if r.loaded == nil {
				r.loaded = map[string]*Dictionary{}
			}
			r.loaded[lang] = d
		}
		delete(r.flight, lang)
		r.mu.Unlock()
		call.dict, call.err = d, err
		close(call.done)
	}()
	select {
	case <-call.done:
		return call.dict, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func validLang(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func (r *Remote) fetch(ctx context.Context, lang string) (*Dictionary, error) {
	aff, err := r.get(ctx, lang+".aff")
	if err != nil {
		return nil, err
	}
	dic, err := r.get(ctx, lang+".dic")
	if err != nil {
		return nil, err
	}
	return LoadHunspell(dic, aff, r.Options), nil
}
