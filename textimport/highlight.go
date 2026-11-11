package textimport

import (
	"regexp"
	"strings"
	"unicode"
)

type class int

const (
	normal class = iota
	key
	str
	num
	word // true, false, null, dates
	comment
	section
	good  // INFO
	warn  // WARN
	bad   // ERROR
	quiet // DEBUG, timestamps
)

var classColour = map[class]string{
	key: "#0b4f8a", str: "#116329", num: "#a35200", word: "#7b1fa2", comment: "#5c6370",
	section: "#0b4f8a", good: "#116329", warn: "#a35200", bad: "#b3261e", quiet: "#5c6370",
}

var classBold = map[class]bool{section: true, bad: true}

type span struct {
	text string
	c    class
}

func merge(in []span) []span {
	var out []span
	for _, s := range in {
		if s.text == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].c == s.c {
			out[n-1].text += s.text
			continue
		}
		out = append(out, s)
	}
	return out
}

func highlight(lang, src string) []span {
	var out []span
	switch lang {
	case "json", "json5":
		out = jsonSpans(src, lang == "json5")
	case "yaml":
		out = lines(src, yamlLine)
	case "toml":
		out = lines(src, tomlLine)
	case "ini":
		out = lines(src, iniLine)
	case "log":
		out = lines(src, logLine)
	case "xml":
		out = xmlSpans(src)
	default:
		return []span{{src, normal}}
	}
	return merge(out)
}

func lines(src string, f func(string) []span) []span {
	var out []span
	parts := strings.SplitAfter(src, "\n")
	for _, l := range parts {
		out = append(out, f(l)...)
	}
	return out
}

func jsonSpans(s string, json5 bool) []span {
	var out []span
	rs := []rune(s)
	i := 0
	emit := func(from, to int, c class) { out = append(out, span{string(rs[from:to]), c}) }
	for i < len(rs) {
		r := rs[i]
		switch {
		case r == '"' || (json5 && r == '\''):
			j := i + 1
			for j < len(rs) && rs[j] != r && rs[j] != '\n' {
				if rs[j] == '\\' && j+1 < len(rs) && rs[j+1] != '\n' {
					j++
				}
				j++
			}
			if j < len(rs) && rs[j] == r {
				j++
			}
			k := j
			for k < len(rs) && (rs[k] == ' ' || rs[k] == '\t') {
				k++
			}
			if k < len(rs) && rs[k] == ':' {
				emit(i, j, key)
			} else {
				emit(i, j, str)
			}
			i = j
		case json5 && r == '/' && i+1 < len(rs) && rs[i+1] == '/':
			j := i
			for j < len(rs) && rs[j] != '\n' {
				j++
			}
			emit(i, j, comment)
			i = j
		case json5 && r == '/' && i+1 < len(rs) && rs[i+1] == '*':
			j := i + 2
			for j+1 < len(rs) && (rs[j] != '*' || rs[j+1] != '/') {
				j++
			}
			j = min(j+2, len(rs))
			emit(i, j, comment)
			i = j
		case r == '-' || r == '+' || r == '.' || unicode.IsDigit(r):
			j := i + 1
			for j < len(rs) && (unicode.IsDigit(rs[j]) || strings.ContainsRune(".eE+-xXabcdefABCDEF_", rs[j])) {
				j++
			}
			if r == '-' || r == '+' || r == '.' {
				if j == i+1 || (!unicode.IsDigit(rs[i+1]) && rs[i+1] != '.') {
					emit(i, i+1, normal)
					i++
					break
				}
			}
			emit(i, j, num)
			i = j
		case unicode.IsLetter(r) || r == '_' || r == '$':
			j := i + 1
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_' || rs[j] == '$') {
				j++
			}
			w := string(rs[i:j])
			k := j
			for k < len(rs) && (rs[k] == ' ' || rs[k] == '\t') {
				k++
			}
			switch {
			case json5 && k < len(rs) && rs[k] == ':': // an unquoted key
				emit(i, j, key)
			case w == "true" || w == "false" || w == "null" || (json5 && (w == "Infinity" || w == "NaN")):
				emit(i, j, word)
			default:
				emit(i, j, normal)
			}
			i = j
		default:
			emit(i, i+1, normal)
			i++
		}
	}
	return out
}

var (
	yamlKey  = regexp.MustCompile(`^(\s*(?:-\s+)*)("[^"]*"|'[^']*'|[^\s#:'"][^#:]*?)(\s*:)(\s|$)`)
	yamlWord = regexp.MustCompile(`^(true|false|yes|no|on|off|null|~|True|False|Null)$`)
	yamlNum  = regexp.MustCompile(`^[-+]?(\d[\d_]*)(\.\d+)?([eE][-+]?\d+)?$|^0x[0-9a-fA-F]+$`)
)

