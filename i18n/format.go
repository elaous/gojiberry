package i18n

import (
	"math"
	"strconv"
	"strings"
	"time"
)

type numberFormat struct {
	decimal, group string
	minGroup       int
}

var numberFormats = map[string]numberFormat{
	"en": {".", ",", 4},
	"de": {",", ".", 4},
	"fr": {",", " ", 4}, // a narrow no-break space
	"es": {",", ".", 5}, // Spanish writes 1234 but 12.345
}

func formatFor(lang string) numberFormat {
	if f, ok := numberFormats[base(normalize(lang))]; ok {
		return f
	}
	return numberFormats["en"]
}

func Number(lang string, n float64, decimals int) string {
	f := formatFor(lang)
	if decimals < 0 {
		decimals = 0
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	s := strconv.FormatFloat(math.Abs(n), 'f', decimals, 64)
	whole, frac, _ := strings.Cut(s, ".")
	if len(whole) >= f.minGroup {
		var b strings.Builder
		for i, r := range whole {
			if i > 0 && (len(whole)-i)%3 == 0 {
				b.WriteString(f.group)
			}
			b.WriteRune(r)
		}
		whole = b.String()
	}
	out := whole
	if frac != "" {
		out += f.decimal + frac
	}
	if n < 0 && strings.Trim(out, "0., ") != "" {
		out = "-" + out
	}
	return out
}

func Percent(lang string, ratio float64) string {
	s := Number(lang, ratio*100, 0)
	switch base(normalize(lang)) {
	case "fr":
		return s + " %"
	case "de":
		return s + " %"
	}
	return s + "%"
}

// DateStyle is how much of a date is written.
type DateStyle int

const (
	DateShort  DateStyle = iota // 31/12/2026 (the order of the language)
	DateMedium                  // 31 Dec 2026
	DateLong                    // 31 December 2026
)

var months = map[string][12]string{
	"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	"de": {"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
	"fr": {"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
	"es": {"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
}

var shortMonths = map[string][12]string{
	"en": {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	"de": {"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."},
	"fr": {"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
	"es": {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"},
}

func Date(lang string, t time.Time, style DateStyle) string {
	lang = normalize(lang)
	b := base(lang)
	us := lang == "en-us" || lang == "en"
	if _, ok := months[b]; !ok {
		b, us = "en", true // a language without data gets plain English
	}
	d, m, y := t.Day(), int(t.Month())-1, t.Year()
	switch style {
	case DateShort:
		switch {
		case b == "en" && us:
			return two(m+1) + "/" + two(d) + "/" + strconv.Itoa(y)
		case b == "de":
			return two(d) + "." + two(m+1) + "." + strconv.Itoa(y)
		}
		return two(d) + "/" + two(m+1) + "/" + strconv.Itoa(y)
	case DateMedium:
		mn := shortMonths[b][m]
		switch {
		case b == "en" && us:
			return mn + " " + strconv.Itoa(d) + ", " + strconv.Itoa(y)
		case b == "de":
			return strconv.Itoa(d) + ". " + mn + " " + strconv.Itoa(y)
		}
		return strconv.Itoa(d) + " " + mn + " " + strconv.Itoa(y)
	}
	mn := months[b][m]
	switch {
	case b == "en" && us:
		return mn + " " + strconv.Itoa(d) + ", " + strconv.Itoa(y)
	case b == "de":
		return strconv.Itoa(d) + ". " + mn + " " + strconv.Itoa(y)
	case b == "es":
		return strconv.Itoa(d) + " de " + mn + " de " + strconv.Itoa(y)
	}
	return strconv.Itoa(d) + " " + mn + " " + strconv.Itoa(y)
}

func Time(lang string, t time.Time) string {
	lang = normalize(lang)
	if lang == "en" || lang == "en-us" {
		h := t.Hour() % 12
		if h == 0 {
			h = 12
		}
		ampm := "AM"
		if t.Hour() >= 12 {
			ampm = "PM"
		}
		return strconv.Itoa(h) + ":" + two(t.Minute()) + " " + ampm
	}
	return two(t.Hour()) + ":" + two(t.Minute())
}

func two(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func Zone(t time.Time) string {
	_, off := t.Zone()
	if off == 0 {
		return "UTC"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	h, m := off/3600, off%3600/60
	if m == 0 {
		return "UTC" + sign + strconv.Itoa(h)
	}
	return "UTC" + sign + strconv.Itoa(h) + ":" + two(m)
}

func DateTime(lang string, t time.Time) string {
	return Date(lang, t, DateMedium) + ", " + Time(lang, t) + " (" + Zone(t) + ")"
}
