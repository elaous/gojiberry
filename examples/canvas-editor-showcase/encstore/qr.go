package encstore

import (
	"fmt"
	"html"
	"strings"

	"rsc.io/qr"
)

// RecoveryQR draws the recovery key as a QR code, an inline SVG that goes on the printed sheet so
// that the key can be moved to another device by pointing its camera at the paper instead of typing
// seventy characters. The key stays on the sheet as text as well, and the SVG has a text alternative
// (alt) for a screen reader: the code is a convenience, never the only way to read the key. It is
// black on white with the four-module quiet zone scanners need, and refers to nothing outside itself (no namespace URL either: inline SVG in HTML needs none).
func RecoveryQR(secret, alt string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ErrNotAKey
	}
	code, err := qr.Encode(secret, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := code.Size + 2*quiet
	var path strings.Builder
	for y := range code.Size {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < code.Size && code.Black(x, y) {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", start+quiet, y+quiet, x-start, x-start)
		}
	}
	return fmt.Sprintf(`<svg role="img" aria-label="%s" viewBox="0 0 %d %d" width="240" height="240" shape-rendering="crispEdges"><title>%s</title><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		html.EscapeString(alt), n, n, html.EscapeString(alt), n, n, path.String()), nil
}
