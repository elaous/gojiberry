package public

import (
	"context"
	"reflect"
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// DocumentsOnly is a Blocklist that accepts only document bodies.
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
