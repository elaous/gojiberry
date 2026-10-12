package speechcoach

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Vocabulary is the person's own list of words and short phrases they say often (names, places, terms,
// the way they greet someone). It biases a recogniser toward those spellings by being sent as its initial
// prompt (whisper.cpp's --prompt, voice/listen.Options.Prompt); nothing is trained. A prompt is a hint,
// not a guarantee: it helps with names and rare words and does not teach a recogniser a new way of
// speaking.
type Vocabulary struct {
	terms []string
}

const (
	MaxTerms   = 500
	MaxTermLen = 60 // runes
)

// Terms lists the entries, oldest first.
func (v *Vocabulary) Terms() []string { return append([]string(nil), v.terms...) }

// Add keeps a term (trimmed, control characters dropped, no duplicates ignoring case). It reports whether
// the list changed; a term that is empty or too long, or a full list, is refused.
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

// Remove deletes a term (ignoring case); it reports whether there was one.
func (v *Vocabulary) Remove(term string) bool {
	for i, t := range v.terms {
		if strings.EqualFold(t, strings.TrimSpace(term)) {
			v.terms = append(v.terms[:i], v.terms[i+1:]...)
			return true
		}
	}
	return false
}

// Prompt builds the initial prompt: the terms, then the words from the correction memory, separated by
// commas, within maxBytes (a whole term is never cut; ones that do not fit are left out). The newest
// terms are chosen first when space runs out, then the correction words by how often they were chosen;
// the text is written oldest to newest.
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

// UnmarshalVocabulary reads what Marshal wrote, keeping only the terms Add would take.
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
