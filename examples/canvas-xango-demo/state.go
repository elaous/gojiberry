package main

import "github.com/elaous/gojiberry/canvas"

// State is everything the tree depends on.
type State struct {
	Mode   canvas.Mode
	Source string
	Pages  int
}
