package osmmap

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// A Place is where a map is centred.
type Place struct {
	Name     string
	Lat, Lng float64 // degrees
	Kind     string  // "coordinates", "pluscode" or "address"
}

var reCoords = regexp.MustCompile(`^\s*([+-]?\d{1,3}(?:\.\d+)?)\s*°?\s*([NSns])?\s*[,;\s]\s*([+-]?\d{1,3}(?:\.\d+)?)\s*°?\s*([EWew])?\s*$`)

var ErrNotCoordinates = errors.New("osmmap: not coordinates (like 48.8584, 2.2945)")

const dmsPart = `(\d{1,3})\s*(?:°|º|d|\s)\s*(\d{1,2}(?:\.\d+)?)\s*(?:['′’m]|\s)?\s*(?:(\d{1,2}(?:\.\d+)?)\s*(?:["″”s]|'')?)?\s*`

var reDMS = regexp.MustCompile(`^\s*` + dmsPart + `([NSns])\s*[,;\s]\s*` + dmsPart + `([EWew])\s*$`)

func dms(deg, min, sec, hemi string) float64 {
	d, _ := strconv.ParseFloat(deg, 64)
	m, _ := strconv.ParseFloat(min, 64)
	s, _ := strconv.ParseFloat(sec, 64)
	v := d + m/60 + s/3600
	if strings.EqualFold(hemi, "S") || strings.EqualFold(hemi, "W") {
		v = -v
	}
	return v
}

func ParseCoordinates(s string) (lat, lng float64, err error) {
	if d := reDMS.FindStringSubmatch(s); d != nil {
		if mm, _ := strconv.ParseFloat(d[2], 64); mm >= 60 {
			return 0, 0, errors.New("osmmap: minutes must be less than 60")
		}
		if ss, _ := strconv.ParseFloat(d[3], 64); ss >= 60 {
			return 0, 0, errors.New("osmmap: seconds must be less than 60")
		}
		lat, lng = dms(d[1], d[2], d[3], d[4]), dms(d[5], d[6], d[7], d[8])
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			return 0, 0, errors.New("osmmap: latitude must be between -90 and 90 and longitude between -180 and 180")
		}
		return lat, lng, nil
	}
	m := reCoords.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, ErrNotCoordinates
	}
	lat, _ = strconv.ParseFloat(m[1], 64)
	lng, _ = strconv.ParseFloat(m[3], 64)
	if strings.EqualFold(m[2], "S") {
		lat = -lat
	}
	if strings.EqualFold(m[4], "W") {
		lng = -lng
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, errors.New("osmmap: latitude must be between -90 and 90 and longitude between -180 and 180")
	}
	return lat, lng, nil
}

func Locate(s string) (Place, bool, error) {
	s = strings.TrimSpace(s)
	if lat, lng, err := ParseCoordinates(s); err == nil {
		return Place{Name: s, Lat: lat, Lng: lng, Kind: "coordinates"}, true, nil
	} else if !errors.Is(err, ErrNotCoordinates) {
		return Place{}, false, err // looks like coordinates, but out of range
	}
	if lat, lng, _, _, err := DecodePlusCode(s); err == nil {
		return Place{Name: strings.ToUpper(s), Lat: lat, Lng: lng, Kind: "pluscode"}, true, nil
	}
	return Place{}, false, nil
}
