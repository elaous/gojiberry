package collab

import (
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

// conflictWindow is how long a paragraph counts as "being edited here" after the last local change in it.
const conflictWindow = 10 * time.Second

// Conflict is a paragraph that another person changed while it was being edited here. The merge keeps both
// changes (characters are merged one by one), so nothing is lost; it is worth saying because the text the
// person is looking at moved under them.
type Conflict struct {
	// Paragraph is the paragraph as it was here just before the other change arrived.
	Paragraph string
	// Replicas are the replicas whose operations arrived (other than this one), in order.
	Replicas []string
}

type recentParagraph struct {
	text string
	at   time.Time
}

// now is replaced by tests.
var now = time.Now

// noteRecent remembers the paragraph around caret (an offset into runes) as just edited here.
func (b *Bridge) noteRecent(runes []rune, caret int) {
	p := paragraphAt(runes, caret)
	if strings.TrimSpace(p) == "" {
		return
	}
	t := now()
	b.recent = slices.DeleteFunc(b.recent, func(r recentParagraph) bool { return t.Sub(r.at) > conflictWindow })
	// the same paragraph, typed on, replaces its earlier form
	if n := len(b.recent); n > 0 && (strings.HasPrefix(p, b.recent[n-1].text) || strings.HasPrefix(b.recent[n-1].text, p)) {
		b.recent = b.recent[:n-1]
	}
	b.recent = append(b.recent, recentParagraph{p, t})
	if len(b.recent) > 8 {
		b.recent = b.recent[len(b.recent)-8:]
	}
}

// checkConflict compares the text before and after other people's operations were merged: a paragraph that
// was edited here lately and is no longer there as it was has been changed by them.
func (b *Bridge) checkConflict(before, after []atom, ops []crdt.Op) {
	if b.OnConflict == nil || len(b.recent) == 0 {
		return
	}
	t := now()
	var replicas []string
	add := func(r string) {
		if r != "" && r != b.replica && !slices.Contains(replicas, r) {
			replicas = append(replicas, r)
		}
	}
	for _, op := range ops {
		add(op.TS.Replica)
		for _, c := range op.CharOps {
			add(c.ID.Replica)
		}
	}
	if len(replicas) == 0 {
		return
	}
	have := map[string]int{}
	for _, p := range paragraphsOf(after) {
		have[p]++
	}
	for _, r := range b.recent {
		if t.Sub(r.at) > conflictWindow {
			continue
		}
		// the paragraph as it was here is still in the document: untouched by the others
		if have[r.text] > 0 {
			have[r.text]--
			continue
		}
		// it was here before they arrived (not already gone for another reason)
		if !slices.Contains(paragraphsOf(before), r.text) {
			continue
		}
		b.OnConflict(Conflict{Paragraph: r.text, Replicas: replicas})
		b.recent = nil
		return
	}
}

func paragraphsOf(atoms []atom) []string {
	rs := make([]rune, 0, len(atoms))
	for _, a := range atoms {
		rs = append(rs, a.r)
	}
	return strings.Split(string(rs), "\n")
}

// paragraphAt is the paragraph of runes around offset pos.
func paragraphAt(runes []rune, pos int) string {
	pos = max(0, min(pos, len(runes)))
	start, end := pos, pos
	for start > 0 && runes[start-1] != '\n' {
		start--
	}
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	return string(runes[start:end])
}
