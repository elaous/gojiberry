package collab

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docfile"
)

// Side data -- comments, their replies, footnote texts and named styles --
// is a set of small whole-value records, each its own crdt block merged
// last-writer-wins (so two people replying at once both keep their reply,
// but two edits of the same comment's text keep one):
//
//	comment:<id>        the comment without its replies
//	reply:<id>:<key>    one reply
//	note:<id>           a footnote's text
//	style:<name>        a named style
const sideKind = crdt.LWWKindPrefix + ":side"

// sideSync remembers each record's last agreed value, to send only changes.
type sideSync struct {
	synced map[string]string
}

func replyKey(r docfile.Reply) string {
	return fmt.Sprintf("%d-%s", r.Created, r.Author)
}

// desiredSide lists the records the flow holds now.
func (b *Bridge) desiredSide() map[string]string {
	out := map[string]string{}
	put := func(id string, v any) {
		j, _ := json.Marshal(v)
		out[id] = string(j)
	}
	for _, c := range docfile.FromComments(b.Flow.Comments) {
		replies := c.Replies
		c.Replies = nil
		put("comment:"+c.ID, c)
		for _, r := range replies {
			put("reply:"+c.ID+":"+replyKey(r), r)
		}
	}
	for _, n := range docfile.FromFootnotes(b.Flow.Footnotes) {
		put("note:"+n.ID, n)
	}
	for _, s := range docfile.FromStyles(b.Flow.Styles) {
		put("style:"+s.Name, s)
	}
	return out
}

func (b *Bridge) pushSide() {
	want := b.desiredSide()
	if b.side.synced == nil {
		b.side.synced = map[string]string{}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if b.side.synced[id] != want[id] {
			b.Doc.Put(crdt.Block{ID: id, Kind: sideKind, Content: want[id]})
			b.side.synced[id] = want[id]
		}
	}
	for id := range b.side.synced {
		if _, ok := want[id]; !ok {
			b.Doc.Delete(id)
			delete(b.side.synced, id)
		}
	}
}

// pullSide rebuilds the flow's comments, notes and styles from the
// replica's records.
func (b *Bridge) pullSide() {
	synced := map[string]string{}
	hadStyles := false
	for id := range b.side.synced {
		if strings.HasPrefix(id, "style:") {
			hadStyles = true
		}
	}
	var comments []docfile.Comment
	type pendingReply struct {
		rest string // "<comment id>:<key>"
		r    docfile.Reply
	}
	var pendingReplies []pendingReply
	var notes []docfile.Footnote
	var styles []docfile.StyleDef
	for _, blk := range b.Doc.Blocks() {
		if !crdt.LWWKind(blk.Kind) {
			continue
		}
		synced[blk.ID] = blk.Content
		switch kind, rest, _ := strings.Cut(blk.ID, ":"); kind {
		case "comment":
			var c docfile.Comment
			if json.Unmarshal([]byte(blk.Content), &c) == nil {
				comments = append(comments, c)
			}
		case "reply":
			var r docfile.Reply
			if json.Unmarshal([]byte(blk.Content), &r) == nil {
				pendingReplies = append(pendingReplies, pendingReply{rest, r})
			}
		case "note":
			var n docfile.Footnote
			if json.Unmarshal([]byte(blk.Content), &n) == nil {
				notes = append(notes, n)
			}
		case "style":
			var s docfile.StyleDef
			if json.Unmarshal([]byte(blk.Content), &s) == nil {
				styles = append(styles, s)
			}
		}
	}
	b.side.synced = synced
	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].Created != comments[j].Created {
			return comments[i].Created < comments[j].Created
		}
		return comments[i].ID < comments[j].ID
	})
	// A reply belongs to the comment whose id begins its key (the longest
	// such id: an id may itself contain a colon).
	replies := map[string][]docfile.Reply{}
	for _, pr := range pendingReplies {
		best := ""
		for _, c := range comments {
			if strings.HasPrefix(pr.rest, c.ID+":") && len(c.ID) >= len(best) {
				best = c.ID
			}
		}
		if best != "" {
			replies[best] = append(replies[best], pr.r)
		}
	}
	for i := range comments {
		rs := replies[comments[i].ID]
		sort.SliceStable(rs, func(a, c int) bool { return rs[a].Created < rs[c].Created })
		comments[i].Replies = rs
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	sort.SliceStable(styles, func(i, j int) bool { return styles[i].Name < styles[j].Name })

	f := b.Flow
	if f.Comments == nil {
		f.Comments = canvas.NewCommentSet()
	}
	f.Comments.ReplaceAll(docfile.ToComments(comments).List())
	if f.Footnotes == nil {
		f.Footnotes = canvas.NewFootnoteSet()
	}
	f.Footnotes.ReplaceAll(docfile.ToFootnotes(notes).List())
	if len(styles) > 0 || hadStyles {
		f.Styles = docfile.ToStyles(styles)
	}
}
