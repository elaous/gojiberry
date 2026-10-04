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
