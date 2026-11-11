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

type googleFontFile struct {
	Weight int
	Italic bool
	Data   []byte
}

type fontFaceEntry struct {
	Weight int
	Italic bool
	URL    string
}

var cssFontFaceRe = regexp.MustCompile(`(?s)@font-face\s*\{\s*font-family:[^;]*;\s*font-style:\s*(\w+)\s*;\s*font-weight:\s*(\d+)\s*;.*?src:\s*url\((https://[^)]+)\)\s*format\('truetype'\)`)

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

func fetchGoogleFontFilesAPI(family string) ([]googleFontFile, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	u := "https://fonts.googleapis.com/css?family=" + url.QueryEscape(family) + ":400,700,400italic,700italic"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	// bare UA string: elicits TrueType src URLs from the legacy endpoint
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

var familyNameRe = regexp.MustCompile(`^[A-Za-z0-9 '.+-]{1,80}$`)

var downloadSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func downloadSlug(family string) string {
	slug := downloadSlugRe.ReplaceAllString(strings.ToLower(family), "-")
	return strings.Trim(slug, "-")
}

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

	// a few downloads at a time: each fetches megabytes and rescans
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
		// beside the final name, then rename: no half font on rescan
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
