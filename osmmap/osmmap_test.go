package osmmap

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestPlusCodeFromTheSpecification(t *testing.T) {
	// 7FG49Q00+ is the area 20.35..20.40 N, 2.75..2.80 E (a padded code: six digits).
	lat, lng, ls, gs, err := DecodePlusCode("7FG49Q00+")
	if err != nil || !near(lat, 20.375, 1e-9) || !near(lng, 2.775, 1e-9) || !near(ls, 0.05, 1e-9) || !near(gs, 0.05, 1e-9) {
		t.Fatalf("7FG49Q00+: %v %v %v %v %v", lat, lng, ls, gs, err)
	}
	// 8FVC9G8F+6W: ten digits, about 14 m; it contains the point it was made from.
	lat, lng, ls, gs, err = DecodePlusCode("8fvc9g8f+6w") // lower case is the same
	if err != nil || !near(lat, 47.3655625, 1e-6) || !near(lng, 8.5248125, 1e-6) || !near(ls, 0.000125, 1e-9) {
		t.Fatalf("8FVC9G8F+6W: %v %v %v %v %v", lat, lng, ls, gs, err)
	}
}

func TestPlusCodeRoundTrip(t *testing.T) {
	for _, p := range [][2]float64{{47.365562, 8.524813}, {-33.8568, 151.2153}, {0, 0}, {-89.99, -179.99}, {89.99, 179.99}, {35.6586, 139.7454}} {
		for _, digits := range []int{8, 10, 11, 13, 15} {
			code := EncodePlusCode(p[0], p[1], digits)
			lat, lng, ls, gs, err := DecodePlusCode(code)
			if err != nil {
				t.Fatalf("%v %d: %q: %v", p, digits, code, err)
			}
			if math.Abs(lat-p[0]) > ls/2+1e-9 || math.Abs(lng-p[1]) > gs/2+1e-9 {
				t.Fatalf("%v %d: %q decodes to %v,%v (cell %v x %v)", p, digits, code, lat, lng, ls, gs)
			}
		}
	}
}

func TestWhatIsNotAFullPlusCodeIsRefused(t *testing.T) {
	for _, s := range []string{"", "9G8F+6W", "8FVC9G8F", "8FVC9G8F+6W+", "8FVC9G8F+6", "8FVC9G8F+6U", "ZZVC9G8F+6W", "8FVC00G8+6W", "8FVC9G0+", "Paris"} {
		if _, _, _, _, err := DecodePlusCode(s); err == nil {
			t.Errorf("%q should not be a full Plus Code", s)
		}
	}
}

func TestCoordinatesAndLocate(t *testing.T) {
	for in, want := range map[string][2]float64{
		"48.8584, 2.2945": {48.8584, 2.2945}, "48.8584 2.2945": {48.8584, 2.2945}, "48.8584 N, 2.2945 E": {48.8584, 2.2945},
		"33.8568 S 151.2153 E": {-33.8568, 151.2153}, "-33.8568,151.2153": {-33.8568, 151.2153}, "40.7 N 74.0 W": {40.7, -74.0}, "48.8584°N 2.2945°E": {48.8584, 2.2945},
		`48°51'30.2"N 2°17'40.1"E`: {48.858389, 2.294472}, `48° 51' 30" N, 2° 17' 40" E`: {48.858333, 2.294444},
		"33 51 25 S 151 12 55 E": {-33.856944, 151.215278}, "48°51.503'N 2°17.668'E": {48.858383, 2.294467},
		"40°42'51\"N 74°0'21\"W": {40.714167, -74.005833},
	} {
		lat, lng, err := ParseCoordinates(in)
		if err != nil || !near(lat, want[0], 1e-5) || !near(lng, want[1], 1e-5) {
			t.Errorf("%q: %v %v %v", in, lat, lng, err)
		}
	}
	for _, bad := range []string{`48°61'30"N 2°17'40"E`, `48°51'75"N 2°17'40"E`, `95°0'0"N 2°17'40"E`} {
		if _, _, err := ParseCoordinates(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, _, err := ParseCoordinates("91, 2"); err == nil {
		t.Error("latitude out of range")
	}
	if p, ok, err := Locate("48.8584, 2.2945"); !ok || err != nil || p.Kind != "coordinates" {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	if p, ok, err := Locate("7fg49q00+"); !ok || err != nil || p.Kind != "pluscode" || p.Name != "7FG49Q00+" {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	if _, ok, err := Locate("10 Downing Street, London"); ok || err != nil {
		t.Fatalf("an address is for the geocoder: %v %v", ok, err)
	}
	if _, _, err := Locate("95.0, 10.0"); err == nil {
		t.Fatal("looks like coordinates but out of range")
	}
}

func TestNominatim(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery + "|" + r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`[{"lat":"48.8584","lon":"2.2945","display_name":"Eiffel Tower,  Paris"},{"lat":"x","lon":"1","display_name":"bad"},{"lat":"95","lon":"1","display_name":"far"}]`))
	}))
	defer srv.Close()
	places, err := Nominatim{Base: srv.URL, UserAgent: "test"}.Search(context.Background(), "Eiffel Tower")
	if err != nil || len(places) != 1 || places[0].Name != "Eiffel Tower, Paris" || !near(places[0].Lat, 48.8584, 1e-9) || places[0].Kind != "address" {
		t.Fatalf("%+v %v", places, err)
	}
	if !strings.Contains(asked, "q=Eiffel+Tower") || !strings.Contains(asked, "format=jsonv2") || !strings.HasSuffix(asked, "|test") {
		t.Fatalf("asked %q", asked)
	}
	if _, err := (Nominatim{Base: srv.URL}).Search(context.Background(), "  "); err == nil {
		t.Fatal("no address")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusTooManyRequests) }))
	defer bad.Close()
	if _, err := (Nominatim{Base: bad.URL}).Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("%v", err)
	}
}

