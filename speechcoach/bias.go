package speechcoach

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type Vocabulary struct {
	terms []string
}

const (
	MaxTerms   = 500
	MaxTermLen = 60 // runes
)

// Terms lists the entries, oldest first.
func (v *Vocabulary) Terms() []string { return append([]string(nil), v.terms...) }

func (v *Vocabulary) Add(term string) bool {
	term = strings.Join(strings.Fields(cleanLine(term)), " ")
	if term == "" || utf8.RuneCountInString(term) > MaxTermLen || len(v.terms) >= MaxTerms {
		return false
	}
	for _, t := range v.terms {
		if strings.EqualFold(t, term) {
			return false
		}
	}
	v.terms = append(v.terms, term)
	return true
}

func (v *Vocabulary) Remove(term string) bool {
	for i, t := range v.terms {
		if strings.EqualFold(t, strings.TrimSpace(term)) {
			v.terms = append(v.terms[:i], v.terms[i+1:]...)
			return true
		}
	}
	return false
}

func (v *Vocabulary) Prompt(c *Corrections, maxBytes int) string {
	var all []string
	for i := len(v.terms) - 1; i >= 0; i-- {
		all = append(all, v.terms[i])
	}
	if c != nil {
		all = append(all, c.Words()...)
	}
	seen := map[string]bool{}
	var keep []string
	size := 0
	for _, t := range all {
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		add := len(t)
		if len(keep) > 0 {
			add += 2
		}
		if size+add > maxBytes {
			continue
		}
		size += add
		keep = append(keep, t)
	}
	for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 {
		keep[i], keep[j] = keep[j], keep[i]
	}
	return strings.Join(keep, ", ")
}

// Marshal saves the list.
func (v *Vocabulary) Marshal() ([]byte, error) { return json.Marshal(v.terms) }

func UnmarshalVocabulary(b []byte) (*Vocabulary, error) {
	var ts []string
	if err := json.Unmarshal(b, &ts); err != nil {
		return nil, err
	}
	v := &Vocabulary{}
	for _, t := range ts {
		v.Add(t)
	}
	return v, nil
}
