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
	} {
		lat, lng, err := ParseCoordinates(in)
		if err != nil || !near(lat, want[0], 1e-9) || !near(lng, want[1], 1e-9) {
			t.Errorf("%q: %v %v %v", in, lat, lng, err)
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
