package transpile

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Options controls opt-in behavior not on by default.
type Options struct {
	Experimental        bool
	ExperimentalLoaders map[string]LoaderFunc
}

func Transpile(filename string, src []byte) ([]byte, error) {
	return TranspileWithOptions(filename, src, Options{})
}

func TranspileWithOptions(filename string, src []byte, opts Options) ([]byte, error) {
	withResources, err := processImports(filename, filepath.Dir(filename), string(src), opts)
	if err != nil {
		return nil, err
	}
	p := &parser{file: filename, src: []rune(withResources)}
	out, err := p.transpileChunk(p.src)
	if err != nil {
		return nil, err
	}
	withPageMetadata, err := processPageMetadata(filename, filepath.Dir(filename), out)
	if err != nil {
		return nil, err
	}
	return []byte(withPageMetadata), nil
}

type parser struct {
	file string
	src  []rune // the whole original file, used for line/col in error messages
}

// after these, '<' can only open a tag
var exprIntroducingWords = map[string]bool{
	"return": true, "go": true, "defer": true, "range": true,
	"case": true, "if": true, "for": true, "switch": true, "else": true,
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func prevAllowsExprStart(chunk []rune, pos int) bool {
	i := pos
	sawNewline := false
	for i > 0 {
		c := chunk[i-1]
		if c == '\n' {
			sawNewline = true
			i--
			continue
		}
		if unicode.IsSpace(c) {
			i--
			continue
		}
		break
	}
	if i == 0 {
		return true
	}
	c := chunk[i-1]
	switch {
	case isIdentPart(c):
		j := i
		for j > 0 && isIdentPart(chunk[j-1]) {
			j--
		}
		word := string(chunk[j:i])
		if exprIntroducingWords[word] {
			return true
		}
		return sawNewline
	case c == ')' || c == ']' || c == '}' || c == '"' || c == '`' || c == '\'':
		return sawNewline
	default:
		return true
	}
}

func posInfo(src []rune, idx int) (line, col int) {
	line = 1
	lastNL := -1
	for i := 0; i < idx && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			lastNL = i
		}
	}
	return line, idx - lastNL
}

func (p *parser) errorf(chunk []rune, pos int, format string, args ...any) error {
	line, col := posInfo(chunk, pos)
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%s:%d:%d: %s", p.file, line, col, msg)
}

func (p *parser) transpileChunk(chunk []rune) (string, error) {
	var out strings.Builder
	pos := 0
	n := len(chunk)
	for pos < n {
		c := chunk[pos]

		if end, ok := skipStringLiteral(chunk, pos); ok {
			out.WriteString(string(chunk[pos:end]))
			pos = end
			continue
		}
		if end, ok := skipComment(chunk, pos); ok {
			out.WriteString(string(chunk[pos:end]))
			pos = end
			continue
		}

		if c == '<' && pos+1 < n && (isTagNameStart(chunk[pos+1]) || chunk[pos+1] == '>') && prevAllowsExprStart(chunk, pos) {
			expr, newPos, err := p.parseTag(chunk, pos)
			if err != nil {
				return "", err
			}
			out.WriteString(expr)
			pos = newPos
			continue
		}

		out.WriteRune(c)
		pos++
	}
	return out.String(), nil
}

func isTagNameStart(r rune) bool {
	return unicode.IsLetter(r)
}

func skipStringLiteral(chunk []rune, pos int) (int, bool) {
	q := chunk[pos]
	if q != '"' && q != '\'' && q != '`' {
		return 0, false
	}
	i := pos + 1
	n := len(chunk)
	if q == '`' {
		for i < n && chunk[i] != '`' {
			i++
		}
		if i < n {
			i++
		}
		return i, true
	}
	for i < n {
		if chunk[i] == '\\' && i+1 < n {
			i += 2
			continue
		}
		if chunk[i] == q {
			i++
			break
		}
		i++
	}
	return i, true
}

func skipComment(chunk []rune, pos int) (int, bool) {
	n := len(chunk)
	if pos+1 >= n || chunk[pos] != '/' {
		return 0, false
	}
	if chunk[pos+1] == '/' {
		i := pos + 2
		for i < n && chunk[i] != '\n' {
			i++
		}
		return i, true
	}
	if chunk[pos+1] == '*' {
		i := pos + 2
		for i+1 < n && (chunk[i] != '*' || chunk[i+1] != '/') {
			i++
		}
		if i+1 < n {
			i += 2
		} else {
			i = n
		}
		return i, true
	}
	return 0, false
}

