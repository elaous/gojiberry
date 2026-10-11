package canvas

import (
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Proposals: proposed changes to the text, which stay in the text until someone accepts or rejects
// them (suggestion mode, "track changes"). A suggestion is carried by the characters it concerns, in
// Format.Proposal, so it survives everything the text survives (concurrent edits, copies of the
// document, saving) with no list kept apart from the text that could fall out of step with it:
//
//	i:<id>:<unix seconds>:<author>   text proposed to be added
//	d:<id>:<unix seconds>:<author>   text proposed to be removed (it is still there, struck through)
//
// A change that replaces text is one suggestion: the same id on the struck text and the added text.
// Accepting makes the change (the added text becomes ordinary text, the struck text goes); rejecting
// undoes it (the added text goes, the struck text becomes ordinary again).

// ProposalKind says whether marked text is proposed to be added or removed.
type ProposalKind int

const (
	ProposalInsert ProposalKind = iota
	ProposalDelete
)

// ProposalMark is a parsed Format.Proposal.
type ProposalMark struct {
	Kind   ProposalKind
	ID     string
	Author string
	Time   time.Time
}

// String is the Format.Proposal value of the mark.
func (m ProposalMark) String() string {
	k := "i"
	if m.Kind == ProposalDelete {
		k = "d"
	}
	return k + ":" + m.ID + ":" + strconv.FormatInt(m.Time.Unix(), 10) + ":" + url.QueryEscape(m.Author)
}

// ParseProposalMark reads a Format.Proposal value.
func ParseProposalMark(s string) (ProposalMark, bool) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 || parts[1] == "" {
		return ProposalMark{}, false
	}
	var m ProposalMark
	switch parts[0] {
	case "i":
		m.Kind = ProposalInsert
	case "d":
		m.Kind = ProposalDelete
	default:
		return ProposalMark{}, false
	}
	secs, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return ProposalMark{}, false
	}
	author, err := url.QueryUnescape(parts[3])
	if err != nil {
		return ProposalMark{}, false
	}
	m.ID, m.Author, m.Time = parts[1], author, time.Unix(secs, 0)
	return m, true
}

// proposalIDs and proposalClock make the ids and times of new suggestions; tests replace them.
var (
	proposalID = func() string {
		var b [6]byte
		_, _ = rand.Read(b[:])
		return hex.EncodeToString(b[:])
	}
	proposalClock = time.Now
)

// proposalColors are the colors the suggestions of different people are drawn in.
var proposalColors = []string{"#b3261e", "#1a5fb4", "#1a7f37", "#9a6700", "#8250df", "#0e7490"}

// ProposalColor is the color of a person's suggestions: one of a few, chosen by their name, so the same
// person always has the same color.
func ProposalColor(author string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(author))
	return proposalColors[int(h.Sum32())%len(proposalColors)]
}

// proposalStyle draws suggested text: added text underlined, removed text struck through, both in the
// color of whoever suggested it.
func proposalStyle(st *TextStyle, value string) {
	m, ok := ParseProposalMark(value)
	if !ok {
		return
	}
	st.Color = ProposalColor(m.Author)
	if m.Kind == ProposalDelete {
		st.Strike = true
	} else {
		st.Underline = true
	}
}

// Proposal is one proposed change as the text has it.
type Proposal struct {
	ID     string
	Author string
	Time   time.Time
	// Inserted is the text proposed to be added and Deleted the text proposed to be removed; either may be empty
	// (a pure addition or removal), both set is a replacement.
	Inserted, Deleted string
	// From and To are the offsets of the first and the end of the last character the suggestion marks.
	From, To int
}

