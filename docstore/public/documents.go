package public

import (
	"context"
	"reflect"
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// DocumentsOnly is a Blocklist that accepts only a body that is one of the editor's own formats (the .open message or
// the JSON form of a docfile.Document) and carries nothing that reaches out or runs: a link may go to http, https,
// mailto or a place in the document (never javascript:, data: or vbscript:), and a picture, clip, caption track or
// font must be inline (a data: URL of an image, audio, video, font or WebVTT type), never an address that a
// viewer's browser would fetch from somebody else's server (a tracking pixel, a hot-linked file, an SVG with a
// script). It is checked on every create and edit; combine it with a hash list by putting both in Options.Blocklist
// (a ChainBlocklist) or let the service chain its own digests behind it.
//
// This is the "only documents" rule of the public offer (docs/public-docstore.md section 5). It cannot say what the
// text means: abuse in words is for the reports and the takedown.
type DocumentsOnly struct{}

// Blocked implements Blocklist: true refuses the body (422).
func (DocumentsOnly) Blocked(_ context.Context, body []byte) (bool, error) {
	if len(body) == 0 {
		return true, nil
	}
	d, err := docfile.Open.Unmarshal(body)
	if err != nil {
		d, err = docfile.JSON.Unmarshal(body)
	}
	if err != nil {
		return true, nil
	}
	return !cleanValue(reflect.ValueOf(d)), nil
}

// ChainBlocklist asks each list in turn; the first that refuses (or fails) decides.
func ChainBlocklist(lists ...Blocklist) Blocklist { return chainBlocklist(lists) }

var inlineTypes = []string{"data:image/png", "data:image/jpeg", "data:image/gif", "data:image/webp", "data:audio/", "data:video/", "data:font/", "data:text/vtt", "data:application/font"}

func inlineOnly(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "" {
		return true
	}
	for _, p := range inlineTypes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

func linkOK(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	for _, p := range []string{"", "#", "http://", "https://", "mailto:"} {
		if u == "" || strings.HasPrefix(u, p) && p != "" {
			return true
		}
	}
	return false
}

// cleanValue walks a document by reflection: the string fields named Link, URL and CaptionsURL are checked, the
// rest is left alone. A document is a tree of plain structs, slices and pointers, so the walk ends.
func cleanValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || cleanValue(v.Elem())
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if !cleanValue(v.Index(i)) {
				return false
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := range v.NumField() {
			f := v.Field(i)
			if !t.Field(i).IsExported() {
				continue
			}
			if f.Kind() == reflect.String {
				switch t.Field(i).Name {
				case "Link":
					if !linkOK(f.String()) {
						return false
					}
				case "URL", "CaptionsURL":
					if !inlineOnly(f.String()) {
						return false
					}
				}
				continue
			}
			if !cleanValue(f) {
				return false
			}
		}
	}
	return true
}