func yamlLine(l string) []span {
	body := strings.TrimRight(l, "\n")
	nl := l[len(body):]
	if strings.HasPrefix(body, "---") || strings.HasPrefix(body, "...") {
		return []span{{body, quiet}, {nl, normal}}
	}
	if t := strings.TrimSpace(body); strings.HasPrefix(t, "#") {
		return []span{{body, comment}, {nl, normal}}
	}
	var out []span
	rest := body
	if m := yamlKey.FindStringSubmatch(body); m != nil {
		out = append(out, span{m[1], normal}, span{m[2], key}, span{m[3], normal})
		rest = body[len(m[1])+len(m[2])+len(m[3]):]
	} else if t := strings.TrimLeft(body, " "); strings.HasPrefix(t, "- ") || t == "-" {
		pre := body[:len(body)-len(t)] + "-"
		out = append(out, span{pre, normal})
		rest = body[len(pre):]
	}
	out = append(out, yamlValue(rest)...)
	return append(out, span{nl, normal})
}

func yamlValue(v string) []span {
	body, cmt := v, ""
	if i := commentAt(v); i >= 0 {
		body, cmt = v[:i], v[i:]
	}
	lead := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	t := strings.Trim(body, " \t")
	trail := body[len(lead)+len(t):]
	c := normal
	switch {
	case len(t) >= 2 && (t[0] == '"' && t[len(t)-1] == '"' || t[0] == '\'' && t[len(t)-1] == '\''):
		c = str
	case yamlNum.MatchString(t):
		c = num
	case yamlWord.MatchString(t):
		c = word
	case strings.HasPrefix(t, "&") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "!"):
		c = word
	}
	return []span{{lead, normal}, {t, c}, {trail, normal}, {cmt, comment}}
}

func commentAt(v string) int {
	var q rune
	for i, r := range v {
		switch {
		case q != 0:
			if r == q {
				q = 0
			}
		case r == '"' || r == '\'':
			q = r
		case r == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return i
		}
	}
	return -1
}

var (
	tomlHeader = regexp.MustCompile(`^(\s*)(\[\[?[^\]]*\]\]?)(.*)$`)
	tomlKey    = regexp.MustCompile(`^(\s*)("[^"]*"|'[^']*'|[A-Za-z0-9_.\- ]+?)(\s*=\s*)(.*)$`)
	tomlDate   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[-+]\d{2}:\d{2})?)?$|^\d{2}:\d{2}:\d{2}(\.\d+)?$`)
)

func tomlLine(l string) []span {
	body := strings.TrimRight(l, "\n")
	nl := l[len(body):]
	if strings.HasPrefix(strings.TrimSpace(body), "#") {
		return []span{{body, comment}, {nl, normal}}
	}
	if m := tomlHeader.FindStringSubmatch(body); m != nil {
		out := []span{{m[1], normal}, {m[2], section}}
		out = append(out, tomlValue(m[3])...)
		return append(out, span{nl, normal})
	}
	if m := tomlKey.FindStringSubmatch(body); m != nil {
		out := []span{{m[1], normal}, {m[2], key}, {m[3], normal}}
		out = append(out, tomlValue(m[4])...)
		return append(out, span{nl, normal})
	}
	return []span{{body, normal}, {nl, normal}}
}

func tomlValue(v string) []span {
	body, cmt := v, ""
	if i := commentAt(v); i >= 0 {
		body, cmt = v[:i], v[i:]
	}
	lead := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
	t := strings.Trim(body, " \t")
	trail := body[len(lead)+len(t):]
	c := normal
	switch {
	case len(t) >= 2 && (t[0] == '"' || t[0] == '\''):
		c = str
	case t == "true" || t == "false":
		c = word
	case tomlDate.MatchString(t):
		c = word
	case yamlNum.MatchString(t) || t == "inf" || t == "nan" || t == "+inf" || t == "-inf":
		c = num
	}
	return []span{{lead, normal}, {t, c}, {trail, normal}, {cmt, comment}}
}

var (
	iniSection = regexp.MustCompile(`^(\s*)(\[[^\]]*\])(.*)$`)
	iniKey     = regexp.MustCompile(`^(\s*)([^\s=:;#\[][^=:]*?)(\s*[=:]\s*)(.*)$`)
)

func iniLine(l string) []span {
	body := strings.TrimRight(l, "\n")
	nl := l[len(body):]
	if t := strings.TrimSpace(body); strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#") {
		return []span{{body, comment}, {nl, normal}}
	}
	if m := iniSection.FindStringSubmatch(body); m != nil {
		return []span{{m[1], normal}, {m[2], section}, {m[3], normal}, {nl, normal}}
	}
	if m := iniKey.FindStringSubmatch(body); m != nil {
		c := normal
		v := strings.TrimSpace(m[4])
		switch {
		case len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"'):
			c = str
		case yamlNum.MatchString(v):
			c = num
		case strings.EqualFold(v, "true") || strings.EqualFold(v, "false") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "no") || strings.EqualFold(v, "on") || strings.EqualFold(v, "off"):
			c = word
		}
		return []span{{m[1], normal}, {m[2], key}, {m[3], normal}, {m[4], c}, {nl, normal}}
	}
	return []span{{body, normal}, {nl, normal}}
}

