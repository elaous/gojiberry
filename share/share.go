package share

import (
	"net/url"
	"strconv"
	"strings"
)

const QuoteRunes = 80

type Selection struct {
	From, To int
	Quote    string
}

// Of describes the range [from, to) of text.
func Of(text string, from, to int) Selection {
	rs := []rune(text)
	from, to = max(from, 0), min(to, len(rs))
	if to <= from {
		return Selection{}
	}
	q := rs[from:to]
	if len(q) > QuoteRunes {
		q = q[:QuoteRunes]
	}
	return Selection{From: from, To: to, Quote: string(q)}
}

// Empty reports whether nothing is selected.
func (s Selection) Empty() bool { return s.To <= s.From }

// Query is the selection as URL parameters: sel and q.
func (s Selection) Query() url.Values {
	v := url.Values{}
	if !s.Empty() {
		v.Set("sel", strconv.Itoa(s.From)+"-"+strconv.Itoa(s.To))
		v.Set("q", s.Quote)
	}
	return v
}

func Parse(v url.Values) (Selection, bool) {
	a, b, found := strings.Cut(v.Get("sel"), "-")
	if !found {
		return Selection{}, false
	}
	from, err1 := strconv.Atoi(a)
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || from < 0 || to <= from {
		return Selection{}, false
	}
	return Selection{From: from, To: to, Quote: v.Get("q")}, true
}

const DocSegment = "d"

func SplitPath(pathname string) (base, doc string) { return splitSegment(pathname, DocSegment) }

// FolderSegment is the path segment before a folder's id: https://host/app/f/{folder}.
const FolderSegment = "f"

// SplitFolderPath is SplitPath for /f/{folder}.
func SplitFolderPath(pathname string) (base, folder string) {
	return splitSegment(pathname, FolderSegment)
}

func splitSegment(pathname, segment string) (base, id string) {
	p := strings.TrimSuffix(pathname, "/")
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return pathname, ""
	}
	if j := strings.LastIndex(p[:i], "/"); j >= 0 && p[j+1:i] == segment && p[i+1:] != "" {
		id, err := url.PathUnescape(p[i+1:])
		if err != nil {
			return pathname, ""
		}
		return p[:j+1], id
	}
	return pathname, ""
}

func FolderPath(base, folder string) string {
	return strings.TrimSuffix(base, "/") + "/" + FolderSegment + "/" + url.PathEscape(folder)
}

func DocPath(base, doc string) string {
	return strings.TrimSuffix(base, "/") + "/" + DocSegment + "/" + url.PathEscape(doc)
}

// Link is base with params and the selection added.
func Link(base string, params url.Values, s Selection) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	if doc := params.Get("doc"); doc != "" {
		root, _ := SplitPath(u.EscapedPath())
		escaped := DocPath(root, doc)
		u.Path, _ = url.PathUnescape(escaped) // Path is decoded; RawPath keeps the escaping
		u.RawPath = escaped
		params = cloneWithout(params, "doc")
	}
	q := u.Query()
	for k, vs := range params {
		q[k] = vs
	}
	for k, vs := range s.Query() {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s Selection) Resolve(text string) (from, to int, ok bool) {
	rs := []rune(text)
	if s.Empty() || s.From >= len(rs) && s.Quote == "" {
		return 0, 0, false
	}
	n := s.To - s.From
	clamp := func(f int) (int, int, bool) { return f, min(f+n, len(rs)), true }
	q := []rune(s.Quote)
	if len(q) == 0 {
		return clamp(s.From)
	}
	at := func(i int) bool {
		return i >= 0 && i+len(q) <= len(rs) && string(rs[i:i+len(q)]) == s.Quote
	}
	if at(s.From) {
		return clamp(s.From)
	}
	best := -1
	for i := 0; i+len(q) <= len(rs); i++ {
		if at(i) && (best < 0 || abs(i-s.From) < abs(best-s.From)) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return clamp(best)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func cloneWithout(v url.Values, key string) url.Values {
	out := url.Values{}
	for k, vs := range v {
		if k != key {
			out[k] = vs
		}
	}
	return out
}
