package docfile

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The protobuf text format of a Document (what a .pbtxt file holds), written and read
// against the model's types. It writes the form protobuf's own libraries print, and reads
// that and what they accept: either spelling of a field name (snake_case or the Go
// name), `:` or not before a message, `{}` or `<>`, `,` and `;` between fields,
// `#` comments, adjacent strings joined, a list as repeated fields or in `[...]`.

func marshalText(m *message, v reflect.Value) []byte {
	var b strings.Builder
	writeText(&b, m, v, 0)
	return []byte(b.String())
}

func writeText(b *strings.Builder, m *message, v reflect.Value, depth int) {
	pad := strings.Repeat("  ", depth)
	for i := range m.fields {
		f := &m.fields[i]
		fv := v.Field(f.index)
		if f.repeated {
			for j := range fv.Len() {
				writeTextValue(b, pad, f, fv.Index(j), depth)
			}
			continue
		}
		if f.kind == kMsg {
			if f.pointer {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			} else if fv.IsZero() {
				continue
			}
		} else if fv.IsZero() {
			continue
		}
		writeTextValue(b, pad, f, fv, depth)
	}
}

func writeTextValue(b *strings.Builder, pad string, f *field, v reflect.Value, depth int) {
	b.WriteString(pad)
	b.WriteString(f.name)
	if f.kind == kMsg {
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		b.WriteString(": {\n")
		writeText(b, f.msg, v, depth+1)
		b.WriteString(pad)
		b.WriteString("}\n")
		return
	}
	b.WriteString(": ")
	switch f.kind {
	case kString:
		b.WriteString(quoteText(v.String()))
	case kBool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case kFloat:
		b.WriteString(formatFloat(v.Float()))
	default:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	}
	b.WriteByte('\n')
}

