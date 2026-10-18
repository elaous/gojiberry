// Package osmmap finds a place and draws a map of it from OpenStreetMap: a place is named by an address (looked up with
// Nominatim), by coordinates, or by a Plus Code (an Open Location Code, an open specification, so the code is read here
// with no request at all). The map is a picture made of the OpenStreetMap tiles around the place, with a marker on it; the
// credit "© OpenStreetMap contributors" goes with it (the data is under the ODbL).
//
// The public Nominatim and tile servers are for light use (one request a second, no bulk, a way to tell who is asking:
// usage policies of the OpenStreetMap Foundation). This package asks for one search and at most a dozen tiles when a
// person inserts a map; a deployment with more use points Nominatim and Tiles at its own servers.
package osmmap

import (
	"errors"
	"math"
	"strings"
)

const (
	plusAlphabet = "23456789CFGHJMPQRVWX"
	plusSep      = '+'
	plusPad      = '0'
	plusMaxDigit = 15
)

// ErrNotPlusCode is returned for text that is not a full Plus Code.
var ErrNotPlusCode = errors.New("osmmap: not a full Plus Code (like 8FVC9G8F+6W)")

// DecodePlusCode returns the centre of the area a full Plus Code names and how big the area is (degrees of latitude and
// of longitude). A short code (one that needs a nearby place to be understood, like 9G8F+6W) is not a full code.
func DecodePlusCode(code string) (lat, lng, latSize, lngSize float64, err error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	sep := strings.IndexRune(code, plusSep)
	if sep != 8 || strings.Count(code, string(plusSep)) != 1 {
		return 0, 0, 0, 0, ErrNotPlusCode
	}
	digits := strings.Replace(code, string(plusSep), "", 1)
	if i := strings.IndexRune(digits, plusPad); i >= 0 { // padded: the digits stop where the zeros begin
		if strings.Trim(digits[i:], string(plusPad)) != "" || i%2 != 0 || code[len(code)-1] != plusSep {
			return 0, 0, 0, 0, ErrNotPlusCode
		}
		digits = digits[:i]
	}
	if len(digits) < 2 || len(digits) > plusMaxDigit {
		return 0, 0, 0, 0, ErrNotPlusCode
	}
	idx := make([]int, len(digits))
	for i, r := range digits {
		k := strings.IndexRune(plusAlphabet, r)
		if k < 0 {
			return 0, 0, 0, 0, ErrNotPlusCode
		}
		idx[i] = k
	}
	if len(idx)%2 == 1 && len(idx) <= 10 {
		return 0, 0, 0, 0, ErrNotPlusCode // the pairs come whole
	}
	if idx[0] > 8 || (len(idx) > 1 && idx[1] > 17) { // the first pair is a latitude below 90 and a longitude below 180
		return 0, 0, 0, 0, ErrNotPlusCode
	}
	lat, lng = -90, -180
	latRes, lngRes := 20.0, 20.0
	for i := 0; i < len(idx) && i < 10; i += 2 {
		lat += float64(idx[i]) * latRes
		latSize = latRes
		if i+1 < len(idx) {
			lng += float64(idx[i+1]) * lngRes
			lngSize = lngRes
		}
		latRes /= 20
		lngRes /= 20
	}
	latRes, lngRes = latSize, lngSize
	for i := 10; i < len(idx); i++ { // the digits after the tenth cut the area into 4 rows and 5 columns
		latRes /= 5
		lngRes /= 4
		lat += float64(idx[i]/4) * latRes
		lng += float64(idx[i]%4) * lngRes
		latSize, lngSize = latRes, lngRes
	}
	return lat + latSize/2, lng + lngSize/2, latSize, lngSize, nil
}

// EncodePlusCode is the Plus Code of a place with the given number of digits (10 is about 14 metres square; 8 to 15, and
// the 10 digits and more cut into rows and columns as the specification says).
func EncodePlusCode(lat, lng float64, digits int) string {
	digits = min(max(digits, 8), plusMaxDigit)
	if digits == 9 { // the pairs come whole
		digits = 10
	}
	lat = math.Min(math.Max(lat, -90), 90)
	if lat == 90 { // the pole belongs to the cell below it
		lat -= 1e-9
	}
	lng = math.Mod(lng+180, 360)
	if lng < 0 {
		lng += 360
	}
	lng -= 180
	lat += 90
	lng += 180
	var out []byte
	latRes, lngRes := 20.0, 20.0
	for i := 0; i < 10; i += 2 {
		la := int(math.Floor(lat / latRes))
		lo := int(math.Floor(lng / lngRes))
		lat -= float64(la) * latRes
		lng -= float64(lo) * lngRes
		if i < digits {
			out = append(out, plusAlphabet[min(la, 19)])
		}
		if i+1 < digits {
			out = append(out, plusAlphabet[min(lo, 19)])
		}
		if i == 6 {
			out = append(out, byte(plusSep))
		}
		latRes /= 20
		lngRes /= 20
	}
	latRes, lngRes = 20.0/math.Pow(20, 4), 20.0/math.Pow(20, 4)
	for i := 10; i < digits; i++ {
		latRes /= 5
		lngRes /= 4
		row := int(math.Floor(lat / latRes))
		col := int(math.Floor(lng / lngRes))
		lat -= float64(row) * latRes
		lng -= float64(col) * lngRes
		out = append(out, plusAlphabet[min(row*4+col, 19)])
	}
	return string(out)
}
