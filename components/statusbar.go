package components

import (
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

// StatusItem is one thing the status bar says.
type StatusItem struct {
	ID, Text, Title, State string
	Live                   bool
}

// StatusBarProps configures StatusBar.
type StatusBarProps struct {
	Items []StatusItem
}

func StatusBar(props StatusBarProps) xango.Node {
	var kids []xango.Node
	for _, it := range props.Items {
		if it.Text == "" {
			continue
		}
		attrs := []xango.Attr{xango.A("class", "gj-status-item"), xango.A("data-item", it.ID)}
		if it.Title != "" {
			attrs = append(attrs, xango.A("title", it.Title))
		}
		if it.State != "" {
			attrs = append(attrs, xango.A("data-state", it.State))
		}
		if it.Live {
			attrs = append(attrs, xango.A("role", "status"))
		}
		kids = append(kids, xango.El("span", attrs, xango.Text(it.Text)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-statusbar")}, kids...)
}

func HeadingPath(entries []canvas.OutlineEntry, index int) []string {
	if index < 0 || index >= len(entries) {
		return nil
	}
	path := []string{entries[index].Text}
	level := entries[index].Level
	for i := index - 1; i >= 0 && level > 1; i-- {
		if entries[i].Level < level {
			path = append([]string{entries[i].Text}, path...)
			level = entries[i].Level
		}
	}
	return path
}

// HeadingBreadcrumb is HeadingPath as one line, "Chapter › Section".
func HeadingBreadcrumb(entries []canvas.OutlineEntry, index int) string {
	return strings.Join(HeadingPath(entries, index), " › ")
}