type countTiles struct{ n atomic.Int32 }

func (c *countTiles) Tile(_ context.Context, z, x, y int) (image.Image, error) {
	c.n.Add(1)
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	col := color.RGBA{uint8(x * 40), uint8(y * 40), uint8(z * 10), 0xff}
	for i := range img.Pix {
		img.Pix[i] = []uint8{col.R, col.G, col.B, col.A}[i%4]
	}
	return img, nil
}

func TestRenderPlacesTilesAndAMarker(t *testing.T) {
	src := &countTiles{}
	data, err := Render(context.Background(), src, 47.3655625, 8.5248125, 15, 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 600 || img.Bounds().Dy() != 400 {
		t.Fatalf("%v %v", img, err)
	}
	if got := src.n.Load(); got < 6 || got > MaxTiles {
		t.Fatalf("tiles asked: %d", got)
	}
	if r, g, b, _ := img.At(300, 200).RGBA(); r>>8 != 0xd9 || g>>8 != 0x1e || b>>8 != 0x18 {
		t.Fatalf("the marker is at the centre: %v %v %v", r>>8, g>>8, b>>8)
	}
	if _, err := Render(context.Background(), src, 0, 0, 25, 600, 400); err == nil {
		t.Fatal("zoom out of range")
	}
	if _, err := Render(context.Background(), src, 0, 0, 5, 5000, 400); err == nil {
		t.Fatal("size out of range")
	}
}

func TestTheMapRepeatsSidewaysAndEndsAtThePoles(t *testing.T) {
	src := &countTiles{}
	if _, err := Render(context.Background(), src, 0, 179.9999, 3, 600, 400); err != nil { // over the date line
		t.Fatal(err)
	}
	if _, err := Render(context.Background(), src, 85, 0, 2, 600, 400); err != nil { // above the top tile row: left grey
		t.Fatal(err)
	}
}

func TestTilesServer(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		img := image.NewRGBA(image.Rect(0, 0, 256, 256))
		_ = png.Encode(w, img)
	}))
	defer srv.Close()
	img, err := Tiles{URL: srv.URL + "/t/{z}/{x}/{y}.png"}.Tile(context.Background(), 5, 3, 7)
	if err != nil || img.Bounds().Dx() != 256 || path != "/t/5/3/7.png" {
		t.Fatalf("%v %v %q", img, err, path)
	}
	notPNG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer notPNG.Close()
	if _, err := (Tiles{URL: notPNG.URL + "/{z}/{x}/{y}"}).Tile(context.Background(), 1, 0, 0); err == nil {
		t.Fatal("not a picture")
	}
}

