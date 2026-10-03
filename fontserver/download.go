package fontserver

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// googleFontFile is one weight/style cut of a family fetched from
// Google Fonts' own CDN, ready to be written to disk -- download.go's
// counterpart to Variant (repo.go), before it has a Path of its own on
// this server.
type googleFontFile struct {
	Weight int
	Italic bool
	Data   []byte
}

// fontFaceEntry is one @font-face block parsed out of Google's own CSS
// response (cssFontFaceURL below), before its own URL is fetched --
// split out from googleFontFile so parseCSSFontFaces can be unit
// tested against a plain string, with no network or file I/O of its
// own.
type fontFaceEntry struct {
	Weight int
	Italic bool
	URL    string
}

// cssFontFaceRe matches one @font-face block from Google's *legacy*
// css?family= endpoint (not css2?family=, which never serves TTF at
// all -- see fetchGoogleFontFilesAPI's own doc comment on why TTF is
// what this file needs). Deliberately not a general CSS parser: this
// endpoint's own output is generated, not authored, so its shape
// (three fixed declarations per block, always in this order, no
// comments or unusual whitespace) is stable enough for one regex to
// cover reliably -- confirmed against real responses for several
// families while building this, not just guessed at.
var cssFontFaceRe = regexp.MustCompile(`(?s)@font-face\s*\{\s*font-family:[^;]*;\s*font-style:\s*(\w+)\s*;\s*font-weight:\s*(\d+)\s*;.*?src:\s*url\((https://[^)]+)\)\s*format\('truetype'\)`)

// parseCSSFontFaces extracts every @font-face block's own style/
// weight/URL from css (Google's legacy css?family= response body).
// Empty for a family Google doesn't recognize (that endpoint's own
// response for one is either an HTTP error or a body with no
// @font-face blocks at all, depending on exactly how the name fails
// to match -- either way, nothing here to extract).
func parseCSSFontFaces(css string) []fontFaceEntry {
	matches := cssFontFaceRe.FindAllStringSubmatch(css, -1)
	out := make([]fontFaceEntry, 0, len(matches))
	for _, m := range matches {
		weight, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out = append(out, fontFaceEntry{
			Weight: weight,
			Italic: strings.EqualFold(m[1], "italic"),
			URL:    m[3],
		})
	}
	return out
}

// fetchGoogleFontFilesAPI is fetchGoogleFontFiles' real production
// implementation (set by NewServer; swappable so
// TestLocalDownloadFetchesAndCachesFamily and friends can stub it with
// no real network call, the same shape fetchGoogleFontsAPI's own
// swappable field already establishes for the webfonts API).
//
// Deliberately targets Google's *legacy* "css?family=" endpoint (the
// same one <link>-based Google font loading everywhere else in this
// codebase leaves alone in favor of the modern "css2?family=" one),
// with an old-browser User-Agent: css2 only ever serves woff2, which
// Repo.Scan() doesn't parse at all (its own doc comment explains why:
// this server only unpacks raw sfnt data, not woff2's own compressed
// container), while css' own format negotiation still serves plain
// TrueType to a Cascading Style Sheets Level 1 encoded font it doesn't
// recognize as a modern browser. Confirmed against Google's real API
// while building this (not simulated): a plain
// `curl -A "Mozilla/4.0" "https://fonts.googleapis.com/css?family=Open+Sans:400,700,400italic,700italic"`
// returns exactly the four @font-face blocks, each with a
// format('truetype') src URL that itself resolves to real TTF bytes.
// Requesting all four of 400/700/400italic/700italic up front (not
// just the family's own default weight) means one download call gives
// the picker's own existing bold/italic toolbar toggles something real
// to render with immediately, not just upright regular text.
func fetchGoogleFontFilesAPI(family string) ([]googleFontFile, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	u := "https://fonts.googleapis.com/css?family=" + url.QueryEscape(family) + ":400,700,400italic,700italic"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// Elicits TrueType src URLs -- see this function's own doc comment.
	// Deliberately the bare string, not a fuller legacy-browser UA
	// (e.g. an IE6 string): confirmed against Google's real API while
	// building this that a fuller UA can make some families (Roboto,
	// at least) fall back to a variable-font "kit=" URL with no
	// format('truetype') annotation and no static-weight TTF behind it
	// at all, while this bare string reliably returns a real static
	// TrueType file for every weight requested, for every family
	// tried.
	req.Header.Set("User-Agent", "Mozilla/4.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fontserver: google fonts css endpoint returned %s: %s", resp.Status, body)
	}
	css, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	faces := parseCSSFontFaces(string(css))
	if len(faces) == 0 {
		return nil, fmt.Errorf("fontserver: no such Google Fonts family %q", family)
	}

	out := make([]googleFontFile, 0, len(faces))
	for _, f := range faces {
		fileResp, err := client.Get(f.URL)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(fileResp.Body, 8<<20))
		fileResp.Body.Close()
		if err != nil {
			return nil, err
		}
		if fileResp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fontserver: fetching %s returned %s", f.URL, fileResp.Status)
		}
		out = append(out, googleFontFile{Weight: f.Weight, Italic: f.Italic, Data: data})
	}
	return out, nil
}

