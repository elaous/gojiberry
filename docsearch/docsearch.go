package docsearch

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Index holds the title and text of each document.
type Index struct {
	docs map[string]*doc
}

type doc struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	// Updated is the UnixNano of the stored version.
	Updated int64    `json:"updated,omitempty"`
	words   []string // lower-case words of the text, rebuilt on load
	twds    []string // and of the title
}

// Hit is one document that matches a query.
type Hit struct {
	ID    string
	Title string
	// Score ranks the hits, higher first.
	Score float64
	// Snippet is a few words of text around the match.
	Snippet string
}

func (ix *Index) Add(id, title, text string) {
	if ix.docs == nil {
		ix.docs = map[string]*doc{}
	}
	ix.docs[id] = newDoc(title, text)
}

func newDoc(title, text string) *doc {
	return &doc{Title: title, Text: text, words: words(text), twds: words(title)}
}

// Remove drops a document.
func (ix *Index) Remove(id string) { delete(ix.docs, id) }

// Len is how many documents are indexed.
func (ix *Index) Len() int { return len(ix.docs) }

func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(strings.Trim(string(cur), "'-’")))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '\'' || r == '’' || r == '-' {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return slices.DeleteFunc(out, func(w string) bool { return w == "" })
}

// Search returns the documents that contain every query word.
func (ix *Index) Search(query string, limit int) []Hit {
	terms := words(query)
	if len(terms) == 0 {
		return nil
	}
	var hits []Hit
	for id, d := range ix.docs {
		score := 0.0
		ok := true
		for i, t := range terms {
			last := i == len(terms)-1
			n, tn := count(d.words, t, last), count(d.twds, t, last)
			if n == 0 && tn == 0 {
				ok = false
				break
			}
			score += float64(n) + 5*float64(tn)
		}
		if ok {
			hits = append(hits, Hit{ID: id, Title: d.Title, Score: score, Snippet: snippet(d.Text, terms)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func count(ws []string, t string, prefix bool) int {
	n := 0
	for _, w := range ws {
		if w == t || (prefix && strings.HasPrefix(w, t)) {
			n++
		}
	}
	return n
}

func snippet(text string, terms []string) string {
	lower := []rune(strings.ToLower(text))
	rs := []rune(text)
	if len(lower) != len(rs) {
		lower = rs
	}
	at := -1
	for _, t := range terms {
		tr := []rune(t)
		for i := 0; i+len(tr) <= len(lower); i++ {
			if (i == 0 || !unicode.IsLetter(lower[i-1])) && string(lower[i:i+len(tr)]) == t {
				if at < 0 || i < at {
					at = i
				}
				break
			}
		}
	}
	if at < 0 {
		at = 0
	}
	from, to := max(0, at-25), min(len(rs), at+35)
	s := strings.Join(strings.Fields(string(rs[from:to])), " ")
	if from > 0 {
		s = "…" + s
	}
	if to < len(rs) {
		s += "…"
	}
	return s
}

// DeriveKey makes the 32-byte key that seals an index.
func DeriveKey(secret []byte, label string) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, nil, "gojiberry/docsearch/v1 "+label, 32)
}

var magic = []byte("GJSI1")

// Seal serializes the index and encrypts it.
func (ix *Index) Seal(key []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(ix.docs)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(slices.Clone(magic), nonce...)
	return aead.Seal(out, nonce, plain, magic), nil
}

// ErrCorrupt is returned by Open for damaged data.
var ErrCorrupt = errors.New("docsearch: the index cannot be opened")

// Open decrypts what Seal wrote.
func Open(key, sealed []byte) (*Index, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	n := len(magic) + aead.NonceSize()
	if len(sealed) < n+aead.Overhead() || string(sealed[:len(magic)]) != string(magic) {
		return nil, ErrCorrupt
	}
	plain, err := aead.Open(nil, sealed[len(magic):n], sealed[n:], magic)
	if err != nil {
		return nil, ErrCorrupt
	}
	var docs map[string]*doc
	if json.Unmarshal(plain, &docs) != nil {
		return nil, ErrCorrupt
	}
	ix := &Index{docs: map[string]*doc{}}
	for id, d := range docs {
		if d != nil {
			nd := newDoc(d.Title, d.Text)
			nd.Updated = d.Updated
			ix.docs[id] = nd
		}
	}
	return ix, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("docsearch: the key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
