package components

import "fmt"

// Translate is how the components say their words: it returns the text for a
// message id ("saver.save"), or def, the English the component has, when it has
// none. It is the identity until an app sets it, once, before the first render
// (package i18n has a catalog that does the lookup):
//
//	components.Translate = func(id, def string) string { return cat.Text(lang, id, def) }
var Translate = func(id, def string) string { return def }

// tr is the text for id in the current language.
func tr(id, def string) string { return Translate(id, def) }

// trf is tr with fmt arguments put into the translated text.
func trf(id, def string, args ...any) string { return fmt.Sprintf(Translate(id, def), args...) }

// TranslatePlural is how the components say a message that depends on a number: the text for the id
// in the plural form of n, with n put in. one and other are the English the component has. It is
// English's rule until an app sets it (package i18n has Catalog.Plural):
//
//	components.TranslatePlural = func(id, one, other string, n int) string { return cat.Plural(lang, id, one, other, n) }
var TranslatePlural = func(id, one, other string, n int) string {
	m := other
	if n == 1 {
		m = one
	}
	return fmt.Sprintf(m, n)
}

// trn is the text for id with the plural form of n; the texts take n as their %d.
func trn(id, one, other string, n int) string { return TranslatePlural(id, one, other, n) }