func findMatchingBrace(chunk []rune, openPos int) (int, error) {
	depth := 0
	i := openPos
	n := len(chunk)
	for i < n {
		if end, ok := skipStringLiteral(chunk, i); ok {
			i = end
			continue
		}
		if end, ok := skipComment(chunk, i); ok {
			i = end
			continue
		}
		switch chunk[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
		i++
	}
	return 0, fmt.Errorf("unterminated '{' (unmatched brace)")
}

type attr struct {
	name    string
	literal string // set when the value was a plain "..." literal
	expr    string // set when the value was a {expr} hole (already transpiled)
	isExpr  bool
	bare    bool // attribute with no value at all
}

type childNode struct {
	text   string // set for a literal text run
	expr   string
	isText bool
}

func (p *parser) parseTag(chunk []rune, pos int) (string, int, error) {
	n := len(chunk)
	startPos := pos
	pos++ // consume '<'

	if pos < n && chunk[pos] == '>' {
		pos++ // consume '>'
		children, newPos, err := p.parseChildren(chunk, pos, "")
		if err != nil {
			return "", 0, err
		}
		return p.renderFragment(children), newPos, nil
	}

	nameStart := pos
	for pos < n && (isIdentPart(chunk[pos]) || chunk[pos] == '.' || chunk[pos] == '-') {
		pos++
	}
	if pos == nameStart {
		return "", 0, p.errorf(chunk, pos, "expected tag name after '<'")
	}
	name := string(chunk[nameStart:pos])
	if name == "Fragment" {
		return p.parseKeyedFragment(chunk, pos)
	}
	if !isComponentName(name) && !isKnownTag(name) {
		return "", 0, p.errorf(chunk, nameStart, "unknown HTML tag <%s>: not a recognized HTML element, not a custom element (needs a hyphen, e.g. <my-widget>), and not a Component (needs a capital first letter); if this is a real tag xango doesn't know about yet, add it to validHTMLTags in internal/transpile/htmlvalidate.go", name)
	}

	typeArgs := ""
	if pos < n && chunk[pos] == '[' && isComponentName(name) {
		end, err := findMatchingBracket(chunk, pos)
		if err != nil {
			return "", 0, p.errorf(chunk, pos, "in the type arguments of <%s>: %v", name, err)
		}
		typeArgs = string(chunk[pos : end+1])
		pos = end + 1
	}

	attrs, pos, err := p.parseAttrs(chunk, pos, name)
	if err != nil {
		return "", 0, err
	}

	for pos < n && isSpace(chunk[pos]) {
		pos++
	}
	if pos+1 < n && chunk[pos] == '/' && chunk[pos+1] == '>' {
		pos += 2
		return p.renderTag(name, typeArgs, attrs, nil), pos, nil
	}
	if pos >= n || chunk[pos] != '>' {
		return "", 0, p.errorf(chunk, pos, "expected '>' or '/>' to close <%s> tag opened", name)
	}
	pos++ // consume '>'

	children, pos, err := p.parseChildren(chunk, pos, name)
	if err != nil {
		return "", 0, err
	}
	_ = startPos
	return p.renderTag(name, typeArgs, attrs, children), pos, nil
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

func (p *parser) parseAttrs(chunk []rune, pos int, tagName string) ([]attr, int, error) {
	n := len(chunk)
	validateAttrs := tagName != "" && !isComponentName(tagName) && !strings.ContainsRune(tagName, '-')
	var attrs []attr
	for {
		for pos < n && isSpace(chunk[pos]) {
			pos++
		}
		if pos >= n {
			return nil, 0, p.errorf(chunk, pos, "unterminated tag: EOF while scanning attributes")
		}
		if chunk[pos] == '>' || (chunk[pos] == '/' && pos+1 < n && chunk[pos+1] == '>') {
			return attrs, pos, nil
		}
		nameStart := pos
		for pos < n && !isSpace(chunk[pos]) && chunk[pos] != '=' && chunk[pos] != '>' && chunk[pos] != '/' {
			pos++
		}
		if pos == nameStart {
			return nil, 0, p.errorf(chunk, pos, "expected attribute name")
		}
		name := string(chunk[nameStart:pos])
		if validateAttrs && !isEventAttr(name) && !isKnownAttr(name) {
			return nil, 0, p.errorf(chunk, nameStart, "unknown HTML attribute %q on <%s>: not a recognized HTML attribute and not data-*/aria-*; if this is real, add it to validHTMLAttrs in internal/transpile/htmlvalidate.go", name, tagName)
		}

		save := pos
		for pos < n && isSpace(chunk[pos]) {
			pos++
		}
		if pos < n && chunk[pos] == '=' {
			pos++
			for pos < n && isSpace(chunk[pos]) {
				pos++
			}
			if pos >= n {
				return nil, 0, p.errorf(chunk, pos, "expected attribute value after '%s='", name)
			}
			switch chunk[pos] {
			case '"':
				end, _ := skipStringLiteral(chunk, pos)
				raw := string(chunk[pos+1 : end-1])
				attrs = append(attrs, attr{name: name, literal: raw})
				pos = end
			case '{':
				closeIdx, err := findMatchingBrace(chunk, pos)
				if err != nil {
					return nil, 0, p.errorf(chunk, pos, "in value of attribute %q: %v", name, err)
				}
				inner := chunk[pos+1 : closeIdx]
				exprSrc, err := p.transpileChunk(inner)
				if err != nil {
					return nil, 0, err
				}
				attrs = append(attrs, attr{name: name, expr: exprSrc, isExpr: true})
				pos = closeIdx + 1
			default:
				return nil, 0, p.errorf(chunk, pos, "attribute %q value must be \"literal\" or {expr}", name)
			}
		} else {
			pos = save
			attrs = append(attrs, attr{name: name, bare: true})
		}
	}
}

func (p *parser) parseChildren(chunk []rune, pos int, tagName string) ([]childNode, int, error) {
	n := len(chunk)
	var children []childNode
	textStart := pos
	flushText := func(end int) {
		if end > textStart {
			raw := string(chunk[textStart:end])
			if cleaned, keep := cleanText(raw); keep {
				children = append(children, childNode{text: cleaned, isText: true})
			}
		}
	}
	for pos < n {
		c := chunk[pos]
		if c == '<' {
			if pos+1 < n && chunk[pos+1] == '/' {
				flushText(pos)
				closeEnd, closedName, err := parseCloseTag(chunk, pos)
				if err != nil {
					return nil, 0, err
				}
				if closedName != tagName {
					return nil, 0, p.errorf(chunk, pos, "mismatched closing tag: expected </%s>, found </%s>", labelFor(tagName), closedName)
				}
				return children, closeEnd, nil
			}
			flushText(pos)
			expr, newPos, err := p.parseTag(chunk, pos)
			if err != nil {
				return nil, 0, err
			}
			children = append(children, childNode{expr: expr})
			pos = newPos
			textStart = pos
			continue
		}
		if c == '{' {
			flushText(pos)
			closeIdx, err := findMatchingBrace(chunk, pos)
			if err != nil {
				return nil, 0, p.errorf(chunk, pos, "in child expression: %v", err)
			}
			inner := chunk[pos+1 : closeIdx]
			exprSrc, err := p.transpileChunk(inner)
			if err != nil {
				return nil, 0, err
			}
			children = append(children, childNode{expr: fmt.Sprintf("xango.Child(%s)", exprSrc)})
			pos = closeIdx + 1
			textStart = pos
			continue
		}
		pos++
	}
	return nil, 0, p.errorf(chunk, pos, "unterminated tag: EOF looking for closing tag </%s>", labelFor(tagName))
}

func labelFor(tagName string) string {
	if tagName == "" {
		return ""
	}
	return tagName
}

func parseCloseTag(chunk []rune, pos int) (int, string, error) {
	n := len(chunk)
	i := pos + 2 // skip "</"
	nameStart := i
	for i < n && (isIdentPart(chunk[i]) || chunk[i] == '.' || chunk[i] == '-') {
		i++
	}
	name := string(chunk[nameStart:i])
	for i < n && isSpace(chunk[i]) {
		i++
	}
	if i >= n || chunk[i] != '>' {
		return 0, "", fmt.Errorf("malformed closing tag near position %d", pos)
	}
	return i + 1, name, nil
}

func cleanText(s string) (string, bool) {
	if strings.TrimSpace(s) == "" {
		if strings.Contains(s, "\n") {
			return "", false
		}
		return s, true
	}
	if strings.Contains(s, "\n") {
		return strings.TrimSpace(s), true
	}
	return s, true
}

func isComponentName(name string) bool {
	last := name
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 {
		last = name[idx+1:]
	}
	r := []rune(last)
	return len(r) > 0 && unicode.IsUpper(r[0])
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func isEventAttr(name string) bool {
	if len(name) < 3 || name[:2] != "on" {
		return false
	}
	return unicode.IsUpper(rune(name[2]))
}

// DOM event names are always fully lowercase
func eventNameFor(attrName string) string {
	return strings.ToLower(attrName[2:])
}

func (p *parser) parseKeyedFragment(chunk []rune, pos int) (string, int, error) {
	n := len(chunk)
	attrs, pos, err := p.parseAttrs(chunk, pos, "Fragment")
	if err != nil {
		return "", 0, err
	}
	if len(attrs) != 1 || attrs[0].name != "key" || attrs[0].bare {
		return "", 0, p.errorf(chunk, pos, `<Fragment> only accepts a single key="..." or key={expr} attribute, e.g. <Fragment key={item.ID}>...</Fragment>`)
	}
	keyExpr := keyAttrExprSource(attrs[0])

	for pos < n && isSpace(chunk[pos]) {
		pos++
	}
	if pos+1 < n && chunk[pos] == '/' && chunk[pos+1] == '>' {
		pos += 2
		return fmt.Sprintf("xango.Frag().WithKey(%s)", keyExpr), pos, nil
	}
	if pos >= n || chunk[pos] != '>' {
		return "", 0, p.errorf(chunk, pos, "expected '>' or '/>' to close <Fragment> tag opened")
	}
	pos++ // consume '>'

	children, pos, err := p.parseChildren(chunk, pos, "Fragment")
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%s.WithKey(%s)", p.renderFragment(children), keyExpr), pos, nil
}

func keyAttrExprSource(a attr) string {
	if a.isExpr {
		return fmt.Sprintf("xango.AttrVal(%s)", a.expr)
	}
	return strconv.Quote(a.literal)
}

func (p *parser) renderFragment(children []childNode) string {
	var b strings.Builder
	b.WriteString("xango.Frag(")
	for i, c := range children {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(childExprSource(c))
	}
	b.WriteString(")")
	return b.String()
}

func childExprSource(c childNode) string {
	if c.isText {
		return fmt.Sprintf("xango.Text(%s)", strconv.Quote(c.text))
	}
	return c.expr
}

func (p *parser) renderTag(name, typeArgs string, attrs []attr, children []childNode) string {
	if isComponentName(name) {
		return p.renderComponent(name, typeArgs, attrs, children)
	}
	return p.renderElement(name, attrs, children)
}

func (p *parser) renderElement(name string, attrs []attr, children []childNode) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("xango.El(%s, ", strconv.Quote(name)))
	if len(attrs) == 0 {
		b.WriteString("nil")
	} else {
		b.WriteString("[]xango.Attr{")
		for i, a := range attrs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(attrExprSource(a))
		}
		b.WriteString("}")
	}
	for _, c := range children {
		b.WriteString(", ")
		b.WriteString(childExprSource(c))
	}
	b.WriteString(")")
	return b.String()
}

