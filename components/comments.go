package components

import (
	"regexp"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// --- Markdown text with mentions -----------------------------------------

type inlinePattern struct {
	re   *regexp.Regexp
	make func(m []string) xango.Node
}

// inlinePatterns is set in init: the makers call inlineNodes, which reads it.
var inlinePatterns []inlinePattern

func init() {
	inlinePatterns = []inlinePattern{
		{regexp.MustCompile("`([^`]+)`"), func(m []string) xango.Node { return xango.El("code", nil, xango.Text(m[1])) }},
		{regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`), func(m []string) xango.Node {
			if !safeURL(m[2]) {
				return xango.Text(m[1])
			}
			return xango.El("a", []xango.Attr{xango.A("href", m[2]), xango.A("rel", "noopener noreferrer"), xango.A("target", "_blank")}, inlineNodes(m[1])...)
		}},
		{regexp.MustCompile(`\*\*(.+?)\*\*`), func(m []string) xango.Node { return xango.El("strong", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`~~(.+?)~~`), func(m []string) xango.Node { return xango.El("s", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`\*([^*\s][^*]*?)\*`), func(m []string) xango.Node { return xango.El("em", nil, inlineNodes(m[1])...) }},
		{regexp.MustCompile(`(?:^|\b)_([^_\s][^_]*?)_(?:\b|$)`), func(m []string) xango.Node { return xango.El("em", nil, inlineNodes(m[1])...) }},
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
func inlineNodes(s string) []xango.Node {
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
	var out []xango.Node
	out = append(out, mentionNodes(s[:bestIdx[0]])...)
	out = append(out, inlinePatterns[best].make(m))
	return append(out, inlineNodes(s[bestIdx[1]:])...)
}

func mentionNodes(s string) []xango.Node {
	if s == "" {
		return nil
	}
	var out []xango.Node
	last := 0
	for _, m := range canvas.Mentions(s) {
		if m.Start > last {
			out = append(out, xango.Text(s[last:m.Start]))
		}
		switch m.Kind {
		case canvas.MentionEmail:
			out = append(out, xango.El("a", []xango.Attr{xango.A("class", "gj-mention gj-mention-email"), xango.A("href", "mailto:"+m.Name), xango.A("data-mention", m.Name)}, xango.Text(m.Text)))
		default:
			out = append(out, xango.El("span", []xango.Attr{xango.A("class", "gj-mention"), xango.A("data-mention", m.Name)}, xango.Text(m.Text)))
		}
		last = m.End
	}
	if last < len(s) {
		out = append(out, xango.Text(s[last:]))
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
func MarkdownText(text string) xango.Node {
	var blocks []xango.Node
	var para []string
	flush := func() {
		if len(para) == 0 {
			return
		}
		var kids []xango.Node
		for i, l := range para {
			if i > 0 {
				kids = append(kids, xango.El("br", nil))
			}
			kids = append(kids, inlineNodes(l)...)
		}
		blocks = append(blocks, xango.El("p", nil, kids...))
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
			blocks = append(blocks, xango.El("p", []xango.Attr{xango.A("class", "gj-md-heading")}, inlineNodes(reHeading.FindStringSubmatch(line)[1])...))
		case reBullet.MatchString(line) || reOrdered.MatchString(line):
			flush()
			tag, re := "ul", reBullet
			if reOrdered.MatchString(line) {
				tag, re = "ol", reOrdered
			}
			var items []xango.Node
			for ; i < len(lines) && re.MatchString(lines[i]); i++ {
				items = append(items, xango.El("li", nil, inlineNodes(re.FindStringSubmatch(lines[i])[1])...))
			}
			i--
			blocks = append(blocks, xango.El(tag, nil, items...))
		case strings.HasPrefix(strings.TrimSpace(line), ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			blocks = append(blocks, xango.El("blockquote", nil, inlineNodes(strings.Join(q, " "))...))
		default:
			para = append(para, line)
		}
	}
	flush()
	return xango.El("div", []xango.Attr{xango.A("class", "gj-markdown")}, blocks...)
}

// --- CommentCard and CommentList -------------------------------------------

// CommentCardProps configures CommentCard.
type CommentCardProps struct {
	Comment canvas.Comment
	// Excerpt is the commented text, shown as a quote.
	Excerpt  string
	Selected bool
	// CanEdit lets the viewer resolve and delete the comment; CanReply lets
	// them reply (a viewer role may reply but not resolve or delete).
	CanEdit bool
	// CanReply shows the reply box; it is also shown when CanEdit is set.
	CanReply bool
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
func CommentCard(props CommentCardProps) xango.Node {
	c := props.Comment
	class := "gj-comment"
	if props.Selected {
		class += " gj-comment-selected"
	}
	if c.Resolved {
		class += " gj-comment-resolved"
	}
	attrs := []xango.Attr{xango.A("class", class), xango.A("data-comment", c.ID)}
	if props.OnSelect != nil {
		attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnSelect(c.ID) }))
	}
	author := c.Author
	if author == "" {
		author = "Someone"
	}
	head := xango.El("div", []xango.Attr{xango.A("class", "gj-comment-head")},
		xango.El("strong", []xango.Attr{xango.A("class", "gj-comment-author")}, xango.Text(author)),
		xango.El("span", []xango.Attr{xango.A("class", "gj-comment-time")}, xango.Text(timeLabel(c.Created))))
	kids := []xango.Node{head}
	if props.Excerpt != "" {
		kids = append(kids, xango.El("blockquote", []xango.Attr{xango.A("class", "gj-comment-excerpt")}, xango.Text(props.Excerpt)))
	}
	kids = append(kids, MarkdownText(c.Text))
	if c.Resolved {
		kids = append(kids, xango.El("span", []xango.Attr{xango.A("class", "gj-comment-badge")}, xango.Text("Resolved")))
	}
	if len(c.Replies) > 0 {
		var rs []xango.Node
		for _, r := range c.Replies {
			name := r.Author
			if name == "" {
				name = "Someone"
			}
			rs = append(rs, xango.El("li", []xango.Attr{xango.A("class", "gj-comment-reply")},
				xango.El("strong", nil, xango.Text(name)), xango.Text(" "), MarkdownText(r.Text)))
		}
		kids = append(kids, xango.El("ul", []xango.Attr{xango.A("class", "gj-comment-replies")}, rs...))
	}
	btn := func(class, label string, on func()) xango.Node {
		a := []xango.Attr{xango.A("type", "button"), xango.A("class", class)}
		if on != nil {
			a = append(a, xango.On("click", func(xango.Event) { on() }))
		} else {
			a = append(a, xango.A("disabled", "disabled"))
		}
		return xango.El("button", a, xango.Text(label))
	}
	if props.CanEdit || props.CanReply {
		var onReply func()
		if props.OnReply != nil && strings.TrimSpace(props.ReplyDraft) != "" {
			onReply = func() { props.OnReply(c.ID, props.ReplyDraft) }
		}
		in := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-comment-input"), xango.A("placeholder", "Reply…"),
			xango.A("aria-label", "Reply"), xango.A("value", props.ReplyDraft)}
		if props.OnReplyDraft != nil {
			in = append(in, xango.On("input", func(e xango.Event) { props.OnReplyDraft(c.ID, xango.EventValue[string](e)) }))
		}
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-comment-reply-row")}, xango.El("input", in), btn("gj-comment-send", "Reply", onReply)))
	}
	if props.CanEdit {
		resolve := "Resolve"
		if c.Resolved {
			resolve = "Reopen"
		}
		var onResolve, onDelete func()
		if props.OnResolve != nil {
			onResolve = func() { props.OnResolve(c.ID) }
		}
		if props.OnDelete != nil {
			onDelete = func() { props.OnDelete(c.ID) }
		}
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-comment-actions")}, btn("gj-comment-resolve", resolve, onResolve), btn("gj-comment-delete", "Delete", onDelete)))
	}
	return xango.El("div", attrs, kids...)
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
	CanReply     bool
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
func CommentList(props CommentListProps) xango.Node {
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
	var cards []xango.Node
	for _, c := range list {
		cards = append(cards, CommentCard(CommentCardProps{
			Comment: c, Excerpt: props.Excerpts[c.ID], Selected: c.ID == props.Selected, CanEdit: props.CanEdit, CanReply: props.CanReply,
			ReplyDraft: props.Drafts[c.ID],
			OnSelect:   props.OnSelect, OnResolve: props.OnResolve, OnDelete: props.OnDelete,
			OnReplyDraft: props.OnReplyDraft, OnReply: props.OnReply,
		}))
	}
	if len(cards) == 0 {
		cards = append(cards, xango.El("p", []xango.Attr{xango.A("class", "gj-comment-empty")}, xango.Text("No comments.")))
	}
	toggle := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-comment-toggle"), xango.A("aria-pressed", boolAttr(props.ShowResolved))}
	if props.OnToggleShowResolved != nil {
		show := !props.ShowResolved
		toggle = append(toggle, xango.On("click", func(xango.Event) { props.OnToggleShowResolved(show) }))
	}
	label := "Show resolved"
	if len(done) > 0 {
		label += " (" + itoa(len(done)) + ")"
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-comments")},
		xango.El("div", []xango.Attr{xango.A("class", "gj-comments-head")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-comments-title")}, xango.Text("Comments")),
			xango.El("button", toggle, xango.Text(label))),
		xango.El("div", []xango.Attr{xango.A("class", "gj-comments-list")}, cards...))
}
