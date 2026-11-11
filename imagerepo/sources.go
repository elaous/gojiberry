package imagerepo

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func decodeJSON(r io.Reader, into any) error { return json.NewDecoder(r).Decode(into) }

type Openverse struct {
	Base   string // default https://api.openverse.org
	Label  string // the name shown; default "Openverse"
	Client *http.Client
}

// Name implements Repository.
func (o Openverse) Name() string {
	if o.Label != "" {
		return o.Label
	}
	return "Openverse"
}

// Search implements Repository.
func (o Openverse) Search(ctx context.Context, q string, page int) ([]Result, error) {
	base := strings.TrimRight(o.Base, "/")
	if base == "" {
		base = "https://api.openverse.org"
	}
	v := url.Values{"q": {q}, "page": {strconv.Itoa(page)}, "page_size": {"12"}, "license": {"cc0,pdm,by,by-sa"}}
	var out struct {
		Results []struct {
			ID             string
			Title          string
			Creator        string
			License        string
			LicenseVersion string `json:"license_version"`
			LicenseURL     string `json:"license_url"`
			Width          int
			Height         int
			Foreign        string `json:"foreign_landing_url"`
		} `json:"results"`
	}
	if err := getJSON(ctx, o.Client, base+"/v1/images/?"+v.Encode(), &out, decodeJSON); err != nil {
		return nil, err
	}
	var res []Result
	for _, r := range out.Results {
		if r.ID == "" {
			continue
		}
		res = append(res, Result{
			Repo: o.Name(), ID: r.ID, Title: r.Title, Creator: r.Creator,
			License: licenseName(r.License, r.LicenseVersion), LicenseURL: r.LicenseURL, Source: r.Foreign,
			Thumb: base + "/v1/images/" + r.ID + "/thumb/", URL: base + "/v1/images/" + r.ID + "/thumb/", W: r.Width, H: r.Height,
		})
	}
	return res, nil
}

func licenseName(code, version string) string {
	switch strings.ToLower(code) {
	case "cc0":
		return "CC0"
	case "pdm":
		return "Public domain"
	case "":
		return ""
	}
	s := "CC " + strings.ToUpper(code)
	if version != "" {
		s += " " + version
	}
	return s
}

type Commons struct {
	Base   string
	Label  string // the name shown; default "Wikimedia Commons"
	Client *http.Client
}

// Name implements Repository.
func (c Commons) Name() string {
	if c.Label != "" {
		return c.Label
	}
	return "Wikimedia Commons"
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func plain(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

// Search implements Repository.
func (c Commons) Search(ctx context.Context, q string, page int) ([]Result, error) {
	base := strings.TrimRight(c.Base, "/")
	if base == "" {
		base = "https://commons.wikimedia.org"
	}
	v := url.Values{
		"action": {"query"}, "format": {"json"}, "origin": {"*"}, "generator": {"search"}, "gsrnamespace": {"6"},
		"gsrsearch": {q + " filetype:bitmap"}, "gsrlimit": {"12"}, "gsroffset": {strconv.Itoa((page - 1) * 12)},
		"prop": {"imageinfo"}, "iiprop": {"url|size|mime|extmetadata"}, "iiurlwidth": {"1000"},
	}
	var out struct {
		Query struct {
			Pages map[string]struct {
				Index     int
				Title     string
				ImageInfo []struct {
					ThumbURL       string `json:"thumburl"`
					URL            string `json:"url"`
					DescriptionURL string `json:"descriptionurl"`
					Mime           string `json:"mime"`
					Width, Height  int
					Meta           map[string]struct{ Value string } `json:"extmetadata"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := getJSON(ctx, c.Client, base+"/w/api.php?"+v.Encode(), &out, decodeJSON); err != nil {
		return nil, err
	}
	type idx struct {
		i int
		r Result
	}
	var found []idx
	for id, p := range out.Query.Pages {
		if len(p.ImageInfo) == 0 {
			continue
		}
		ii := p.ImageInfo[0]
		short := ii.Meta["LicenseShortName"].Value
		if !reusable(short) || !accepted[ii.Mime] {
			continue
		}
		pic := ii.ThumbURL
		if pic == "" {
			pic = ii.URL
		}
		title := strings.TrimPrefix(p.Title, "File:")
		if i := strings.LastIndexByte(title, '.'); i > 0 && len(title)-i <= 5 {
			title = title[:i]
		}
		found = append(found, idx{p.Index, Result{
			Repo: c.Name(), ID: id, Title: title, Creator: plain(ii.Meta["Artist"].Value), License: plain(short),
			LicenseURL: ii.Meta["LicenseUrl"].Value, Source: ii.DescriptionURL, Thumb: pic, URL: pic, W: ii.Width, H: ii.Height,
		}})
	}
	for i := 1; i < len(found); i++ { // insertion sort by search rank, the map has no order
		for j := i; j > 0 && found[j].i < found[j-1].i; j-- {
			found[j], found[j-1] = found[j-1], found[j]
		}
	}
	res := make([]Result, len(found))
	for i, f := range found {
		res[i] = f.r
	}
	return res, nil
}

func reusable(short string) bool {
	s := strings.ToLower(strings.TrimSpace(short))
	switch {
	case s == "":
		return false
	case strings.Contains(s, "-nc") || strings.Contains(s, "-nd"):
		return false
	case strings.HasPrefix(s, "public domain"), strings.HasPrefix(s, "pd"), strings.HasPrefix(s, "cc0"), strings.HasPrefix(s, "cc by"):
		return true
	}
	return false
}
