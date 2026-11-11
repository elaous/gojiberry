//go:build js && wasm

package fonts

import (
	"context"
	"errors"
	"sort"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

var ErrUnsupported = errors.New("fonts: this browser cannot list locally installed fonts")

type browserLocal struct{}

func BrowserLocal() Repository { return browserLocal{} }

func (browserLocal) Name() string { return "This device" }

func (b browserLocal) Fonts(ctx context.Context) ([]Font, error) {
	q := js.Global().Get("queryLocalFonts")
	if q.Type() != js.TypeFunction {
		return nil, ErrUnsupported
	}
	type result struct {
		list js.Value
		err  error
	}
	done := make(chan result, 1)
	go func() {
		list, err := domrt.Await(q.Invoke())
		done <- result{list, err}
	}()
	var list js.Value
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		list = r.list
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	byFamily := map[string]*Font{}
	for i := 0; i < list.Length(); i++ {
		d := list.Index(i)
		family, style := d.Get("family").String(), d.Get("style").String()
		f, ok := byFamily[family]
		if !ok {
			f = &Font{Family: family, Source: b.Name()}
			byFamily[family] = f
		}
		w, italic := classifyStyle(style)
		if !hasVariant(f.Variants, Variant{Weight: w, Italic: italic}) {
			f.Variants = append(f.Variants, Variant{Weight: w, Italic: italic, Style: style})
		}
	}
	out := make([]Font, 0, len(byFamily))
	for _, f := range byFamily {
		sortVariants(f.Variants)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Family) < strings.ToLower(out[j].Family) })
	return out, nil
}

func classifyStyle(style string) (weight int, italic bool) {
	s := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(style))
	italic = strings.Contains(s, "italic") || strings.Contains(s, "oblique")
	for _, w := range []struct {
		word   string
		weight int
	}{{"thin", 100}, {"extralight", 200}, {"ultralight", 200}, {"light", 300}, {"medium", 500}, {"semibold", 600},
		{"demibold", 600}, {"extrabold", 800}, {"ultrabold", 800}, {"black", 900}, {"heavy", 900}, {"bold", 700}} {
		if strings.Contains(s, w.word) {
			return w.weight, italic
		}
	}
	return 400, italic
}

func hasVariant(vs []Variant, v Variant) bool { return variantIndex(vs, v) >= 0 }
