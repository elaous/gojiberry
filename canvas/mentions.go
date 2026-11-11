package canvas

import (
	"strconv"
	"strings"
	"time"
)

// MentionNotice is one mention of a person by handle in a comment or reply.
type MentionNotice struct {
	CommentID string
	Reply     int // reply index, or -1 for the comment itself.
	Author    string
	Text      string
	Created   time.Time
}

// Key identifies the notice so a host can skip shown ones.
func (n MentionNotice) Key() string { return n.CommentID + "#" + strconv.Itoa(n.Reply) }

// MentionsOf lists comments and replies that mention handle, oldest first.
func (s *CommentSet) MentionsOf(handle string) []MentionNotice {
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if s == nil || handle == "" {
		return nil
	}
	own := func(author string) bool {
		if strings.EqualFold(strings.TrimPrefix(author, "@"), handle) {
			return true
		}
		if s.Roster != nil {
			if h, ok := s.Roster.HandleOf(author); ok && strings.EqualFold(h, handle) {
				return true
			}
		}
		return false
	}
	who := func(author string) string { // the handle, not the address, when the roster knows the person
		if s.Roster != nil {
			if h, ok := s.Roster.HandleOf(author); ok {
				return h
			}
		}
		return author
	}
	mentions := func(text string) bool {
		for _, m := range Mentions(text) {
			if m.Kind == MentionUser && strings.EqualFold(m.Name, handle) {
				return true
			}
		}
		return false
	}
	var out []MentionNotice
	for _, c := range s.Visible() {
		if !own(c.Author) && mentions(c.Text) {
			out = append(out, MentionNotice{CommentID: c.ID, Reply: -1, Author: who(c.Author), Text: c.Text, Created: c.Created})
		}
		for i, r := range c.Replies {
			if !own(r.Author) && mentions(r.Text) {
				out = append(out, MentionNotice{CommentID: c.ID, Reply: i, Author: who(r.Author), Text: r.Text, Created: r.Created})
			}
		}
	}
	return out
}

// NewMentions is MentionsOf without notices whose Key is in done.
func (s *CommentSet) NewMentions(handle string, done map[string]bool) []MentionNotice {
	var out []MentionNotice
	for _, n := range s.MentionsOf(handle) {
		if !done[n.Key()] {
			out = append(out, n)
		}
	}
	return out
}
