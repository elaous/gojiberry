package canvas

import (
	"strconv"
	"strings"
)

// MarkdownClipboard copies the selection as Markdown and pastes
// Markdown as formatted text:
//
//   - Ctrl/Cmd+Shift+C puts the selection (every cursor's, joined by
//     newlines) on the system clipboard as Markdown: **bold**,
//     *italic*, ~~strike~~, ++underline++, ==highlight==, ^super^,
//     ~sub~, `code` and #-headings -- the syntax Markdown understands;
//   - Ctrl/Cmd+Shift+V reads the clipboard as Markdown and inserts it
//     as text elements: the marks become formatting, not characters.
//
// Plain Ctrl+C and Ctrl+V are unchanged.
type MarkdownClipboard struct {
	// Markdown is the converter used to paste; the zero value is the
	// default one.
	Markdown Markdown
}

// Command names.
const (
	CmdCopyMarkdown  Command = "clipboard.copy-markdown"
	CmdPasteMarkdown Command = "clipboard.paste-markdown"
)

// Name implements Feature.
func (MarkdownClipboard) Name() string { return "markdown-clipboard" }

// Install implements Feature.
func (m MarkdownClipboard) Install(r *Registry) {
	r.AddCommand(CmdCopyMarkdown, "Copy as Markdown", "Mod+Shift+C", func(c *CommandContext) bool {
		if c.Clipboard == nil || c.Clipboard.Write == nil {
			return false
		}
		var parts []string
		for _, cur := range normalize(c.Target.Cursors()) {
			if from, to := cur.Range(); from < to {
				parts = append(parts, MarkdownFromItems(c.Target.Slice(from, to)))
			}
		}
		if len(parts) == 0 {
			return false
		}
		c.Clipboard.Write(strings.Join(parts, "\n"))
		return true
	})
	r.AddCommand(CmdPasteMarkdown, "Paste Markdown", "Mod+Shift+V", func(c *CommandContext) bool {
		if c.Clipboard == nil || c.Clipboard.Read == nil {
			return false
		}
		t := c.Target
		c.Clipboard.Read(func(text string) {
			PasteMarkdown(t, m.Markdown, text)
			if c.Redraw != nil {
				c.Redraw()
			}
		})
		return true
	})
}

// PasteMarkdown inserts Markdown text at every cursor of t, replacing
// the selections, as formatted text. When the text has as many lines as
// there are cursors, each cursor gets one line.
func PasteMarkdown(t EditTarget, md Markdown, text string) bool {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if text == "" {
		return false
	}
	cs := normalize(t.Cursors())
	keep := holdsObjects(t)
	var pieces [][]Item
	if lines := strings.Split(text, "\n"); len(lines) == len(cs) && len(cs) > 1 {
		for _, l := range lines {
			pieces = append(pieces, flattenItems(md.Transform(l), keep))
		}
	} else {
		all := flattenItems(md.Transform(text), keep)
		for range cs {
			pieces = append(pieces, all)
		}
	}
	var edits []Edit
	var out []Cursor
	shift := 0
	for i, c := range cs {
		from, to := c.Range()
		edits = append(edits, Edit{From: from, To: to, Items: pieces[i], AfterOps: c.After && from == to})
		n := itemsLen(pieces[i])
		out = append(out, At(from+shift+n))
		shift += n - (to - from)
	}
	t.Replace(edits)
	t.SetCursors(out)
	return true
}

// flattenItems prepares converted Markdown for insertion. With keep (the
// target holds objects) tables, images, breaks, column configs and
// paragraph formats are kept; otherwise only the text is: Chars as they
// are, breaks as newlines, list items and quotes as text prefixes ("• ",
// "1. ", "▌ "). Trailing newlines are trimmed.
func flattenItems(items []Item, keep bool) []Item {
	var out []Item
	var pending ParaFormat
	var nums listNumbers
	atStart := true
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			if !keep && atStart {
				n := nums.next(pending.List)
				prefix := ListPrefix(pending.List, n)
				if pending.QuoteBar != "" {
					prefix = "▌ " + prefix
				}
				if prefix != "" {
					out = append(out, Chars{Text: prefix})
				}
				pending = ParaFormat{}
			}
			out = append(out, v)
			atStart = strings.HasSuffix(v.Text, "\n")
		case ParaFormat:
			if keep {
				out = append(out, v)
			} else {
				pending = v
			}
		case Break:
			if keep {
				out = append(out, v)
			} else {
				out = append(out, Chars{Text: "\n"})
				atStart = true
			}
		case Inline, ColumnConfig:
			if keep {
				out = append(out, v)
			}
		}
	}
	for len(out) > 0 {
		c, ok := out[len(out)-1].(Chars)
		if !ok {
			break
		}
		trimmed := strings.TrimRight(c.Text, "\n")
		if trimmed == c.Text {
			break
		}
		if trimmed == "" {
			out = out[:len(out)-1]
			continue
		}
		out[len(out)-1] = Chars{Text: trimmed, Format: c.Format}
		break
	}
	return out
}

