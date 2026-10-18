//go:build !experimental && !exp_textformats

package transfer

// Without the exp_textformats tag the editor reads only the formats it always has; see textformats.go.
func registerTextFormats(*Registry) {}

func wrapJSON(i Importer) Importer { return i }
