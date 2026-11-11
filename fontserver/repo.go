package fontserver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Variant struct {
	Weight int    `json:"weight"` // CSS-style 100-900; see classifySubfamily
	Italic bool   `json:"italic"`
	Style  string `json:"style"` // the font's own subfamily name, e.g. "Bold Italic", for display
	Path   string `json:"path"`
}

// Family is every scanned Variant sharing one sfnt family name.
type Family struct {
	Name     string    `json:"name"`
	Variants []Variant `json:"variants"`
}

type Repo struct {
	dir string

	mu       sync.RWMutex
	families map[string]*Family // keyed by Family.Name
	paths    map[string]bool
}

// NewRepo creates a Repo rooted at dir.
func NewRepo(dir string) *Repo {
	return &Repo{dir: dir, families: map[string]*Family{}, paths: map[string]bool{}}
}

func (r *Repo) Scan() error {
	families := map[string]*Family{}
	paths := map[string]bool{}

	err := filepath.WalkDir(r.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".ttf" && ext != ".otf" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		family, subfamily, err := sfntNames(data)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(r.dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		weight, italic := classifySubfamily(subfamily)

		f, ok := families[family]
		if !ok {
			f = &Family{Name: family}
			families[family] = f
		}
		f.Variants = append(f.Variants, Variant{Weight: weight, Italic: italic, Style: subfamily, Path: rel})
		paths[rel] = true
		return nil
	})
	if err != nil {
		return err
	}

	for _, f := range families {
		sort.Slice(f.Variants, func(i, j int) bool {
			if f.Variants[i].Weight != f.Variants[j].Weight {
				return f.Variants[i].Weight < f.Variants[j].Weight
			}
			return !f.Variants[i].Italic && f.Variants[j].Italic
		})
	}

	r.mu.Lock()
	r.families = families
	r.paths = paths
	r.mu.Unlock()
	return nil
}

func (r *Repo) Families() []Family {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Family, 0, len(r.families))
	for _, f := range r.families {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Repo) VariantPath(family string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.families[family]
	if !ok || len(f.Variants) == 0 {
		return "", false
	}
	return f.Variants[0].Path, true
}

// only paths Scan produced: traversal check
func (r *Repo) Open(rel string) ([]byte, bool) {
	r.mu.RLock()
	ok := r.paths[rel]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	return data, true
}

func classifySubfamily(subfamily string) (weight int, italic bool) {
	s := strings.ToLower(subfamily)
	italic = strings.Contains(s, "italic") || strings.Contains(s, "oblique")
	switch {
	case strings.Contains(s, "thin"):
		weight = 100
	case strings.Contains(s, "extralight"), strings.Contains(s, "ultralight"):
		weight = 200
	case strings.Contains(s, "light"):
		weight = 300
	case strings.Contains(s, "medium"):
		weight = 500
	case strings.Contains(s, "semibold"), strings.Contains(s, "demibold"):
		weight = 600
	case strings.Contains(s, "extrabold"), strings.Contains(s, "ultrabold"):
		weight = 800
	case strings.Contains(s, "black"), strings.Contains(s, "heavy"):
		weight = 900
	case strings.Contains(s, "bold"):
		weight = 700
	default:
		weight = 400
	}
	return weight, italic
}
