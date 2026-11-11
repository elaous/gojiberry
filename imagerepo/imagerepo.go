package imagerepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Result is one picture a repository found.
type Result struct {
	Repo       string // the repository's name
	ID         string
	Title      string
	Creator    string
	License    string // short name: "CC0", "Public domain", "CC BY 4.0"
	LicenseURL string
	Source     string // the page the picture lives on
	Thumb      string // address of a small preview
	URL        string
	W, H       int
}

// Repository is one place to search.
type Repository interface {
	Name() string
	// Search finds pictures for query; page starts at 1.
	Search(ctx context.Context, query string, page int) ([]Result, error)
}

var accepted = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/avif": true, "image/svg+xml": true}

// DefaultMaxBytes is the largest picture Fetch reads.
const DefaultMaxBytes = 8 << 20

var ErrNotEnabled = errors.New("imagerepo: repository is not enabled")

// ErrTooLarge and ErrBadType are the limits Fetch enforces.
var (
	ErrTooLarge = errors.New("imagerepo: picture is too large")
	ErrBadType  = errors.New("imagerepo: not a png, jpeg, gif, webp, avif or svg picture")
)

type Library struct {
	// Client is used for every fetch; nil means http.DefaultClient.
	Client *http.Client
	// MaxBytes limits one fetched picture; 0 means DefaultMaxBytes.
	MaxBytes int64

	mu      sync.Mutex
	repos   map[string]Repository
	enabled map[string]bool
	cache   map[string]cached
	order   []string
}

type cached struct {
	data []byte
	mime string
}

const cacheMax = 16

func New() *Library {
	return &Library{repos: map[string]Repository{}, enabled: map[string]bool{}, cache: map[string]cached{}}
}

// Register adds a repository (replacing one of the same name).
func (l *Library) Register(r Repository) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.repos[r.Name()] = r
}

// Names lists the registered repositories, sorted.
func (l *Library) Names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.repos))
	for n := range l.repos {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (l *Library) Enable(name string, on bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.repos[name]; !ok {
		return false
	}
	l.enabled[name] = on
	return true
}

// Enabled reports whether a repository is on.
func (l *Library) Enabled(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled[name]
}

// Search asks one enabled repository.
func (l *Library) Search(ctx context.Context, name, query string, page int) ([]Result, error) {
	l.mu.Lock()
	r, ok := l.repos[name]
	on := l.enabled[name]
	l.mu.Unlock()
	if !ok || !on {
		return nil, ErrNotEnabled
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if page < 1 {
		page = 1
	}
	return r.Search(ctx, query, page)
}

func (l *Library) Fetch(ctx context.Context, res Result) (data []byte, mime string, err error) {
	if !l.Enabled(res.Repo) {
		return nil, "", ErrNotEnabled
	}
	l.mu.Lock()
	if c, ok := l.cache[res.URL]; ok {
		l.mu.Unlock()
		return c.data, c.mime, nil
	}
	l.mu.Unlock()
	max := l.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, res.URL, nil)
	if err != nil {
		return nil, "", err
	}
	c := l.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("imagerepo: %s answered %d", res.Repo, resp.StatusCode)
	}
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	if !accepted[mime] {
		return nil, "", ErrBadType
	}
	if resp.ContentLength > max {
		return nil, "", ErrTooLarge
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > max {
		return nil, "", ErrTooLarge
	}
	l.mu.Lock()
	if len(l.order) >= cacheMax {
		delete(l.cache, l.order[0])
		l.order = l.order[1:]
	}
	l.cache[res.URL] = cached{data, mime}
	l.order = append(l.order, res.URL)
	l.mu.Unlock()
	return data, mime, nil
}

func (r Result) Credit() string {
	var b strings.Builder
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "Picture"
	}
	b.WriteString(title)
	if c := strings.TrimSpace(r.Creator); c != "" {
		b.WriteString(" by " + c)
	}
	if r.License != "" {
		b.WriteString(", " + r.License)
	}
	if r.Repo != "" {
		b.WriteString(", via " + r.Repo)
	}
	return b.String()
}

func getJSON(ctx context.Context, c *http.Client, u string, into any, decode func(io.Reader, any) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("imagerepo: %s answered %d", req.URL.Host, resp.StatusCode)
	}
	return decode(io.LimitReader(resp.Body, 4<<20), into)
}
