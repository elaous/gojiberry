package osmmap

import (
	"context"
	"errors"
	"math"
	"strings"
)

var ErrNotShortCode = errors.New("osmmap: not a short Plus Code (like 9G8F+6W)")

func ParseShortCode(s string) (code, locality string, ok bool) {
	s = strings.TrimSpace(s)
	first, rest, _ := strings.Cut(s, " ")
	first = strings.ToUpper(first)
	sep := strings.IndexRune(first, plusSep)
	if sep != 2 && sep != 4 && sep != 6 || strings.Count(first, string(plusSep)) != 1 || len(first)-sep-1 < 2 {
		return "", "", false
	}
	for _, r := range strings.Replace(first, string(plusSep), "", 1) {
		if !strings.ContainsRune(plusAlphabet, r) {
			return "", "", false
		}
	}
	return first, strings.Trim(strings.TrimSpace(rest), ","), true
}

func RecoverNearest(short string, refLat, refLng float64) (string, error) {
	short = strings.ToUpper(strings.TrimSpace(short))
	sep := strings.IndexRune(short, plusSep)
	if sep != 2 && sep != 4 && sep != 6 || strings.Count(short, string(plusSep)) != 1 {
		return "", ErrNotShortCode
	}
	padding := 8 - sep
	resolution := math.Pow(20, float64(2-padding/2))
	toEdge := resolution / 2
	refLat = math.Min(math.Max(refLat, -90), 90)
	refLng = math.Mod(refLng+180, 360)
	if refLng < 0 {
		refLng += 360
	}
	refLng -= 180
	prefix := EncodePlusCode(refLat, refLng, 10)[:padding]
	lat, lng, _, _, err := DecodePlusCode(prefix + short)
	if err != nil {
		return "", ErrNotShortCode
	}
	if refLat+toEdge < lat && lat-resolution >= -90 {
		lat -= resolution
	} else if refLat-toEdge > lat && lat+resolution <= 90 {
		lat += resolution
	}
	if refLng+toEdge < lng {
		lng -= resolution
	} else if refLng-toEdge > lng {
		lng += resolution
	}
	return EncodePlusCode(lat, lng, len(strings.Replace(prefix+short, string(plusSep), "", 1))), nil
}

func LocateShort(ctx context.Context, geo Geocoder, s string) (Place, bool, error) {
	code, locality, ok := ParseShortCode(s)
	if !ok || locality == "" {
		return Place{}, false, nil
	}
	places, err := geo.Search(ctx, locality)
	if err != nil {
		return Place{}, true, err
	}
	if len(places) == 0 {
		return Place{}, true, errors.New("osmmap: the place named with the code was not found")
	}
	full, err := RecoverNearest(code, places[0].Lat, places[0].Lng)
	if err != nil {
		return Place{}, true, err
	}
	lat, lng, _, _, err := DecodePlusCode(full)
	if err != nil {
		return Place{}, true, err
	}
	return Place{Name: full, Lat: lat, Lng: lng, Kind: "pluscode"}, true, nil
}
