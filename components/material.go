package components

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

func Material(name string) xango.Node {
	path, ok := materialPaths[name]
	if !ok {
		return xango.Text("")
	}
	svg := "%3Csvg%20xmlns=%27http://www.w3.org/2000/svg%27%20viewBox=%270%20-960%20960%20960%27%3E%3Cpath%20d=%27" +
		strings.ReplaceAll(path, " ", "%20") + "%27/%3E%3C/svg%3E"
	url := "url(data:image/svg+xml," + svg + ")"
	return xango.El("span", []xango.Attr{
		xango.A("class", "gj-material"), xango.A("data-material", name), xango.A("aria-hidden", "true"),
		xango.A("style", "-webkit-mask-image:"+url+";mask-image:"+url),
	})
}

// MaterialNames lists the symbols available, sorted.
func MaterialNames() []string {
	out := make([]string, 0, len(materialPaths))
	for n := range materialPaths {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HasMaterial says whether Material knows the name.
func HasMaterial(name string) bool { _, ok := materialPaths[name]; return ok }

var commandMaterial = map[string]string{
	"bold": "format_bold", "italic": "format_italic", "underline": "format_underlined", "strikethrough": "strikethrough_s",
	"superscript": "superscript", "subscript": "subscript", "copy-format": "format_paint", "paste-format": "format_paint",
	"edit.undo": "undo", "edit.redo": "redo", "link.edit": "link", "link.remove": "link", "link.open": "link",
	"format.clear": "format_clear", "para.align-left": "format_align_left", "para.align-center": "format_align_center",
	"para.align-right": "format_align_right", "para.indent": "format_indent_increase", "para.outdent": "format_indent_decrease",
	"para.quote": "format_quote", "list.bullets": "format_list_bulleted", "list.numbers": "format_list_numbered", "list.checks": "checklist",
	"comment.add": "add_comment", "bookmark.add": "bookmark_add", "toc.insert": "toc", "find.open": "search", "find.replace": "find_replace",
	"clipboard.copy-markdown": "content_copy", "clipboard.paste-markdown": "content_paste", "line.delete": "delete",
}

func CommandMaterial(command string) string { return commandMaterial[command] }
