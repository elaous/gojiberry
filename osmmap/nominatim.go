package osmmap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A Geocoder finds the places an address names.
type Geocoder interface {
	Search(ctx context.Context, query string) ([]Place, error)
}

// Nominatim asks a Nominatim server (the public one by default, which is for light use; see the package doc).
type Nominatim struct {
	Base      string       // "" is https://nominatim.openstreetmap.org; a relative address is read as it is (a mirror, a test)
	Client    *http.Client // nil is http.DefaultClient
	UserAgent string       // sent as User-Agent; a browser does not let it change, and sends the page's address as the referrer
}

const maxAnswer = 1 << 20

// Search returns up to five places for the address.
func (n Nominatim) Search(ctx context.Context, query string) ([]Place, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 300 {
		return nil, errors.New("osmmap: give an address of up to 300 characters")
	}
	base := strings.TrimSuffix(n.Base, "/")
	if base == "" {
		base = "https://nominatim.openstreetmap.org"
	}
	q := url.Values{"q": {query}, "format": {"jsonv2"}, "limit": {"5"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if n.UserAgent != "" {
		req.Header.Set("User-Agent", n.UserAgent)
	}
	req.Header.Set("Accept", "application/json")
	c := n.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osmmap: the geocoder answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, errors.New("osmmap: the geocoder's answer could not be read")
	}
	var out []Place
	for _, r := range raw {
		lat, e1 := strconv.ParseFloat(r.Lat, 64)
		lng, e2 := strconv.ParseFloat(r.Lon, 64)
		if e1 != nil || e2 != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			continue
		}
		name := strings.Join(strings.Fields(r.DisplayName), " ")
		if len(name) > 300 {
			name = name[:300]
		}
		out = append(out, Place{Name: name, Lat: lat, Lng: lng, Kind: "address"})
	}
	return out, nil
}
