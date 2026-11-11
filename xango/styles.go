package xango

import "sync"

// StyleSheet is a stylesheet registered with RegisterStyle.
type StyleSheet struct {
	ID  string
	CSS string
}

var (
	stylesMu sync.RWMutex
	styles   []StyleSheet
	styleIDs = map[string]bool{}
)

// RegisterStyle adds a stylesheet to the program's set.
func RegisterStyle(id, css string) {
	stylesMu.Lock()
	defer stylesMu.Unlock()
	if styleIDs[id] {
		return
	}
	styleIDs[id] = true
	styles = append(styles, StyleSheet{ID: id, CSS: css})
}

// RegisteredStyles returns the registered stylesheets in order.
func RegisteredStyles() []StyleSheet {
	stylesMu.RLock()
	defer stylesMu.RUnlock()
	return append([]StyleSheet(nil), styles...)
}

func Styles() Node {
	var out []Node
	for _, s := range RegisteredStyles() {
		out = append(out, El("style", []Attr{A("data-xango-style", s.ID)}, Raw(styleEnd.ReplaceAllString(s.CSS, `<\/$1`))))
	}
	return Frag(out...)
}
