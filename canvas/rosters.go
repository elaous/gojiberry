package canvas

import (
	"fmt"
	"strings"
)

// A Roster says who has access to the document, so a comment can name a person without writing down the address
// of someone who may not see it (roadmap 71, step 3). A comment is part of the document: whoever can read it reads
// every word of it, and an e-mail address typed into it would reach all of them, and everyone the document is
// later shared with, whether or not the person it belongs to ever agreed to that.
//
// With a roster on the CommentSet, a mention by e-mail address is written down as the person's handle (@ana) when
// the address belongs to someone with access, so the address itself is never in the document; it is refused when
// it belongs to nobody with access; and a comment that came from somewhere else with an address in it is shown
// with the address hidden. Without a roster nothing changes: the text is kept as it was written.
type Roster interface {
	// HandleOf gives the handle (without the @) of the person with this e-mail address, if they have access.
	HandleOf(address string) (handle string, ok bool)
	// Has says whether a handle is that of someone with access.
	Has(handle string) bool
}

// MentionProblemKind says what is wrong with a mention.
type MentionProblemKind int

const (
	// MentionNoAccess is an e-mail address of someone who has no access to the document: writing it would show it
	// to everyone who reads the comment. The comment is not saved until it is taken out.
	MentionNoAccess MentionProblemKind = iota + 1
	// MentionUnknown is a @handle that is nobody's with access: nobody is told. It is a hint, not a refusal.
	MentionUnknown
)

// A MentionProblem is a mention the roster does not know.
type MentionProblem struct {
	Mention Mention
	Kind    MentionProblemKind
}

// Blocks says whether the problem keeps the comment from being saved.
func (p MentionProblem) Blocks() bool { return p.Kind == MentionNoAccess }

// Message says what is wrong, in words, and what to do.
func (p MentionProblem) Message() string {
	if p.Kind == MentionNoAccess {
		return fmt.Sprintf("%s has no access to this document, so writing the address would show it to everyone who reads the comment. Share the document with them first, or write their name instead.", p.Mention.Text)
	}
	return fmt.Sprintf("%s is nobody who has access to this document, so no one will be told.", p.Mention.Text)
}

// CheckMentions reads the mentions in text against the set's roster. clean is the text to save: each e-mail
// address of a person with access is replaced by their @handle. problems lists the mentions the roster does not
// know, in order; the text of a blocking one is left as written, for the caller to ask the author to change.
// With no roster the text is returned as it is and there are no problems.
func (s *CommentSet) CheckMentions(text string) (clean string, problems []MentionProblem) {
	if s == nil || s.Roster == nil {
		return text, nil
	}
	var b strings.Builder
	last := 0
	for _, m := range Mentions(text) {
		switch m.Kind {
		case MentionEmail:
			if h, ok := s.Roster.HandleOf(m.Name); ok {
				b.WriteString(text[last:m.Start])
				b.WriteString("@" + h)
				last = m.End
			} else {
				problems = append(problems, MentionProblem{Mention: m, Kind: MentionNoAccess})
			}
		case MentionUser:
			if !s.Roster.Has(m.Name) {
				problems = append(problems, MentionProblem{Mention: m, Kind: MentionUnknown})
			}
		}
	}
	b.WriteString(text[last:])
	return b.String(), problems
}

// BlockingMention returns the first problem that keeps a comment from being saved.
func BlockingMention(problems []MentionProblem) (MentionProblem, bool) {
	for _, p := range problems {
		if p.Blocks() {
			return p, true
		}
	}
	return MentionProblem{}, false
}

// hideAddresses writes text for a reader: an address of a person with access becomes their @handle, any other
// address becomes @someone.
func (s *CommentSet) hideAddresses(text string) string {
	var b strings.Builder
	last := 0
	for _, m := range Mentions(text) {
		if m.Kind != MentionEmail {
			continue
		}
		b.WriteString(text[last:m.Start])
		if h, ok := s.Roster.HandleOf(m.Name); ok {
			b.WriteString("@" + h)
		} else {
			b.WriteString("@someone")
		}
		last = m.End
	}
	b.WriteString(text[last:])
	return b.String()
}

// Visible lists the comments as a reader should see them: with a roster, no e-mail address is shown in a comment
// or a reply (see Roster). The comments themselves are not changed.
func (s *CommentSet) Visible() []Comment {
	list := s.List()
	if s == nil || s.Roster == nil {
		return list
	}
	for i := range list {
		list[i].Text = s.hideAddresses(list[i].Text)
		replies := make([]Reply, len(list[i].Replies))
		for j, r := range list[i].Replies {
			r.Text = s.hideAddresses(r.Text)
			replies[j] = r
		}
		list[i].Replies = replies
	}
	return list
}
