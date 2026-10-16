package canvas

import (
	"strconv"
	"strings"
	"time"
)

// A MentionNotice is one place a person is mentioned by handle (@ana) in a comment or a reply written by somebody
// else. It carries no address and no document text beyond the comment itself, so a host can deliver it by any channel
// it chooses (a list in the page, a push, a mail) without the canvas knowing which.
type MentionNotice struct {
	CommentID string
	Reply     int // the index of the reply, or -1 when the comment itself mentions
	Author    string
	Text      string
	Created   time.Time
}

// Key identifies the notice, so a host can remember which ones it has already shown or sent.
func (n MentionNotice) Key() string { return n.CommentID + "#" + strconv.Itoa(n.Reply) }

// MentionsOf lists the comments and replies that mention handle, oldest first, leaving out the ones the person wrote
// themselves (an author is the person when it is their handle or, with a roster, an address the roster maps to it). With a roster, Author is the handle.
// Text is the comment as a reader sees it (no address).
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

// NewMentions is MentionsOf without the notices whose Key is in done.
func (s *CommentSet) NewMentions(handle string, done map[string]bool) []MentionNotice {
	var out []MentionNotice
	for _, n := range s.MentionsOf(handle) {
		if !done[n.Key()] {
			out = append(out, n)
		}
	}
	return out
}
