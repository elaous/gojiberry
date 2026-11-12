package components

import (
	"slices"
	"testing"

	"github.com/elaous/xango"
)

func TestDropdownKeyboard(t *testing.T) {
	var opened []bool
	var chosen []string
	props := DropdownProps{
		Options:      []DropdownOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}},
		OnToggleOpen: func(o bool) { opened = append(opened, o) },
		OnSelect:     func(v string) { chosen = append(chosen, v) },
	}
	closed := Dropdown(props)
	press := func(h xango.EventHandler, key string) ([]focusCall, bool) {
		var calls []focusCall
		prevented := false
		h(keyEvent(key, &calls, &prevented))
		return calls, prevented
	}
	control := findHandlers(closed, "keydown")
	if len(control) != 1 {
		t.Fatalf("the button has %d keydown handlers", len(control))
	}
	calls, prevented := press(control[0], "ArrowDown")
	if !prevented || !slices.Equal(opened, []bool{true}) || len(calls) != 1 || calls[0].step != xango.FocusFirst || calls[0].root != ".gj-dropdown" {
		t.Errorf("Down on the button: opened %v calls %+v prevented %v", opened, calls, prevented)
	}
	if calls, _ := press(control[0], "ArrowUp"); len(calls) != 1 || calls[0].step != xango.FocusLast {
		t.Errorf("Up on the button goes to the last option: %+v", calls)
	}

	props.Open = true
	open := Dropdown(props)
	keys := findHandlers(open, "keydown") // the button, then the options
	if len(keys) != 3 {
		t.Fatalf("an open list has %d keydown handlers, want 3", len(keys))
	}
	for key, want := range map[string]xango.FocusStep{"ArrowDown": xango.FocusNext, "ArrowUp": xango.FocusPrev, "Home": xango.FocusFirst, "End": xango.FocusLast} {
		if calls, prevented := press(keys[1], key); len(calls) != 1 || calls[0].step != want || !prevented {
			t.Errorf("%s on an option: %+v", key, calls)
		}
	}
	opened = nil
	calls, _ = press(keys[2], "Enter")
	if !slices.Equal(chosen, []string{"b"}) || len(calls) != 1 || calls[0].items != ":scope > .gj-dropdown-control" {
		t.Errorf("Enter on the second option: chose %v, focus %+v", chosen, calls)
	}
	calls, _ = press(keys[1], "Escape")
	if !slices.Equal(opened, []bool{false}) || len(calls) != 1 || calls[0].items != ":scope > .gj-dropdown-control" {
		t.Errorf("Escape: opened %v focus %+v", opened, calls)
	}
	opened = nil
	if calls, prevented := press(keys[1], "Tab"); !slices.Equal(opened, []bool{false}) || len(calls) != 0 || prevented {
		t.Errorf("Tab closes the list and lets the focus leave: %v %+v %v", opened, calls, prevented)
	}
	options, focusable := 0, 0
	var walk func(xango.Node)
	walk = func(n xango.Node) {
		isOption, noTab := false, false
		for _, a := range n.Attrs {
			isOption = isOption || (a.Name == "role" && a.Value == "option")
			noTab = noTab || (a.Name == "tabindex" && a.Value == "-1")
		}
		if isOption {
			options++
			if noTab {
				focusable++
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(open)
	if options != 2 || focusable != 2 {
		t.Errorf("options %d, with tabindex -1: %d", options, focusable)
	}
}