// Proposals lists the proposed changes in the order they start in the text.
func (f *TextFlow) Proposals() []Proposal {
	d := f.flatten()
	byID := map[string]*Proposal{}
	var order []string
	for i := 0; i < len(d.runes); i++ {
		if d.isObj(i) {
			continue
		}
		m, ok := ParseProposalMark(d.formats[d.rs[i]].Proposal)
		if !ok {
			continue
		}
		s := byID[m.ID]
		if s == nil {
			s = &Proposal{ID: m.ID, Author: m.Author, Time: m.Time, From: i}
			byID[m.ID] = s
			order = append(order, m.ID)
		}
		s.To = i + 1
		if m.Kind == ProposalDelete {
			s.Deleted += string(d.runes[i])
		} else {
			s.Inserted += string(d.runes[i])
		}
	}
	out := make([]Proposal, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// AcceptProposal makes the change: the added text becomes ordinary text and the struck text is removed. It
// reports whether there was such a suggestion. It is one change in the undo history.
func (f *TextFlow) AcceptProposal(id string) bool { return f.resolveProposals(id, true) != 0 }

// RejectProposal undoes the change: the added text is removed and the struck text becomes ordinary text.
func (f *TextFlow) RejectProposal(id string) bool { return f.resolveProposals(id, false) != 0 }

// AcceptAllProposals accepts every suggestion and returns how many there were.
func (f *TextFlow) AcceptAllProposals() int { return f.resolveProposals("", true) }

// RejectAllProposals rejects every suggestion and returns how many there were.
func (f *TextFlow) RejectAllProposals() int { return f.resolveProposals("", false) }

// resolveProposals accepts or rejects the suggestion with this id ("" for all of them) and returns how
// many suggestions it settled.
func (f *TextFlow) resolveProposals(id string, accept bool) int {
	d := f.flatten()
	var edits []Edit
	settled := map[string]bool{}
	for i := 0; i < len(d.runes); {
		m, ok := ParseProposalMark(d.formats[d.rs[i]].Proposal)
		if d.isObj(i) || !ok || (id != "" && m.ID != id) {
			i++
			continue
		}
		// the run of characters with this very mark
		j := i + 1
		for j < len(d.runes) && !d.isObj(j) && d.formats[d.rs[j]].Proposal == d.formats[d.rs[i]].Proposal {
			j++
		}
		settled[m.ID] = true
		remove := (m.Kind == ProposalInsert) != accept // accepted deletions go, rejected insertions go
		if remove {
			edits = append(edits, Edit{From: i, To: j})
		} else {
			var items []Item
			for _, it := range f.Slice(i, j) {
				if c, ok := it.(Chars); ok {
					c.Format.Proposal = ""
					it = c
				}
				items = append(items, it)
			}
			edits = append(edits, Edit{From: i, To: j, Items: items})
		}
		i = j
	}
	if len(edits) == 0 {
		return 0
	}
	f.Replace(edits)
	return len(settled)
}

// proposalsOn reports whether edits made now are proposals.
func (f *TextFlow) proposalsOn() bool { return f.Suggesting && f.Editing() }

// proposalRewrite turns an edit [from, to) -> ins, made by cursor c, into the proposal of it: the removed
// text stays, struck through (text that was itself a suggested addition is really removed), and the
// added text follows it, marked as an addition. It returns the items that replace [from, to) and
// where the caret goes, as an offset into them.
func (f *TextFlow) proposalRewrite(d *flowData, c Cursor, from, to int, ins []Item) (repl []Item, caret int) {
	now := proposalClock()
	author := f.Author
	id := ""
	del := func() string { // the mark of the text struck by this edit
		if id == "" {
			id = proposalID()
		}
		return ProposalMark{Kind: ProposalDelete, ID: id, Author: author, Time: now}.String()
	}
	struck := false
	for _, it := range f.Slice(from, to) {
		ch, ok := it.(Chars)
		if !ok {
			repl = append(repl, it) // paragraph settings and objects stay as they are
			continue
		}
		switch m, marked := ParseProposalMark(ch.Format.Proposal); {
		case marked && m.Kind == ProposalInsert:
			// a proposed addition that is deleted is simply not proposed any more
		case marked:
			repl = append(repl, ch) // already struck
		default:
			ch.Format.Proposal = del()
			repl = append(repl, ch)
			struck = true
		}
	}
	kept := itemsLen(repl)
	insLen := itemsLen(ins)
	if insLen > 0 {
		mark := ""
		switch {
		case struck:
			mark = ProposalMark{Kind: ProposalInsert, ID: id, Author: author, Time: now}.String()
		default:
			// text typed right after the person's own proposed addition continues it
			if prev, ok := f.markBefore(d, from); ok && prev.Kind == ProposalInsert && prev.Author == author {
				mark = ProposalMark{Kind: ProposalInsert, ID: prev.ID, Author: author, Time: prev.Time}.String()
			} else {
				mark = ProposalMark{Kind: ProposalInsert, ID: proposalID(), Author: author, Time: now}.String()
			}
		}
		for _, it := range ins {
			if ch, ok := it.(Chars); ok {
				ch.Format.Proposal = mark
				it = ch
			}
			repl = append(repl, it)
		}
		return repl, kept + insLen
	}
	if c.Caret >= to {
		return repl, 0 // backspace, or a selection made forward: the caret goes to its start
	}
	return repl, kept // the Delete key: the caret goes past what was struck
}

// markBefore is the suggestion mark of the character before offset o.
func (f *TextFlow) markBefore(d *flowData, o int) (ProposalMark, bool) {
	if o <= 0 || o > len(d.runes) || d.isObj(o-1) {
		return ProposalMark{}, false
	}
	return ParseProposalMark(d.formats[d.rs[o-1]].Proposal)
}

// proposalEdits is editCursors for suggestion mode.
func (f *TextFlow) proposalEdits(gen func(c Cursor, rs []rune) (from, to int, ins []Item)) {
	rs := textRunes(f)
	d := f.flatten()
	var edits []Edit
	var out []Cursor
	shift := 0
	for _, c := range normalize(f.Cursors()) {
		from, to, ins := gen(c, rs)
		repl, caret := f.proposalRewrite(d, c, from, to, ins)
		edits = append(edits, Edit{From: from, To: to, Items: repl, AfterOps: c.After && from == to})
		out = append(out, At(from+shift+caret))
		shift += itemsLen(repl) - (to - from)
	}
	f.Replace(edits)
	f.SetCursors(out)
}
