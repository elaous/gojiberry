package docximport

import (
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
)

const commentPrefix = "w"

func (r *reader) loadComments() {
	cs := r.xml("word/comments.xml")
	if cs == nil {
		return
	}
	r.comments = map[string]bool{}
	type entry struct {
		c        docfile.Comment
		paraID   string // the w14:paraId of the comment's last paragraph
		reply    bool
		parentID string // paraId of the comment it answers
		done     bool
	}
	var entries []*entry
	byPara := map[string]*entry{}
	for _, n := range cs.children("comment") {
		id := n.attr("id")
		if id == "" {
			continue
		}
		var paras []string
		var last *node
		for _, p := range n.children("p") {
			paras = append(paras, strings.TrimSpace(plainText(p)))
			last = p
		}
		e := &entry{c: docfile.Comment{ID: commentPrefix + id, Author: n.attr("author"), Text: strings.TrimSpace(strings.Join(paras, "\n"))}}
		if d, err := time.Parse(time.RFC3339, n.attr("date")); err == nil {
			e.c.Created = d.UnixNano()
		}
		if last != nil {
			e.paraID = last.attr("paraId")
		}
		entries = append(entries, e)
		if e.paraID != "" {
			byPara[e.paraID] = e
		}
	}
	if ext := r.xml("word/commentsExtended.xml"); ext != nil {
		for _, x := range ext.children("commentEx") {
			e := byPara[x.attr("paraId")]
			if e == nil {
				continue
			}
			e.done = x.attr("done") == "1"
			e.parentID = x.attr("paraIdParent")
		}
	}
	for _, e := range entries {
		if e.parentID != "" {
			if parent := byPara[e.parentID]; parent != nil && parent != e {
				e.reply = true
			}
		}
	}
	idx := map[*entry]int{} // the position of each top-level comment in r.doc.Comments
	for _, e := range entries {
		if !e.reply {
			e.c.Resolved = e.done
			idx[e] = len(r.doc.Comments)
			r.doc.Comments = append(r.doc.Comments, e.c)
		}
	}
	for _, e := range entries {
		if !e.reply {
			r.comments[e.c.ID] = true
			continue
		}
		root := byPara[e.parentID]
		for root.reply && byPara[root.parentID] != nil && byPara[root.parentID] != root { // a reply to a reply: the thread's first comment
			root = byPara[root.parentID]
		}
		i, ok := idx[root]
		if !ok {
			continue
		}
		rep := docfile.Reply{Author: e.c.Author, Text: e.c.Text, Created: e.c.Created}
		r.doc.Comments[i].Replies = append(r.doc.Comments[i].Replies, rep)
	}
}

func (r *reader) openComment(id string) {
	if id != "" && r.comments[commentPrefix+id] {
		r.openComments = append(r.openComments, commentPrefix+id)
	}
}

func (r *reader) closeComment(id string) {
	want := commentPrefix + id
	for i := len(r.openComments) - 1; i >= 0; i-- {
		if r.openComments[i] == want {
			r.openComments = append(r.openComments[:i], r.openComments[i+1:]...)
			return
		}
	}
}

func (r *reader) currentComment() string {
	if n := len(r.openComments); n > 0 {
		return r.openComments[n-1]
	}
	return ""
}
