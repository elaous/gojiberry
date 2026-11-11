package components

import "fmt"

// identity until an app sets it, before first render
var Translate = func(id, def string) string { return def }

func tr(id, def string) string { return Translate(id, def) }

func trf(id, def string, args ...any) string { return fmt.Sprintf(Translate(id, def), args...) }

var TranslatePlural = func(id, one, other string, n int) string {
	m := other
	if n == 1 {
		m = one
	}
	return fmt.Sprintf(m, n)
}

func trn(id, one, other string, n int) string { return TranslatePlural(id, one, other, n) }
