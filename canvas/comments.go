package canvas

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Comments are notes attached to a stretch of text. The text carries the
// comment's ID in Format.Comment (so the anchor follows the text through
// edits, like a link does); the comment itself -- author, Markdown body,
// replies -- is in a CommentSet. Hovering commented text shows a tip with
// the summary; the Comments feature adds, edits, resolves and deletes
// them.

// Reply is a follow-up to a comment.
type Reply struct {
	Author  string
	Text    string // Markdown
	Created time.Time
}

// Comment is a note on some text.
type Comment struct {
	ID string
	// Author is a user name or e-mail address.
	Author string
	// Text is Markdown, and may mention people with @username or an
	// e-mail address (see Mentions).
	Text     string
	Created  time.Time
	Resolved bool
	Replies  []Reply
}

// CommentSet holds a document's comments. A TextFlow's Comments field is
// one; share it with the DOM side to list them.
type CommentSet struct {
	// IDPrefix, when set, starts every ID Add makes ("ana-c3"). Replicas of a
	// shared document each use their own, so two people commenting at once
	// never mint the same ID.
	IDPrefix string
	// Roster, when set, keeps e-mail addresses out of comments (see Roster).
	Roster Roster

	list []Comment
	next int
}

// NewCommentSet returns a set holding comments.
func NewCommentSet(comments ...Comment) *CommentSet {
	s := &CommentSet{}
	for _, c := range comments {
		s.Add(c)
	}
	return s
}

// Add stores c, giving it an ID ("c1", "c2", ...) if it has none, and
// returns it. An existing ID replaces that comment.
func (s *CommentSet) Add(c Comment) Comment {
	if c.ID == "" {
		for {
			s.next++
			c.ID = s.IDPrefix + "c" + strconv.Itoa(s.next)
			if _, taken := s.Get(c.ID); !taken {
				break
			}
		}
	}
	for i := range s.list {
		if s.list[i].ID == c.ID {
			s.list[i] = c
			return c
		}
	}
	s.list = append(s.list, c)
	return c
}

// Get returns the comment with the ID.
func (s *CommentSet) Get(id string) (Comment, bool) {
	if s != nil {
		for _, c := range s.list {
			if c.ID == id {
				return c, true
			}
		}
	}
	return Comment{}, false
}

// Update changes the comment with the ID in place.
func (s *CommentSet) Update(id string, fn func(*Comment)) bool {
	if s != nil {
		for i := range s.list {
			if s.list[i].ID == id {
				fn(&s.list[i])
				s.list[i].ID = id
				return true
			}
		}
	}
	return false
}

// Delete removes the comment with the ID.
func (s *CommentSet) Delete(id string) bool {
	if s != nil {
		for i := range s.list {
			if s.list[i].ID == id {
				s.list = append(s.list[:i], s.list[i+1:]...)
				return true
			}
		}
	}
	return false
}

// List returns the comments, oldest first.
func (s *CommentSet) List() []Comment {
	if s == nil {
		return nil
	}
	return append([]Comment(nil), s.list...)
}

// Len is the number of comments.
func (s *CommentSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.list)
}

// --- mentions and summaries ---------------------------------------------

// MentionKind says what a Mention names.
type MentionKind int

const (
	MentionUser  MentionKind = iota // @username
	MentionEmail                    // user@example.com
)

// Mention is a person named in a comment.
type Mention struct {
	Kind MentionKind
	// Text is what was written ("@ana" or "ana@example.com"); Name is the
	// user name without the @ or the address itself.
	Text, Name string
	// Start and End are the byte range of Text in the comment.
	Start, End int
}

var (
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	reHandle = regexp.MustCompile(`(^|[^\w@.+-])(@[A-Za-z0-9_](?:[A-Za-z0-9_.-]*[A-Za-z0-9_])?)`)
	reLinkMD = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	reMarks  = regexp.MustCompile("(?m)^\\s{0,3}(?:#{1,6}\\s+|>\\s?|[-*+]\\s+(?:\\[[ xX]\\]\\s+)?|\\d+[.)]\\s+)|```[^\\n]*|[~`]+")
	// Emphasis markers are only syntax at the edge of a word: snake_case and
	// 2*3 keep theirs.
	reEmphLead  = regexp.MustCompile(`(^|[^\w*_])[*_]+`)
	reEmphTrail = regexp.MustCompile(`[*_]+([^\w*_]|$)`)
	reSpaces    = regexp.MustCompile(`\s+`)
)

// Mentions finds the @usernames and e-mail addresses in text, in order.
func Mentions(text string) []Mention {
	var out []Mention
	var taken [][2]int
	for _, m := range reEmail.FindAllStringIndex(text, -1) {
		out = append(out, Mention{Kind: MentionEmail, Text: text[m[0]:m[1]], Name: text[m[0]:m[1]], Start: m[0], End: m[1]})
		taken = append(taken, [2]int{m[0], m[1]})
	}
	for _, m := range reHandle.FindAllStringSubmatchIndex(text, -1) {
		s, e := m[4], m[5]
		overlap := false
		for _, t := range taken {
			if s < t[1] && e > t[0] {
				overlap = true
			}
		}
		if !overlap {
			out = append(out, Mention{Kind: MentionUser, Text: text[s:e], Name: text[s+1 : e], Start: s, End: e})
		}
	}
	for i := 1; i < len(out); i++ { // by position
		for j := i; j > 0 && out[j].Start < out[j-1].Start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// StripMarkdown returns text without Markdown syntax: emphasis marks,
// headings, quotes and list markers, code fences and the targets of
// links (their text stays).
func StripMarkdown(text string) string {
	text = reLinkMD.ReplaceAllString(text, "$1")
	text = reMarks.ReplaceAllString(text, "")
	text = reEmphLead.ReplaceAllString(text, "$1")
	text = reEmphTrail.ReplaceAllString(text, "$1")
	return strings.TrimSpace(text)
}

// CommentSummary is the plain one-line form of a comment's Markdown for a
// tip: syntax stripped, whitespace collapsed, cut at max characters with
// an ellipsis.
func CommentSummary(text string, max int) string {
	s := reSpaces.ReplaceAllString(StripMarkdown(text), " ")
	if rs := []rune(s); max > 0 && len(rs) > max {
		return strings.TrimSpace(string(rs[:max-1])) + "…"
	}
	return s
}

// A character can carry several comments: Format.Comment holds their IDs
// separated by commas (IDs never contain one; see CommentSet.IDPrefix).

// commentList splits a Format.Comment value into IDs.
func commentList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// commentHas reports whether the value includes id.
func commentHas(s, id string) bool {
	for _, c := range commentList(s) {
		if c == id {
			return true
		}
	}
	return false
}

// commentAdd returns the value with id included.
func commentAdd(s, id string) string {
	if commentHas(s, id) {
		return s
	}
	if s == "" {
		return id
	}
	return s + "," + id
}

// commentRemove returns the value without id.
func commentRemove(s, id string) string {
	var keep []string
	for _, c := range commentList(s) {
		if c != id {
			keep = append(keep, c)
		}
	}
	return strings.Join(keep, ",")
}