func xmlSpans(s string) []span {
	var out []span
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			out = append(out, span{s, normal})
			break
		}
		out = append(out, span{s[:i], normal})
		s = s[i:]
		switch {
		case strings.HasPrefix(s, "<!--"):
			j := strings.Index(s[4:], "-->")
			end := len(s)
			if j >= 0 {
				end = 4 + j + 3
			}
			out = append(out, span{s[:end], comment})
			s = s[end:]
		case strings.HasPrefix(s, "<![CDATA["):
			j := strings.Index(s, "]]>")
			end := len(s)
			if j >= 0 {
				end = j + 3
			}
			out = append(out, span{s[:end], str})
			s = s[end:]
		case strings.HasPrefix(s, "<?") || strings.HasPrefix(s, "<!"):
			j := strings.IndexByte(s, '>')
			end := len(s)
			if j >= 0 {
				end = j + 1
			}
			out = append(out, span{s[:end], quiet})
			s = s[end:]
		default:
			var tag []span
			tag, s = xmlTag(s)
			out = append(out, tag...)
		}
	}
	return out
}

func xmlTag(s string) ([]span, string) {
	var out []span
	n := 1
	if n < len(s) && s[n] == '/' {
		n++
	}
	out = append(out, span{s[:n], normal})
	s = s[n:]
	name := 0
	for name < len(s) && !strings.ContainsRune(" \t\n\r/>=\"'", rune(s[name])) {
		name++
	}
	out = append(out, span{s[:name], key})
	s = s[name:]
	for len(s) > 0 {
		switch c := s[0]; c {
		case '>':
			return append(out, span{">", normal}), s[1:]
		case '/', '=', ' ', '\t', '\n', '\r':
			out = append(out, span{s[:1], normal})
			s = s[1:]
		case '"', '\'':
			j := strings.IndexByte(s[1:], c)
			end := len(s)
			if j >= 0 {
				end = j + 2
			}
			out = append(out, span{s[:end], str})
			s = s[end:]
		case '<': // a tag never closed: the next one starts here
			return out, s
		default:
			j := 0
			for j < len(s) && !strings.ContainsRune(" \t\n\r/>=\"'<", rune(s[j])) {
				j++
			}
			out = append(out, span{s[:j], word})
			s = s[j:]
		}
	}
	return out, ""
}

var (
	logTime  = regexp.MustCompile(`^(\[?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[-+]\d{2}:?\d{2})?\]?|\[?(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\]?|\[?\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\]?)`)
	logLevel = regexp.MustCompile(`\b(FATAL|CRITICAL|SEVERE|ERROR|ERR|WARNING|WARN|NOTICE|INFO|DEBUG|TRACE|VERBOSE)\b`)
)

func logLine(l string) []span {
	body := strings.TrimRight(l, "\n")
	nl := l[len(body):]
	var out []span
	rest := body
	if m := logTime.FindString(body); m != "" {
		out = append(out, span{m, quiet})
		rest = body[len(m):]
	}
	if loc := logLevel.FindStringIndex(rest); loc != nil && loc[0] < 40 {
		c := map[string]class{"FATAL": bad, "CRITICAL": bad, "SEVERE": bad, "ERROR": bad, "ERR": bad, "WARNING": warn, "WARN": warn,
			"NOTICE": good, "INFO": good, "DEBUG": quiet, "TRACE": quiet, "VERBOSE": quiet}[rest[loc[0]:loc[1]]]
		out = append(out, span{rest[:loc[0]], normal}, span{rest[loc[0]:loc[1]], c}, span{rest[loc[1]:], normal})
	} else {
		out = append(out, span{rest, normal})
	}
	return append(out, span{nl, normal})
}
