package fonts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Blobs is where a Library keeps the font files it downloads: a
// key-value store of bytes. saver.Blobs adapts any docstore backend
// (Postgres, SQLite, S3, local files, ...) to it.
type Blobs interface {
	Put(ctx context.Context, key string, data []byte) error
	// Get returns found=false, with no error, for a key that is not there.
	Get(ctx context.Context, key string) (data []byte, found bool, err error)
}

// ErrNotAdded is returned by Use for a family that was never Added: only
// fonts the user picked in the font selector are downloaded.
var ErrNotAdded = errors.New("fonts: that family was not added to the library")

// KeyPrefix starts the keys a Library stores font files under.
const KeyPrefix = "fonts/"

// Ready describes a font file that is now in the store and in hand.
type Ready struct {
	Family  string
	Variant Variant // with Key set
	Data    []byte
	// Downloaded is true when this call fetched the file from its URL,
	// false when it came from the store.
	Downloaded bool
}

// Library is a document's fonts. Fonts the user chooses in the font
// selector are Added; the first time the document actually uses one of
// them (Use, normally driven by canvas.Stage.OnFontUse), its file is
// downloaded and kept in Blobs, and from then on loaded from there: a
// lazy download, so a font that was only previewed or picked and never
// used costs nothing.
type Library struct {
	// Blobs is the store; required for Use.
	Blobs Blobs
	// Fetch downloads a URL; nil means http.Get with the default client.
	Fetch func(ctx context.Context, url string) ([]byte, error)
	// OnReady is called, once per font file, when it is in the store:
	// load it into the browser here (see LoadFace). It may be called from
	// a goroutine.
	OnReady func(Ready)
	// OnError is called when a download or store fails (once per
	// attempt; the next Use tries again).
	OnError func(family string, err error)

	mu       sync.Mutex
	added    map[string]Font
	ready    map[string]bool // key -> reported
	inflight map[string]bool
}

// Add records a font chosen from the font selector. Nothing is
// downloaded yet. Adding a family again replaces its description but
// keeps the keys of files already stored.
func (l *Library) Add(f Font) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.added == nil {
		l.added = map[string]Font{}
	}
	f.Variants = append([]Variant(nil), f.Variants...)
	if old, ok := l.added[f.Family]; ok {
		for i, v := range f.Variants {
			if v.Key != "" {
				continue
			}
			for _, o := range old.Variants {
				if o.Weight == v.Weight && o.Italic == v.Italic && o.URL == v.URL {
					f.Variants[i].Key = o.Key
				}
			}
		}
	}
	l.added[f.Family] = f
}

// Added returns the added fonts, by family, with Key set on the
// variants that have been downloaded.
func (l *Library) Added() []Font {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Font, 0, len(l.added))
	for _, f := range l.added {
		f.Variants = append([]Variant(nil), f.Variants...)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// Key is the store key for a variant of family: "fonts/Family/700-n.woff2".
func Key(family string, v Variant) string {
	style := "n"
	if v.Italic {
		style = "i"
	}
	ext := ".font"
	if u, err := url.Parse(v.URL); err == nil {
		if e := strings.ToLower(path.Ext(u.Path)); e != "" && len(e) <= 6 {
			ext = e
		}
	}
	family = strings.NewReplacer("/", "_", "\\", "_").Replace(family)
	weight := v.Weight
	if weight == 0 {
		weight = 400
	}
	return fmt.Sprintf("%s%s/%d-%s%s", KeyPrefix, family, weight, style, ext)
}

func (l *Library) fetch(ctx context.Context, u string) ([]byte, error) {
	if l.Fetch != nil {
		return l.Fetch(ctx, u)
	}
	if pu, err := url.Parse(u); err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
		return nil, fmt.Errorf("fonts: %q is not an http(s) URL", u)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("fonts: %s: %s", u, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFontSize+1))
	if err == nil && len(data) > maxFontSize {
		err = fmt.Errorf("fonts: %s is larger than %d bytes", u, maxFontSize)
	}
	return data, err
}

const (
	fetchTimeout = 60 * time.Second
	maxFontSize  = 32 << 20
)

// Use makes sure the cut of family nearest weight/italic is in the store,
// downloading it first if this is the first use, and reports it through
// OnReady (once per file). It returns the store key, or "" for a font
// with no file to download (one installed on the device). A family that
// was never Added gives ErrNotAdded. Use blocks while it downloads; use
// UseAsync from an event handler.
func (l *Library) Use(ctx context.Context, family string, weight int, italic bool) (string, error) {
	l.mu.Lock()
	font, ok := l.added[family]
	l.mu.Unlock()
	if !ok {
		return "", ErrNotAdded
	}
	v, ok := font.Closest(weight, italic)
	if !ok || v.URL == "" {
		return "", nil
	}
	if l.Blobs == nil {
		err := errors.New("fonts: the library has no store")
		if l.OnError != nil {
			l.OnError(family, err)
		}
		return "", err
	}
	key := Key(family, v)

	l.mu.Lock()
	if l.inflight[key] {
		l.mu.Unlock()
		return key, nil
	}
	if l.ready[key] {
		l.mu.Unlock()
		return key, nil
	}
	if l.inflight == nil {
		l.inflight = map[string]bool{}
	}
	l.inflight[key] = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.inflight, key)
		l.mu.Unlock()
	}()

	data, found, err := l.Blobs.Get(ctx, key)
	downloaded := false
	if err == nil && !found {
		if data, err = l.fetch(ctx, v.URL); err == nil {
			err = l.Blobs.Put(ctx, key, data)
			downloaded = true
		}
	}
	if err != nil {
		if l.OnError != nil {
			l.OnError(family, err)
		}
		return "", err
	}
	v.Key = key
	l.mu.Lock()
	if l.ready == nil {
		l.ready = map[string]bool{}
	}
	l.ready[key] = true
	f := l.added[family]
	for i := range f.Variants {
		if f.Variants[i].Weight == v.Weight && f.Variants[i].Italic == v.Italic && f.Variants[i].URL == v.URL {
			f.Variants[i].Key = key
		}
	}
	l.added[family] = f
	l.mu.Unlock()
	if l.OnReady != nil {
		l.OnReady(Ready{Family: family, Variant: v, Data: data, Downloaded: downloaded})
	}
	return key, nil
}

// UseAsync is Use in a goroutine, for calling from event handlers (the
// result arrives through OnReady and OnError). A family that was not
// added is ignored.
func (l *Library) UseAsync(family string, weight int, italic bool) {
	go func() { _, _ = l.Use(context.Background(), family, weight, italic) }()
}
