//go:build !experimental && !exp_textformats

package transfer

func registerTextFormats(*Registry) {}

func wrapJSON(i Importer) Importer { return i }
