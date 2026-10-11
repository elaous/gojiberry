package i18n

import (
	"fmt"
	"strings"
)

// Plural forms. A message that depends on a number is stored as one text per plural category of the
// language, under the id with "#category" added: "files.count#one" and "files.count#other" ("%d file",
// "%d files"). The categories are CLDR's cardinal ones (zero, one, two, few, many, other); a language
// has only those it needs (English: one and other; Russian: one, few and many; Japanese: other).
// Catalog.Plural picks the text for the number, falls back to "#other" and then to the English the
// code has, and puts the number in.

// PluralCategory is the CLDR cardinal plural category of the whole number n in lang ("en", "fr-CA").
func PluralCategory(lang string, n int) string {
	if n < 0 {
		n = -n
	}
	switch base(normalize(lang)) {
	case "ja", "zh", "ko", "vi", "th", "id", "ms":
		return "other"
	case "fr", "pt":
		switch {
		case n == 0 || n == 1:
			return "one"
		case n != 0 && n%1000000 == 0:
			return "many"
		}
		return "other"
	case "es", "it", "ca":
		switch {
		case n == 1:
			return "one"
		case n != 0 && n%1000000 == 0:
			return "many"
		}
		return "other"
	case "ru", "uk", "be":
		switch {
		case n%10 == 1 && n%100 != 11:
			return "one"
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return "few"
		}
		return "many"
	case "pl":
		switch {
		case n == 1:
			return "one"
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return "few"
		}
		return "many"
	case "cs", "sk":
		switch {
		case n == 1:
			return "one"
		case n >= 2 && n <= 4:
			return "few"
		}
		return "other"
	case "ar":
		switch {
		case n == 0:
			return "zero"
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		case n%100 >= 3 && n%100 <= 10:
			return "few"
		case n%100 >= 11:
			return "many"
		}
		return "other"
	case "he":
		switch n {
		case 1:
			return "one"
		case 2:
			return "two"
		}
		return "other"
	}
	// English, German, Dutch, Swedish and the rest: one for exactly 1.
	if n == 1 {
		return "one"
	}
	return "other"
}

// Plural returns the text of id for the number n in lang, with n put in (and args after it, for
// messages that have more than one verb). one and other are the English texts the code has, used when
// the catalog has nothing for the category (and "#other" nothing either).
func (c *Catalog) Plural(lang, id, one, other string, n int, args ...any) string {
	cat := PluralCategory(lang, n)
	m, ok := c.Lookup(lang, id+"#"+cat)
	if !ok {
		m, ok = c.Lookup(lang, id+"#other")
	}
	if !ok {
		m = other
		if n == 1 {
			m = one
		}
	}
	if strings.ContainsRune(m, '%') {
		return fmt.Sprintf(m, append([]any{n}, args...)...)
	}
	return m
}
