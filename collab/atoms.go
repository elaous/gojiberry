// Package collab keeps a canvas.TextFlow in step with a shared document.
package collab

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// Reserved runes for non-text atoms; U+FFFC is an object.
const (
	ctlPara  = ''
	ctlBreak = ''
	ctlCols  = ''
	objRune  = '￼'
	ctlKey   = "ctl"
)

type atom struct {
	r     rune
	attrs map[string]string // shared between the atoms of a run: read only
}

// inStream reports whether the atom takes a stream position.
func (a atom) inStream() bool { return a.r != ctlPara && a.r != ctlBreak && a.r != ctlCols }

func isReserved(r rune) bool { return r == ctlPara || r == ctlBreak || r == ctlCols }

// formatAttrs returns a format's non-zero fields as key-value pairs.
func formatAttrs(f canvas.Format) map[string]string {
	df := docfile.FromItems([]canvas.Item{canvas.Chars{Text: "x", Format: f}})[0].Chars.Format
	out := map[string]string{}
	v := reflect.ValueOf(df)
	for i := range v.NumField() {
		fv := v.Field(i)
		if fv.IsZero() {
			continue
		}
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		switch fv.Kind() {
		case reflect.String:
			out[name] = fv.String()
		case reflect.Bool:
			out[name] = "true"
		case reflect.Int, reflect.Int64:
			out[name] = strconv.FormatInt(fv.Int(), 10)
		case reflect.Float64:
			out[name] = strconv.FormatFloat(fv.Float(), 'g', -1, 64)
		}
	}
	return out
}

// attrsFormat is formatAttrs backwards.
func attrsFormat(attrs map[string]string) canvas.Format {
	var df docfile.Format
	v := reflect.ValueOf(&df).Elem()
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		s, ok := attrs[name]
		if !ok {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(s)
		case reflect.Bool:
			fv.SetBool(s == "true")
		case reflect.Int, reflect.Int64:
			n, _ := strconv.ParseInt(s, 10, 64)
			fv.SetInt(n)
		case reflect.Float64:
			n, _ := strconv.ParseFloat(s, 64)
			fv.SetFloat(n)
		}
	}
	return docfile.ToItems([]docfile.Item{{Chars: &docfile.Chars{Format: df}}})[0].(canvas.Chars).Format
}

// growsAtEnd reports whether text typed at a format's end extends it.
func growsAtEnd(key string) bool {
	switch key {
	case "link", "comment", "bookmark", "index", ctlKey:
		return false
	}
	return true
}

// ErrUnsupported is returned for flow content that cannot be shared.
type ErrUnsupported struct{ What string }

func (e ErrUnsupported) Error() string { return "collab: cannot share " + e.What }

// atomsFromItems flattens flow items into atoms.
func atomsFromItems(items []canvas.Item) ([]atom, error) {
	var out []atom
	control := func(r rune, it canvas.Item) error {
		d := docfile.FromItems([]canvas.Item{it})
		if len(d) != 1 {
			return ErrUnsupported{fmt.Sprintf("%T", it)}
		}
		b, err := json.Marshal(d[0])
		if err != nil {
			return err
		}
		out = append(out, atom{r: r, attrs: map[string]string{ctlKey: string(b)}})
		return nil
	}
	for _, it := range items {
		switch v := it.(type) {
		case canvas.Chars:
			attrs := formatAttrs(v.Format)
			for _, r := range v.Text {
				if isReserved(r) {
					return nil, ErrUnsupported{fmt.Sprintf("the reserved character U+%04X", r)}
				}
				out = append(out, atom{r: r, attrs: attrs})
			}
		case canvas.ParaFormat:
			if err := control(ctlPara, it); err != nil {
				return nil, err
			}
		case canvas.Break:
			if err := control(ctlBreak, it); err != nil {
				return nil, err
			}
		case canvas.ColumnConfig:
			if err := control(ctlCols, it); err != nil {
				return nil, err
			}
		case canvas.Inline:
			if err := control(objRune, it); err != nil {
				return nil, fmt.Errorf("an inline object (%T): %w", v.Object, err)
			}
		}
	}
	return out, nil
}

// itemsFromAtoms rebuilds flow items from atoms.
func itemsFromAtoms(atoms []atom) []canvas.Item {
	var out []canvas.Item
	var text []rune
	var cur map[string]string
	flush := func() {
		if len(text) > 0 {
			out = append(out, canvas.Chars{Text: string(text), Format: attrsFormat(cur)})
			text = nil
		}
	}
	for _, a := range atoms {
		payload, isCtl := a.attrs[ctlKey]
		if a.r == ctlPara || a.r == ctlBreak || a.r == ctlCols || (a.r == objRune && isCtl) {
			flush()
			var d docfile.Item
			if json.Unmarshal([]byte(payload), &d) != nil {
				continue
			}
			out = append(out, docfile.ToItems([]docfile.Item{d})...)
			continue
		}
		if len(text) > 0 && !sameAttrs(cur, a.attrs) {
			flush()
		}
		if len(text) == 0 {
			cur = a.attrs
		}
		text = append(text, a.r)
	}
	flush()
	return out
}

func sameAttrs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// streamCount counts the atoms that take a stream position.
func streamCount(atoms []atom) int {
	n := 0
	for _, a := range atoms {
		if a.inStream() {
			n++
		}
	}
	return n
}

// atomPos returns the atom index just after the o-th stream position.
func atomPos(atoms []atom, o int) int {
	if o <= 0 {
		return 0
	}
	n := 0
	for i, a := range atoms {
		if a.inStream() {
			n++
			if n == o {
				return i + 1
			}
		}
	}
	return len(atoms)
}
