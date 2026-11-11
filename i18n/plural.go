package i18n

import (
	"fmt"
	"strings"
)

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
	if n == 1 {
		return "one"
	}
	return "other"
}

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
	if strings.Contains(m, "%d") {
		m = strings.Replace(m, "%d", "%s", 1)
		return fmt.Sprintf(m, append([]any{Number(lang, float64(n), 0)}, args...)...)
	}
	return m
}
