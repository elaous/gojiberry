package crdt

import "sort"

type Side uint8

const (
	// Before is the gap in front of the anchor's character.
	Before Side = iota
	// After is the gap behind it.
	After
)

type Anchor struct {
	ID   Timestamp
	Side Side
}

type Mark struct {
	ID         Timestamp
	Key, Value string
	Start, End Anchor
}

type Run struct {
	Text string
	// Attrs holds the keys whose winning mark has a value.
	Attrs map[string]string
}

func (t *Text) addMark(m Mark) {
	if t.markIDs[m.ID] {
		return
	}
	if t.markIDs == nil {
		t.markIDs = make(map[Timestamp]bool)
	}
	t.markIDs[m.ID] = true
	t.marks = append(t.marks, m)
}

func (t *Text) gap(a Anchor) (int, bool) {
	if a.ID == (Timestamp{}) {
		if a.Side == After {
			return 0, true
		}
		return len(t.chars), true
	}
	i := t.indexOf(a.ID)
	if i < 0 {
		return 0, false
	}
	if a.Side == After {
		return i + 1, true
	}
	return i, true
}

func (t *Text) anchorsFor(from, to int, growEnd bool) (start, end Anchor, ok bool) {
	_, ids := t.visible()
	if from < 0 || to > len(ids) || from >= to {
		return Anchor{}, Anchor{}, false
	}
	start = Anchor{ID: ids[from], Side: Before}
	switch {
	case !growEnd:
		end = Anchor{ID: ids[to-1], Side: After}
	case to < len(ids):
		end = Anchor{ID: ids[to], Side: Before}
	default:
		end = Anchor{Side: Before} // the end of the text
	}
	return start, end, true
}

type resolved struct {
	m          Mark
	start, end int
}

func (t *Text) Runs() []Run {
	var marks []resolved
	for _, m := range t.marks {
		s, ok1 := t.gap(m.Start)
		e, ok2 := t.gap(m.End)
		if ok1 && ok2 && s < e {
			marks = append(marks, resolved{m, s, e})
		}
	}
	starts := make(map[int][]int)
	ends := make(map[int][]int)
	for i, r := range marks {
		starts[r.start] = append(starts[r.start], i)
		ends[r.end] = append(ends[r.end], i)
	}
	active := map[string][]int{} // key -> marks covering the current character
	var attrs map[string]string
	dirty := true
	var runs []Run
	var cur []rune
	var curAttrs map[string]string
	flush := func() {
		if len(cur) > 0 {
			runs = append(runs, Run{Text: string(cur), Attrs: curAttrs})
			cur = nil
		}
	}
	for i := 0; i <= len(t.chars); i++ {
		for _, mi := range ends[i] {
			k := marks[mi].m.Key
			list := active[k]
			for j, x := range list {
				if x == mi {
					active[k] = append(list[:j], list[j+1:]...)
					break
				}
			}
			dirty = true
		}
		for _, mi := range starts[i] {
			k := marks[mi].m.Key
			active[k] = append(active[k], mi)
			dirty = true
		}
		if i == len(t.chars) {
			break
		}
		if dirty {
			attrs = map[string]string{}
			for k, list := range active {
				var best *Mark
				for _, mi := range list {
					if m := &marks[mi].m; best == nil || m.ID.after(best.ID) {
						best = m
					}
				}
				if best != nil && best.Value != "" {
					attrs[k] = best.Value
				}
			}
			dirty = false
		}
		c := t.chars[i]
		if c.deleted {
			continue
		}
		if len(cur) > 0 && !sameAttrs(curAttrs, attrs) {
			flush()
		}
		if len(cur) == 0 {
			curAttrs = attrs
		}
		cur = append(cur, c.value)
	}
	flush()
	return runs
}

func sameAttrs(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// MarkKeys returns the keys of marks in the text.
func (t *Text) MarkKeys() []string {
	seen := map[string]bool{}
	for _, m := range t.marks {
		seen[m.Key] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
