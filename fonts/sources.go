package fonts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/fontserver"
)

// --- a directory of font files on disk -------------------------------

// LocalRepository is a repository over a directory of .ttf/.otf files:
// the local font repository. It scans the directory (with the same
// sfnt name-table reader the fontserver uses) on first use, and again
// whenever Rescan is called.
type LocalRepository struct {
	name   string
	prefix string
	repo   *fontserver.Repo

	once sync.Once
	err  error
}

// Local returns a repository of the font files under dir. urlPrefix is
// what each file's relative path is appended to for Variant.URL -- e.g.
// "/fonts/" if you serve dir at that path, or fontserver's
// "http://host/local/files/"; leave it empty to give the bare relative
// path.
func Local(dir, urlPrefix string) *LocalRepository {
	return &LocalRepository{name: "Local fonts", prefix: urlPrefix, repo: fontserver.NewRepo(dir)}
}

// Name implements Repository.
func (l *LocalRepository) Name() string { return l.name }

// Rescan rereads the directory.
func (l *LocalRepository) Rescan() error { return l.repo.Scan() }

// Fonts implements Repository.
func (l *LocalRepository) Fonts(context.Context) ([]Font, error) {
	l.once.Do(func() { l.err = l.repo.Scan() })
	if l.err != nil {
		return nil, l.err
	}
	var out []Font
	for _, fam := range l.repo.Families() {
		f := Font{Family: fam.Name, Source: l.name}
		for _, v := range fam.Variants {
			f.Variants = append(f.Variants, Variant{Weight: v.Weight, Italic: v.Italic, Style: v.Style, URL: l.prefix + escapePath(v.Path)})
		}
		sortVariants(f.Variants)
		out = append(out, f)
	}
	return out, nil
}

// --- a gojiberry fontserver ------------------------------------------

// ServerKind picks which of a fontserver's catalogs to read.
type ServerKind int

const (
	// ServerLocal is the fonts in the server's own directory (/local/fonts).
	ServerLocal ServerKind = iota
	// ServerGoogle is the Google Fonts catalog the server proxies
	// (/google/fonts); empty unless the server has an API key.
	ServerGoogle
)

type serverRepo struct {
	base   string
	kind   ServerKind
	client *http.Client
}

// Server returns a repository backed by a fontserver at baseURL (e.g.
// "http://localhost:8091"). A nil client uses http.DefaultClient.
func Server(baseURL string, kind ServerKind, client *http.Client) Repository {
	if client == nil {
		client = http.DefaultClient
	}
	return &serverRepo{base: strings.TrimRight(baseURL, "/"), kind: kind, client: client}
}

func (s *serverRepo) Name() string {
	if s.kind == ServerGoogle {
		return "Google Fonts"
	}
	return "Server fonts"
}

func (s *serverRepo) Fonts(ctx context.Context) ([]Font, error) {
	path := "/local/fonts"
	if s.kind == ServerGoogle {
		path = "/google/fonts"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fontserver %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if s.kind == ServerGoogle {
		return s.decodeGoogle(resp.Body)
	}
	var payload struct {
		Families []fontserver.Family `json:"families"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("fontserver %s: %w", path, err)
	}
	out := make([]Font, 0, len(payload.Families))
	for _, fam := range payload.Families {
		f := Font{Family: fam.Name, Source: s.Name()}
		for _, v := range fam.Variants {
			f.Variants = append(f.Variants, Variant{Weight: v.Weight, Italic: v.Italic, Style: v.Style, URL: s.base + "/local/files/" + escapePath(v.Path)})
		}
		sortVariants(f.Variants)
		out = append(out, f)
	}
	return out, nil
}

func (s *serverRepo) decodeGoogle(r io.Reader) ([]Font, error) {
	var payload struct {
		Families []struct {
			Family   string            `json:"family"`
			Category string            `json:"category"`
			Variants []string          `json:"variants"`
			Files    map[string]string `json:"files"`
		} `json:"families"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return nil, fmt.Errorf("fontserver /google/fonts: %w", err)
	}
	out := make([]Font, 0, len(payload.Families))
	for _, g := range payload.Families {
		f := Font{Family: g.Family, Source: s.Name(), Category: g.Category}
		keys := append([]string(nil), g.Variants...)
		sort.Strings(keys)
		for _, k := range keys {
			w, italic, ok := parseVariantKey(k)
			if !ok {
				continue
			}
			f.Variants = append(f.Variants, Variant{Weight: w, Italic: italic, Style: k, URL: g.Files[k]})
		}
		sortVariants(f.Variants)
		out = append(out, f)
	}
	return out, nil
}