// A short code recovered near the place it was cut from is the code it was cut from, from 4 or 6 digits cut, and a far
// reference gives a different place.
func TestShortPlusCodesAreRecoveredNearestTheReference(t *testing.T) {
	// A code recovered near the place it was cut from is the code it was cut from.
	for _, p := range [][2]float64{{48.8584, 2.2945}, {-33.8568, 151.2153}, {40.7128, -74.006}, {-0.1, 179.99}} {
		full := EncodePlusCode(p[0], p[1], 10)
		short := full[4:]
		got, err := RecoverNearest(short, p[0]+0.05, p[1]-0.05)
		if err != nil || got != full {
			t.Errorf("%s from %v: %s %v", short, p, got, err)
		}
		short6 := full[6:]
		if got, err := RecoverNearest(short6, p[0]+0.0005, p[1]); err != nil || got != full {
			t.Errorf("%s from %v: %s %v", short6, p, got, err)
		}
	}
	full := EncodePlusCode(48.8584, 2.2945, 10)
	if far, err := RecoverNearest(full[4:], -40, 100); err != nil || far == full {
		t.Errorf("a reference far away gives another place: %s %v", far, err)
	}
	for _, bad := range []string{"849V9G8F+6W", "9G8F6W", "9G8F+6", "ABCD+EF"} {
		if _, err := RecoverNearest(bad, 0, 0); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseShortCode(t *testing.T) {
	if c, l, ok := ParseShortCode("cwc8+r9 Mountain View, California"); !ok || c != "CWC8+R9" || l != "Mountain View, California" {
		t.Errorf("%q %q %v", c, l, ok)
	}
	if c, l, ok := ParseShortCode("9G8F+6W"); !ok || c != "9G8F+6W" || l != "" {
		t.Errorf("%q %q %v", c, l, ok)
	}
	for _, no := range []string{"8FVC9G8F+6W", "10 Downing Street", "48.8584, 2.2945", "9G8F+6"} {
		if _, _, ok := ParseShortCode(no); ok {
			t.Errorf("%q is not a short code", no)
		}
	}
}

type fixedGeocoder struct{ places []Place }

func (f fixedGeocoder) Search(context.Context, string) ([]Place, error) { return f.places, nil }

func TestLocateShortUsesThePlaceNamed(t *testing.T) {
	full := EncodePlusCode(48.8584, 2.2945, 10)
	geo := fixedGeocoder{[]Place{{Name: "Paris", Lat: 48.86, Lng: 2.35, Kind: "address"}}}
	p, ok, err := LocateShort(context.Background(), geo, full[4:]+" Paris")
	if !ok || err != nil || p.Name != full || p.Kind != "pluscode" || !near(p.Lat, 48.8584, 1e-3) {
		t.Fatalf("%+v %v %v", p, ok, err)
	}
	if _, ok, _ := LocateShort(context.Background(), geo, full[4:]); ok {
		t.Error("with no place named there is nothing to read the code near")
	}
	if _, ok, err := LocateShort(context.Background(), fixedGeocoder{}, full[4:]+" Nowhere"); !ok || err == nil {
		t.Error("an unknown place is an error")
	}
}

type slowTiles struct{ n int }

func (c *slowTiles) Tile(_ context.Context, z, x, y int) (image.Image, error) {
	c.n++
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func TestPoliteTilesCacheAndSpaceTheRequests(t *testing.T) {
	src := &slowTiles{}
	p := &PoliteTiles{Source: src, MaxTiles: 2, Gap: time.Second}
	var napped []time.Duration
	clock := time.Unix(1000, 0)
	p.init()
	p.gate.now = func() time.Time { return clock }
	p.gate.nap = func(_ context.Context, d time.Duration) error {
		napped = append(napped, d)
		clock = clock.Add(d)
		return nil
	}
	ctx := context.Background()
	for _, k := range [][3]int{{3, 1, 1}, {3, 1, 1}, {3, 2, 1}, {3, 3, 1}, {3, 1, 1}} { // the first is asked again once, then pushed out
		if _, err := p.Tile(ctx, k[0], k[1], k[2]); err != nil {
			t.Fatal(err)
		}
	}
	if src.n != 4 {
		t.Errorf("%d requests: the repeat is served from the cache, the evicted one is asked again", src.n)
	}
	if len(napped) != 3 || napped[0] != time.Second || napped[1] != time.Second {
		t.Errorf("the requests were not a second apart: %v", napped)
	}
}

type countGeo struct{ n int }

func (c *countGeo) Search(context.Context, string) ([]Place, error) {
	c.n++
	return []Place{{Name: "x"}}, nil
}

func TestPoliteGeocoderCachesByTheQueryWithoutCaseOrSpaces(t *testing.T) {
	src := &countGeo{}
	p := &PoliteGeocoder{Source: src}
	for _, q := range []string{"Eiffel Tower", "eiffel   tower", " EIFFEL TOWER "} {
		if places, err := p.Search(context.Background(), q); err != nil || len(places) != 1 {
			t.Fatal(err)
		}
	}
	if src.n != 1 {
		t.Errorf("%d requests", src.n)
	}
	if _, err := p.Search(context.Background(), "Louvre"); err != nil || src.n != 2 {
		t.Errorf("another query asks again: %d", src.n)
	}
}
