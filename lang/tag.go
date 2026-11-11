package lang

import "strings"

func ValidTag(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 100 {
		return false
	}
	parts := strings.Split(s, "-")
	isAlpha := func(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	isNum := func(c rune) bool { return c >= '0' && c <= '9' }
	all := func(p string, lo, hi int, ok func(rune) bool) bool {
		if len(p) < lo || len(p) > hi {
			return false
		}
		for _, c := range p {
			if !ok(c) {
				return false
			}
		}
		return true
	}
	alpha := func(p string, lo, hi int) bool { return all(p, lo, hi, isAlpha) }
	num := func(p string, n int) bool { return all(p, n, n, isNum) }
	alnum := func(p string, lo, hi int) bool {
		return all(p, lo, hi, func(c rune) bool { return isAlpha(c) || isNum(c) })
	}
	if strings.EqualFold(parts[0], "x") {
		if len(parts) < 2 {
			return false
		}
		for _, p := range parts[1:] {
			if !alnum(p, 1, 8) {
				return false
			}
		}
		return true
	}
	if !alpha(parts[0], 2, 8) {
		return false
	}
	i := 1
	if len(parts[0]) <= 3 { // up to three extended language subtags
		for n := 0; n < 3 && i < len(parts) && alpha(parts[i], 3, 3); n++ {
			i++
		}
	}
	if i < len(parts) && alpha(parts[i], 4, 4) {
		i++
	}
	if i < len(parts) && (alpha(parts[i], 2, 2) || num(parts[i], 3)) {
		i++
	}
	for i < len(parts) && (alnum(parts[i], 5, 8) || (len(parts[i]) == 4 && isNum(rune(parts[i][0])) && alnum(parts[i], 4, 4))) {
		i++
	}
	for i < len(parts) && alnum(parts[i], 1, 1) && !strings.EqualFold(parts[i], "x") {
		i++
		n := 0
		for i < len(parts) && alnum(parts[i], 2, 8) {
			i++
			n++
		}
		if n == 0 {
			return false
		}
	}
	if i < len(parts) && strings.EqualFold(parts[i], "x") {
		i++
		n := 0
		for i < len(parts) && alnum(parts[i], 1, 8) {
			i++
			n++
		}
		if n == 0 {
			return false
		}
	}
	return i == len(parts)
}