func formatFloat(x float64) string {
	switch {
	case math.IsNaN(x):
		return "nan"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	return strconv.FormatFloat(x, 'g', -1, 64)
}

// quoteText writes s as a double-quoted string with the escapes protobuf text uses; text
// that is valid UTF-8 stays as it is.
func quoteText(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '"':
			b.WriteString(`\"`)
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

type textParser struct {
	s    string
	pos  int
	line int
}

var errText = errors.New("docfile: malformed protobuf text")

func (p *textParser) fail(format string, args ...any) error {
	return fmt.Errorf("%w: line %d: %s", errText, p.line+1, fmt.Sprintf(format, args...))
}

// skip passes white space and comments.
func (p *textParser) skip() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case '\n':
			p.line++
			p.pos++
		case ' ', '\t', '\r':
			p.pos++
		case '#':
			for p.pos < len(p.s) && p.s[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *textParser) peek() byte {
	p.skip()
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (p *textParser) ident() string {
	p.skip()
	start := p.pos
	for p.pos < len(p.s) && isIdent(p.s[p.pos]) {
		p.pos++
	}
	return p.s[start:p.pos]
}

func unmarshalText(m *message, data []byte, v reflect.Value) error {
	p := &textParser{s: string(data)}
	if err := p.body(m, v, 0); err != nil {
		return err
	}
	if p.peek() != 0 {
		return p.fail("unexpected %q", p.s[p.pos])
	}
	return nil
}

const maxTextDepth = 64

// body reads fields until the closing delimiter (0 at the end of the text).
func (p *textParser) body(m *message, v reflect.Value, depth int) error {
	if depth > maxTextDepth {
		return p.fail("too deeply nested")
	}
	for {
		c := p.peek()
		if c == 0 || c == '}' || c == '>' {
			return nil
		}
		name := p.ident()
		if name == "" {
			return p.fail("expected a field name, found %q", c)
		}
		var f *field
		for i := range m.fields {
			if m.fields[i].name == name || m.fields[i].goName == name {
				f = &m.fields[i]
				break
			}
		}
		if f == nil {
			return p.fail("%s has no field %q", m.name, name)
		}
		colon := false
		if p.peek() == ':' {
			p.pos++
			colon = true
		}
		fv := v.Field(f.index)
		if f.repeated && p.peek() == '[' {
			p.pos++
			for p.peek() != ']' {
				if err := p.value(f, fv, colon, depth); err != nil {
					return err
				}
				if p.peek() == ',' {
					p.pos++
				} else if p.peek() != ']' {
					return p.fail("expected , or ] in a list")
				}
			}
			p.pos++
		} else if err := p.value(f, fv, colon, depth); err != nil {
			return err
		}
		if c := p.peek(); c == ',' || c == ';' {
			p.pos++
		}
	}
}

func (p *textParser) value(f *field, fv reflect.Value, colon bool, depth int) error {
	if f.kind == kMsg {
		open := p.peek()
		var closer byte
		switch open {
		case '{':
			closer = '}'
		case '<':
			closer = '>'
		default:
			return p.fail("expected { for %s", f.name)
		}
		p.pos++
		dst := fv
		if f.repeated {
			fv.Set(reflect.Append(fv, reflect.New(fv.Type().Elem()).Elem()))
			dst = fv.Index(fv.Len() - 1)
		}
		if dst.Kind() == reflect.Pointer {
			if dst.IsNil() {
				dst.Set(reflect.New(dst.Type().Elem()))
			}
			dst = dst.Elem()
		}
		if err := p.body(f.msg, dst, depth+1); err != nil {
			return err
		}
		if p.peek() != closer {
			return p.fail("expected %q", closer)
		}
		p.pos++
		return nil
	}
	if !colon {
		return p.fail("expected : after %s", f.name)
	}
	dst := fv
	if f.repeated {
		fv.Set(reflect.Append(fv, reflect.New(fv.Type().Elem()).Elem()))
		dst = fv.Index(fv.Len() - 1)
	}
	switch f.kind {
	case kString:
		s, err := p.str()
		if err != nil {
			return err
		}
		dst.SetString(s)
	case kBool:
		w := p.ident()
		switch w {
		case "true", "True", "t", "1":
			dst.SetBool(true)
		case "false", "False", "f", "0":
			dst.SetBool(false)
		default:
			return p.fail("%q is not a bool", w)
		}
	case kFloat:
		x, err := p.float()
		if err != nil {
			return err
		}
		dst.SetFloat(x)
	default:
		w := p.number()
		n, err := strconv.ParseInt(w, 0, 64)
		if err != nil {
			return p.fail("%q is not an integer", w)
		}
		dst.SetInt(n)
	}
	return nil
}

// number reads the characters of a number (sign, digits, hex, a decimal point, an exponent).
func (p *textParser) number() string {
	p.skip()
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if isIdent(c) || c == '-' || c == '+' || c == '.' {
			p.pos++
			continue
		}
		break
	}
	return p.s[start:p.pos]
}

func (p *textParser) float() (float64, error) {
	w := p.number()
	switch strings.ToLower(strings.TrimPrefix(w, "+")) {
	case "inf", "infinity":
		return math.Inf(1), nil
	case "-inf", "-infinity":
		return math.Inf(-1), nil
	case "nan", "-nan":
		return math.NaN(), nil
	}
	w = strings.TrimSuffix(strings.TrimSuffix(w, "f"), "F")
	x, err := strconv.ParseFloat(w, 64)
	if err != nil {
		return 0, p.fail("%q is not a number", w)
	}
	return x, nil
}

// str reads one or more adjacent quoted strings.
func (p *textParser) str() (string, error) {
	var out strings.Builder
	n := 0
	for {
		q := p.peek()
		if q != '"' && q != '\'' {
			break
		}
		n++
		p.pos++
		for {
			if p.pos >= len(p.s) || p.s[p.pos] == '\n' {
				return "", p.fail("a string is not closed")
			}
			c := p.s[p.pos]
			if c == q {
				p.pos++
				break
			}
			if c != '\\' {
				out.WriteByte(c)
				p.pos++
				continue
			}
			p.pos++
			if p.pos >= len(p.s) {
				return "", p.fail("a string is not closed")
			}
			e := p.s[p.pos]
			p.pos++
			switch e {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'a':
				out.WriteByte('\a')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case 'v':
				out.WriteByte('\v')
			case '\\', '\'', '"', '?':
				out.WriteByte(e)
			case 'x', 'X':
				v, w := hexRun(p.s[p.pos:], 2)
				if w == 0 {
					return "", p.fail("bad \\x escape")
				}
				out.WriteByte(byte(v))
				p.pos += w
			case 'u', 'U':
				digits := 4
				if e == 'U' {
					digits = 8
				}
				v, w := hexRun(p.s[p.pos:], digits)
				if w != digits || !utf8.ValidRune(rune(v)) {
					return "", p.fail("bad \\%c escape", e)
				}
				out.WriteRune(rune(v))
				p.pos += w
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '7'; k++ {
						v = v*8 + int(p.s[p.pos]-'0')
						p.pos++
					}
					out.WriteByte(byte(v))
				} else {
					return "", p.fail("unknown escape \\%c", e)
				}
			}
		}
	}
	if n == 0 {
		return "", p.fail("expected a string")
	}
	if !utf8.ValidString(out.String()) {
		return "", p.fail("a string that is not UTF-8")
	}
	return out.String(), nil
}

// hexRun reads up to max hex digits, returning the value and how many there were.
func hexRun(s string, max int) (uint32, int) {
	var v uint32
	n := 0
	for n < len(s) && n < max {
		c := s[n]
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint32(c-'A') + 10
		default:
			return v, n
		}
		v = v<<4 | d
		n++
	}
	return v, n
}