var headingScales = [5]float64{2, 1.6, 1.35, 1.2, 1.05}

// MarkdownFromItems writes items as Markdown (the inverse of the
// Markdown transform for text and character formatting).
func MarkdownFromItems(items []Item) string {
	type mdLine struct {
		runs []Chars
		pf   ParaFormat
	}
	var lines []mdLine
	cur := mdLine{runs: []Chars{}}
	for _, it := range items {
		switch v := it.(type) {
		case ParaFormat:
			cur.pf = v
		case Chars:
			parts := strings.Split(v.Text, "\n")
			for i, p := range parts {
				if i > 0 {
					lines = append(lines, cur)
					cur = mdLine{runs: []Chars{}}
				}
				if p != "" {
					cur.runs = append(cur.runs, Chars{p, v.Format})
				}
			}
		}
	}
	lines = append(lines, cur)

	var out []string
	var nums listNumbers
	for _, ln := range lines {
		runs := ln.runs
		prefix := ""
		if level := headingLevel(runs); level > 0 {
			prefix = strings.Repeat("#", level) + " "
			for i := range runs {
				runs[i].Format.Scale = 0
			}
		}
		n := nums.next(ln.pf.List)
		switch ln.pf.List.Kind {
		case ListBullet:
			prefix = strings.Repeat("  ", ln.pf.List.Level) + "- "
		case ListCheck:
			box := "[ ] "
			if ln.pf.List.Checked {
				box = "[x] "
			}
			prefix = strings.Repeat("  ", ln.pf.List.Level) + "- " + box
		case ListOrdered:
			end := "."
			if strings.HasSuffix(ln.pf.List.Marker, ")") {
				end = ")"
			}
			prefix = strings.Repeat("  ", ln.pf.List.Level) + itoa(n) + end + " "
		}
		if ln.pf.QuoteBar != "" {
			prefix = "> " + prefix
		}
		var b strings.Builder
		b.WriteString(prefix)
		for _, r := range runs {
			b.WriteString(markRun(r))
		}
		line := b.String()
		if prefix == "" {
			line = escapeLineStart(line)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// headingLevel returns 1-5 if every run of the line has the Scale of
// that heading level, else 0.
func headingLevel(runs []Chars) int {
	if len(runs) == 0 {
		return 0
	}
	for lvl, s := range headingScales {
		all := true
		for _, r := range runs {
			if r.Format.Scale != s {
				all = false
				break
			}
		}
		if all {
			return lvl + 1
		}
	}
	return 0
}

func escapeLineStart(line string) string {
	if line == "" {
		return line
	}
	switch line[0] {
	case '#', '>', '-', '+':
		return `\` + line
	}
	return line
}

// markRun writes one run with its formatting as marks around the text,
// keeping surrounding spaces outside the marks.
func markRun(r Chars) string {
	body := strings.TrimSpace(r.Text)
	if body == "" {
		return r.Text
	}
	lead := r.Text[:strings.Index(r.Text, body)]
	trail := r.Text[len(lead)+len(body):]
	f := r.Format
	if f.FontFamily == "monospace" {
		return lead + "`" + body + "`" + trail
	}
	s := escapeInline(body)
	wrap := func(on bool, mark string) {
		if on {
			s = mark + s + mark
		}
	}
	// Innermost first, so the outermost mark is written first.
	wrap(f.Subscript, "~")
	wrap(f.Superscript, "^")
	wrap(f.Background != "", "==")
	wrap(f.Underline, "++")
	wrap(f.Strike, "~~")
	wrap(f.FontStyle == "italic", "*")
	wrap(f.FontWeight >= 600, "**")
	return lead + s + trail
}

// escapeInline backslash-escapes the characters Markdown would read as
// marks.
func escapeInline(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch r {
		case '\\', '`', '*', '~', '^', '[', ']', '<', '>':
			b.WriteByte('\\')
		case '_':
			if i == 0 || i+1 >= len(rs) || !isWordRune(rs[i-1]) || !isWordRune(rs[i+1]) {
				b.WriteByte('\\')
			}
		case '+', '=':
			if (i > 0 && rs[i-1] == r) || (i+1 < len(rs) && rs[i+1] == r) {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(max(n, 1)) }
