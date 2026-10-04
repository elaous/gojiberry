package xango

import "sync"

// StyleSheet is a stylesheet registered with RegisterStyle.
type StyleSheet struct {
	// ID identifies the sheet: the import path of the .css file
	// ("github.com/me/app/components/button.css").
	ID  string
	CSS string
}

var (
	stylesMu sync.RWMutex
	styles   []StyleSheet
	styleIDs = map[string]bool{}
)

// RegisterStyle adds a stylesheet to the program's set. xgc generates a
// call to it from a blank CSS import,
//
//	import _ "github.com/me/app/components/button.css"
//
// the way a blank import of a Go package runs its init: whoever imports
// the package gets its styles. Registering an ID twice is a no-op, so a
// sheet imported by several files appears once. Sheets keep their
// registration order, which is package initialisation order: a package's
// dependencies come before it.
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

// Styles returns one <style> element per registered stylesheet (each
// tagged data-xango-style with its ID), for a page's <head>; Document puts
// them there itself. It is a Fragment with no children when nothing was
// registered.
func Styles() Node {
	var out []Node
	for _, s := range RegisteredStyles() {
		out = append(out, El("style", []Attr{A("data-xango-style", s.ID)}, Raw(styleEnd.ReplaceAllString(s.CSS, `<\/$1`))))
	}
	return Frag(out...)
}
