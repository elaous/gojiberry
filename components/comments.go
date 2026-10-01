package components

import (
	"regexp"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// --- Markdown text with mentions -----------------------------------------

type inlinePattern struct {
	re   *regexp.Regexp
	make func(m []string) gox.Node
}

// inlinePatterns is set in init: the makers call inlineNodes, which reads it.
var inlinePatterns []inlinePattern

func init() {
	inlinePatterns = []inlinePattern{
		{regexp.MustCompile("`([^`]+)`"), func(m []string) gox.Node { return gox.El("code", nil, gox.Text(m[1])) }},
		{regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`), func(m []string) gox.Node {
			if !safeURL(m[2]) {
				return gox.Text(m[1])
			}
			return gox.El("a", []gox.Attr{gox.A("href", m[2]), gox.A("rel", "noopener noreferrer"), gox.A("target", "_blank")}, inlineNodes(m[1])...)
		}},
		{regexp.MustCompile(`\*\*(.+?)\*\*`), func(m []string) gox.Node { return gox.El("strong", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`~~(.+?)~~`), func(m []string) gox.Node { return gox.El("s", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`\*([^*\s][^*]*?)\*`), func(m []string) gox.Node { return gox.El("em", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`(?:^|\b)_([^_\s][^_]*?)_(?:\b|$)`), func(m []string) gox.Node { return gox.El("em", nil, inlineNodes(m[1])...) }},
	}
}

func safeURL(u string) bool {
	l := strings.ToLower(u)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:")
}

// inlineNodes renders inline Markdown: code, links, bold, strike,
// italics, and -- in the plain text between them -- @mentions and e-mail
// addresses, which become highlighted spans (e-mail addresses mailto
// links).
func inlineNodes(s string) []gox.Node {
	best, bestIdx := -1, []int(nil)
	for i, p := range inlinePatterns {
		if loc := p.re.FindStringSubmatchIndex(s); loc != nil && (bestIdx == nil || loc[0] < bestIdx[0]) {
			best, bestIdx = i, loc
		}
	}
	if best < 0 {
		return mentionNodes(s)
	}
	m := inlinePatterns[best].re.FindStringSubmatch(s)
	var out []gox.Node
	out = append(out, mentionNodes(s[:bestIdx[0]])...)
	out = append(out, inlinePatterns[best].make(m))
	return append(out, inlineNodes(s[bestIdx[1]:])...)
}

func mentionNodes(s string) []gox.Node {
	if s == "" {
		return nil
	}
	var out []gox.Node
	last := 0
	for _, m := range canvas.Mentions(s) {
		if m.Start > last {
			out = append(out, gox.Text(s[last:m.Start]))
		}
		switch m.Kind {
		case canvas.MentionEmail:
			out = append(out, gox.El("a", []gox.Attr{gox.A("class", "gj-mention gj-mention-email"), gox.A("href", "mailto:"+m.Name), gox.A("data-mention", m.Name)}, gox.Text(m.Text)))
		default:
			out = append(out, gox.El("span", []gox.Attr{gox.A("class", "gj-mention"), gox.A("data-mention", m.Name)}, gox.Text(m.Text)))
		}
		last = m.End
	}
	if last < len(s) {
		out = append(out, gox.Text(s[last:]))
	}
	return out
}

var (
	reBullet  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reOrdered = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	reHeading = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
)

// MarkdownText renders Markdown for a comment: paragraphs, bullet and
// numbered lists, quotes, headings (as bold lines), and the inline forms
// of inlineNodes, with @username and e-mail mentions highlighted. Raw HTML
// is shown as text, and only http, https and mailto links are live.
func MarkdownText(text string) gox.Node {
	var blocks []gox.Node
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		var kids []gox.Node
		for i, l := range para {
			if i > 0 {
				kids = append(kids, gox.El("br", nil))
			}
			kids = append(kids, inlineNodes(l)...)
		}
		blocks = append(blocks, gox.El("p", nil, kids...))
		para = nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case reHeading.MatchString(line):
			flush()
			blocks = append(blocks, gox.El("p", []gox.Attr{gox.A("class", "gj-md-heading")}, inlineNodes(reHeading.FindStringSubmatch(line)[1])...))
		case reBullet.MatchString(line) || reOrdered.MatchString(line):
			flush()
			tag, re := "ul", reBullet
			if reOrdered.MatchString(line) {
				tag, re = "ol", reOrdered
			}
			var items []gox.Node
			for ; i < len(lines) && re.MatchString(lines[i]); i++ {
				items = append(items, gox.El("li", nil, inlineNodes(re.FindStringSubmatch(lines[i])[1])...))
			}
			i--
			blocks = append(blocks, gox.El(tag, nil, items...))
		case strings.HasPrefix(strings.TrimSpace(line), ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			blocks = append(blocks, gox.El("blockquote", nil, inlineNodes(strings.Join(q, " "))...))
		default:
			para = append(para, line)
		}
	}
	flush()
	return gox.El("div", []gox.Attr{gox.A("class", "gj-markdown")}, blocks...)
}

// --- CommentCard and CommentList -------------------------------------------

// CommentCardProps configures CommentCard.
type CommentCardProps struct {
	Comment canvas.Comment
	// Excerpt is the commented text, shown as a quote.
	Excerpt  string
	Selected bool
	// CanEdit lets the viewer reply, resolve and delete (a viewer role
	// sees the comment only).
	CanEdit bool
	// ReplyDraft is the text in the reply box.
	ReplyDraft string

	OnSelect     func(id string)
	OnResolve    func(id string)
	OnDelete     func(id string)
	OnReplyDraft func(id, text string)
	OnReply      func(id, text string)
}

func timeLabel(t interface {
	IsZero() bool
	Format(string) string
}) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}

// CommentCard renders one comment: author and time, the commented text,
// the Markdown body with mentions highlighted, its replies and, for an
// editor, Resolve/Reopen, Delete and a reply box.
func CommentCard(props CommentCardProps) gox.Node {
	c := props.Comment
	class := "gj-comment"
	if props.Selected {
		class += " gj-comment-selected"
	}
	if c.Resolved {
		class += " gj-comment-resolved"
	}
	attrs := []gox.Attr{gox.A("class", class), gox.A("data-comment", c.ID)}
	if props.OnSelect != nil {
		attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnSelect(c.ID) }))
	}
	author := c.Author
	if author == "" {
		author = "Someone"
	}
	head := gox.El("div", []gox.Attr{gox.A("class", "gj-comment-head")},
		gox.El("strong", []gox.Attr{gox.A("class", "gj-comment-author")}, gox.Text(author)),
		gox.El("span", []gox.Attr{gox.A("class", "gj-comment-time")}, gox.Text(timeLabel(c.Created))))
	kids := []gox.Node{head}
	if props.Excerpt != "" {
		kids = append(kids, gox.El("blockquote", []gox.Attr{gox.A("class", "gj-comment-excerpt")}, gox.Text(props.Excerpt)))
	}
	kids = append(kids, MarkdownText(c.Text))
	if c.Resolved {
		kids = append(kids, gox.El("span", []gox.Attr{gox.A("class", "gj-comment-badge")}, gox.Text("Resolved")))
	}
	if len(c.Replies) > 0 {
		var rs []gox.Node
		for _, r := range c.Replies {
			name := r.Author
			if name == "" {
				name = "Someone"
			}
			rs = append(rs, gox.El("li", []gox.Attr{gox.A("class", "gj-comment-reply")},
				gox.El("strong", nil, gox.Text(name)), gox.Text(" "), MarkdownText(r.Text)))
		}
		kids = append(kids, gox.El("ul", []gox.Attr{gox.A("class", "gj-comment-replies")}, rs...))
	}
	if props.CanEdit {
		btn := func(class, label string, on func()) gox.Node {
			a := []gox.Attr{gox.A("type", "button"), gox.A("class", class)}
			if on != nil {
				a = append(a, gox.On("click", func(gox.Event) { on() }))
			} else {
				a = append(a, gox.A("disabled", "disabled"))
			}
			return gox.El("button", a, gox.Text(label))
		}
		resolve := "Resolve"
		if c.Resolved {
			resolve = "Reopen"
		}
		var onResolve, onDelete, onReply func()
		if props.OnResolve != nil {
			onResolve = func() { props.OnResolve(c.ID) }
		}
		if props.OnDelete != nil {
			onDelete = func() { props.OnDelete(c.ID) }
		}
		if props.OnReply != nil && strings.TrimSpace(props.ReplyDraft) != "" {
			onReply = func() { props.OnReply(c.ID, props.ReplyDraft) }
		}
		in := []gox.Attr{gox.A("type", "text"), gox.A("class", "gj-comment-input"), gox.A("placeholder", "Reply…"),
			gox.A("aria-label", "Reply"), gox.A("value", props.ReplyDraft)}
		if props.OnReplyDraft != nil {
			in = append(in, gox.On("input", func(e gox.Event) { props.OnReplyDraft(c.ID, gox.EventValue[string](e)) }))
		}
		kids = append(kids,
			gox.El("div", []gox.Attr{gox.A("class", "gj-comment-reply-row")}, gox.El("input", in), btn("gj-comment-send", "Reply", onReply)),
			gox.El("div", []gox.Attr{gox.A("class", "gj-comment-actions")}, btn("gj-comment-resolve", resolve, onResolve), btn("gj-comment-delete", "Delete", onDelete)))
	}
	return gox.El("div", attrs, kids...)
}

// CommentListProps configures CommentList.
type CommentListProps struct {
	Comments []canvas.Comment
	// Excerpts maps a comment ID to the text it is on.
	Excerpts map[string]string
	// ShowResolved includes resolved comments (hidden by default).
	ShowResolved bool
	Selected     string
	CanEdit      bool
	Drafts       map[string]string

	OnSelect             func(id string)
	OnResolve            func(id string)
	OnDelete             func(id string)
	OnReplyDraft         func(id, text string)
	OnReply              func(id, text string)
	OnToggleShowResolved func(show bool)
}

// CommentList renders the comments as cards, open ones first, with a
// switch for the resolved ones.
func CommentList(props CommentListProps) gox.Node {
	var open, done []canvas.Comment
	for _, c := range props.Comments {
		if c.Resolved {
			done = append(done, c)
		} else {
			open = append(open, c)
		}
	}
	list := open
	if props.ShowResolved {
		list = append(list, done...)
	}
	var cards []gox.Node
	for _, c := range list {
		cards = append(cards, CommentCard(CommentCardProps{
			Comment: c, Excerpt: props.Excerpts[c.ID], Selected: c.ID == props.Selected, CanEdit: props.CanEdit,
			ReplyDraft: props.Drafts[c.ID],
			OnSelect:   props.OnSelect, OnResolve: props.OnResolve, OnDelete: props.OnDelete,
			OnReplyDraft: props.OnReplyDraft, OnReply: props.OnReply,
		}))
	}
	if len(cards) == 0 {
		cards = append(cards, gox.El("p", []gox.Attr{gox.A("class", "gj-comment-empty")}, gox.Text("No comments.")))
	}
	toggle := []gox.Attr{gox.A("type", "button"), gox.A("class", "gj-comment-toggle"), gox.A("aria-pressed", boolAttr(props.ShowResolved))}
	if props.OnToggleShowResolved != nil {
		show := !props.ShowResolved
		toggle = append(toggle, gox.On("click", func(gox.Event) { props.OnToggleShowResolved(show) }))
	}
	label := "Show resolved"
	if len(done) > 0 {
		label += " (" + itoa(len(done)) + ")"
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-comments")},
		gox.El("div", []gox.Attr{gox.A("class", "gj-comments-head")},
			gox.El("h4", []gox.Attr{gox.A("class", "gj-comments-title")}, gox.Text("Comments")),
			gox.El("button", toggle, gox.Text(label))),
		gox.El("div", []gox.Attr{gox.A("class", "gj-comments-list")}, cards...))
}
