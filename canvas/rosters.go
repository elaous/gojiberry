package canvas

import (
	"fmt"
	"strings"
)

// Roster says who has access, so a comment can name a person safely.
type Roster interface {
	// HandleOf gives the handle (without the @) of the person with this e-mail address, if they have access.
	HandleOf(address string) (handle string, ok bool)
	// Has says whether a handle is that of someone with access.
	Has(handle string) bool
}

// MentionProblemKind says what is wrong with a mention.
type MentionProblemKind int

const (
	// MentionNoAccess: an e-mail address of someone without access.
	MentionNoAccess MentionProblemKind = iota + 1
	// MentionUnknown: a @handle nobody with access has; nobody is told.
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

// CheckMentions returns the mention problems in text against the roster.
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

// hideAddresses replaces addresses of people with access by their handles.
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

// Visible lists comments for readers, without addresses when rostered.
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