// familyNameRe bounds what handleLocalDownload accepts as a family
// name -- letters, digits, spaces, and a handful of punctuation marks
// real family names actually use ("Noto Sans JP", "IBM Plex Mono",
// "M PLUS 1p"), long enough for any real family but short enough that
// nothing pathological reaches either the outbound Google request or
// downloadSlug below. Not a security boundary against Google itself
// (a made-up name just gets "no such family" back), but is one against
// building a nonsensical or oversized local directory name from
// arbitrary client input.
var familyNameRe = regexp.MustCompile(`^[A-Za-z0-9 '.+-]{1,80}$`)

// downloadSlugRe strips everything a family name's own directory (and
// per-variant filename) can't safely contain -- letters/digits only,
// case-folded, so "Open Sans" and "open sans" (however a caller
// capitalizes it) always land in the identical directory rather than
// two parallel ones that both really mean the same family.
var downloadSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func downloadSlug(family string) string {
	slug := downloadSlugRe.ReplaceAllString(strings.ToLower(family), "-")
	return strings.Trim(slug, "-")
}

// handleLocalDownload fetches family (?family=) from Google Fonts
// (s.fetchGoogleFontFiles) into its own subdirectory of the local font
// directory ("google-cache/<slug>/", downloadSlug), re-scans the Repo
// so the new files are immediately servable from /local/* like any
// other locally-hosted font, and responds with the (now updated) full
// family list -- the same response shape POST /local/rescan already
// gives, so a caller that already knows how to handle that response
// needs no special case for this one. A family already downloaded
// once is simply re-fetched and overwritten -- idempotent, no separate
// "already have this" check, since re-downloading either produces the
// identical bytes or (a font family Google has since updated) a
// legitimate refresh, both fine outcomes for a plain overwrite.
func (s *Server) handleLocalDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	family := r.URL.Query().Get("family")
	if family == "" {
		http.Error(w, "missing family parameter", http.StatusBadRequest)
		return
	}
	if !familyNameRe.MatchString(family) {
		http.Error(w, "invalid family parameter", http.StatusBadRequest)
		return
	}

	// A few downloads at a time: each fetches megabytes and rescans the
	// font directory.
	if s.downloads != nil { // a Server built without NewServer has no limit
		select {
		case s.downloads <- struct{}{}:
			defer func() { <-s.downloads }()
		default:
			http.Error(w, "too many downloads in progress", http.StatusTooManyRequests)
			return
		}
	}
	files, err := s.fetchGoogleFontFiles(family)
	if err != nil {
		log.Printf("fontserver: downloading %q: %v", family, err)
		http.Error(w, "could not download that family", http.StatusBadGateway)
		return
	}

	slug := downloadSlug(family)
	if slug == "" {
		http.Error(w, "invalid family parameter", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(s.cfg.FontsDir, "google-cache", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range files {
		name := slug + "-" + strconv.Itoa(f.Weight)
		if f.Italic {
			name += "-italic"
		}
		name += ".ttf"
		// Write beside the final name, then rename: a rescan running at the
		// same time never sees half a font.
		tmp := filepath.Join(dir, "."+name+".part")
		if err := os.WriteFile(tmp, f.Data, 0o644); err != nil {
			log.Printf("fontserver: writing %s: %v", name, err)
			http.Error(w, "could not store the font", http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			log.Printf("fontserver: renaming %s: %v", name, err)
			http.Error(w, "could not store the font", http.StatusInternalServerError)
			return
		}
	}

	if err := s.repo.Scan(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"families": s.repo.Families(), "downloaded": len(files)})
}
