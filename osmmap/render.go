package osmmap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"strings"
)

// Credit is the credit that goes with every map (the data is under the Open Database Licence).
const Credit = "Map data © OpenStreetMap contributors (ODbL)"

const (
	tileSize = 256
	// MaxTiles is how many tiles one map may ask for.
	MaxTiles = 16
	// MinZoom and MaxZoom bound the zoom (0 is the whole world in one tile, 19 is a building).
	MinZoom, MaxZoom = 1, 19
	maxTileBytes     = 1 << 20
)

// A TileSource gives the picture of one tile.
type TileSource interface {
	Tile(ctx context.Context, z, x, y int) (image.Image, error)
}

// Tiles reads tiles from a server by an address with {z}, {x} and {y} in it (the public OpenStreetMap server by default,
// which is for light use; see the package doc).
type Tiles struct {
	URL       string // "" is https://tile.openstreetmap.org/{z}/{x}/{y}.png
	Client    *http.Client
	UserAgent string
}

// Tile implements TileSource.
func (t Tiles) Tile(ctx context.Context, z, x, y int) (image.Image, error) {
	tmpl := t.URL
	if tmpl == "" {
		tmpl = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"
	}
	addr := strings.NewReplacer("{z}", fmt.Sprint(z), "{x}", fmt.Sprint(x), "{y}", fmt.Sprint(y)).Replace(tmpl)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, err
	}
	if t.UserAgent != "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}
	c := t.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osmmap: the tile server answered %d", resp.StatusCode)
	}
	img, err := png.Decode(io.LimitReader(resp.Body, maxTileBytes))
	if err != nil {
		return nil, errors.New("osmmap: a tile was not a PNG picture")
	}
	return img, nil
}

// worldPixel is where a place is on the map of the whole world at the zoom, in pixels from its top left corner.
func worldPixel(lat, lng float64, zoom int) (x, y float64) {
	lat = math.Max(math.Min(lat, 85.0511), -85.0511) // the map ends there
	size := float64(tileSize) * math.Pow(2, float64(zoom))
	rad := lat * math.Pi / 180
	x = (lng + 180) / 360 * size
	y = (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) / 2 * size
	return x, y
}

// Render draws the map of w x h pixels centred on the place at the zoom, with a marker on the place, and returns it as a PNG.
func Render(ctx context.Context, src TileSource, lat, lng float64, zoom, w, h int) ([]byte, error) {
	if zoom < MinZoom || zoom > MaxZoom {
		return nil, fmt.Errorf("osmmap: the zoom must be between %d and %d", MinZoom, MaxZoom)
	}
	if w < 64 || h < 64 || w > 1024 || h > 1024 {
		return nil, errors.New("osmmap: the map must be between 64 and 1024 pixels on a side")
	}
	cx, cy := worldPixel(lat, lng, zoom)
	left, top := cx-float64(w)/2, cy-float64(h)/2
	x0, x1 := int(math.Floor(left/tileSize)), int(math.Floor((left+float64(w)-1)/tileSize))
	y0, y1 := int(math.Floor(top/tileSize)), int(math.Floor((top+float64(h)-1)/tileSize))
	if (x1-x0+1)*(y1-y0+1) > MaxTiles {
		return nil, errors.New("osmmap: that map needs too many tiles")
	}
	n := 1 << zoom
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0xdd, 0xdd, 0xdd, 0xff}}, image.Point{}, draw.Src) // where there is no tile
	for ty := y0; ty <= y1; ty++ {
		if ty < 0 || ty >= n {
			continue // above or below the map
		}
		for tx := x0; tx <= x1; tx++ {
			tile, err := src.Tile(ctx, zoom, ((tx%n)+n)%n, ty) // the map repeats sideways
			if err != nil {
				return nil, err
			}
			dx, dy := int(math.Round(float64(tx*tileSize)-left)), int(math.Round(float64(ty*tileSize)-top))
			draw.Draw(img, image.Rect(dx, dy, dx+tileSize, dy+tileSize), tile, tile.Bounds().Min, draw.Src)
		}
	}
	marker(img, w/2, h/2)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// marker draws a red dot with a white ring.
func marker(img *image.RGBA, cx, cy int) {
	const outer, inner = 9, 6
	for y := -outer; y <= outer; y++ {
		for x := -outer; x <= outer; x++ {
			d := x*x + y*y
			switch {
			case d <= inner*inner:
				img.SetRGBA(cx+x, cy+y, color.RGBA{0xd9, 0x1e, 0x18, 0xff})
			case d <= outer*outer:
				img.SetRGBA(cx+x, cy+y, color.RGBA{0xff, 0xff, 0xff, 0xff})
			}
		}
	}
}
