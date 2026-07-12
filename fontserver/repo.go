package fontserver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Variant is one weight/style cut of a Family, backed by a single font
// file on disk. Path is relative to the Repo's root directory (never
// absolute -- see Repo.Open, which is the only place a Variant's Path
// is turned back into bytes, and which rejects anything that doesn't
// match a path this same scan produced).
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

// Repo is a locally-run "font CDN": point it at a directory of
// .ttf/.otf files and it scans them once, groups them by family, and
// caches the result in memory -- the same read-and-cache role a real
// CDN's origin-fetch-then-cache plays, just for a folder on disk
// instead of a network origin. Scan again (Repo.Scan) whenever the
// directory's contents change; nothing here watches the filesystem on
// its own.
type Repo struct {
	dir string

	mu       sync.RWMutex
	families map[string]*Family // keyed by Family.Name
	paths    map[string]bool    // every Variant.Path this Repo has ever produced, for Open's traversal check
}

// NewRepo creates a Repo rooted at dir. The directory need not exist
// yet -- Scan reports that as zero families rather than an error, the
// same "nothing here yet" shape ConfigFromEnv's other defaults use.
func NewRepo(dir string) *Repo {
	return &Repo{dir: dir, families: map[string]*Family{}, paths: map[string]bool{}}
}

// Scan walks the Repo's directory for .ttf/.otf files (woff/woff2
// wrap this same sfnt data in a compressed container we don't unpack,
// so they're skipped, not errored -- a mixed directory still serves
// everything it can read), parses each one's name table, and replaces
// the cached family list atomically. A file that fails to parse (not
// really an sfnt font, or a 'name' table we can't make sense of) is
// skipped and does not fail the whole scan, the same tolerant
// posture the toolbar's custom-page-size dialog takes toward bad
// input.
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

// Families returns every scanned family, sorted by name for a stable
// response the font picker can render straight through.
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

// Open returns the raw bytes of the font file at rel, which must be
// exactly one of the Paths Scan last produced -- not merely a path
// that resolves inside r.dir. This is what stops a request like
// /local/files/..%2f..%2f..%2fetc%2fpasswd from reading anything
// outside the scanned font set, rather than relying on path-cleaning
// alone to catch every traversal trick.
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

// classifySubfamily derives a CSS-style numeric weight (100-900) and
// an italic flag from an sfnt subfamily name like "Bold Italic" or
// "Semibold" -- fonts don't carry a structured weight field anywhere
// as reliable as this name (OS/2.usWeightClass exists but is
// routinely wrong or absent in the wild), and this is the same
// name-driven convention Google Fonts' own "700italic"-style variant
// keys use.
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