func attrExprSource(a attr) string {
	if isEventAttr(a.name) {
		if !a.isExpr {
			return fmt.Sprintf("xango.On(%s, nil /* event attrs need {handlerExpr} */)", strconv.Quote(eventNameFor(a.name)))
		}
		return fmt.Sprintf("xango.On(%s, %s)", strconv.Quote(eventNameFor(a.name)), a.expr)
	}
	switch {
	case a.bare:
		return fmt.Sprintf("xango.A(%s, %s)", strconv.Quote(a.name), strconv.Quote(a.name))
	case a.isExpr:
		return fmt.Sprintf("xango.A(%s, xango.AttrVal(%s))", strconv.Quote(a.name), a.expr)
	default:
		return fmt.Sprintf("xango.A(%s, %s)", strconv.Quote(a.name), strconv.Quote(a.literal))
	}
}

func (p *parser) renderComponent(name, typeArgs string, attrs []attr, children []childNode) string {
	callName := name
	propsType := name + "Props"
	if idx := strings.LastIndexByte(name, '.'); idx >= 0 {
		pkg, last := name[:idx+1], name[idx+1:]
		propsType = pkg + last + "Props"
	}
	var fields []string
	for _, a := range attrs {
		field := capitalizeFirst(a.name)
		switch {
		case a.bare:
			fields = append(fields, fmt.Sprintf("%s: true", field))
		case a.isExpr:
			fields = append(fields, fmt.Sprintf("%s: %s", field, a.expr))
		default:
			fields = append(fields, fmt.Sprintf("%s: %s", field, strconv.Quote(a.literal)))
		}
	}
	if len(children) > 0 {
		childExprs := make([]string, len(children))
		for i, c := range children {
			childExprs[i] = childExprSource(c)
		}
		fields = append(fields, fmt.Sprintf("Children: []xango.Node{%s}", strings.Join(childExprs, ", ")))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s(%s%s{%s})", callName, typeArgs, propsType, typeArgs, strings.Join(fields, ", ")))
	return b.String()
}

func findMatchingBracket(chunk []rune, open int) (int, error) {
	depth := 0
	for i := open; i < len(chunk); i++ {
		switch chunk[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, nil
			}
		case '\n', '<', '>', '{', '}':
			return 0, fmt.Errorf("unexpected %q (type arguments look like [Item] or [string, map[string]int])", chunk[i])
		}
	}
	return 0, fmt.Errorf("missing ']'")
}
