package xango

import (
	"reflect"
	"testing"
)

func TestEventValue(t *testing.T) {
	e := NewEvent("input", "hello", false, nil, nil)
	if got := EventValue[string](e); got != "hello" {
		t.Errorf("EventValue[string] = %q, want %q", got, "hello")
	}

	click := NewEvent("click", nil, false, nil, nil)
	if got := EventValue[string](click); got != "" {
		t.Errorf("EventValue[string] on a payload-less event = %q, want \"\"", got)
	}
	if got := EventValue[float64](click); got != 0 {
		t.Errorf("EventValue[float64] on a payload-less event = %v, want 0", got)
	}
}

func TestNewEventFields(t *testing.T) {
	e := NewEvent("change", 42, true, nil, nil)
	if e.Type != "change" {
		t.Errorf("Type = %q, want %q", e.Type, "change")
	}
	if !e.Checked {
		t.Error("Checked = false, want true")
	}
	if got := EventValue[int](e); got != 42 {
		t.Errorf("EventValue[int] = %v, want 42", got)
	}
}

func TestPreventDefault(t *testing.T) {
	called := false
	e := NewEvent("click", nil, false, func() { called = true }, nil)
	e.PreventDefault()
	if !called {
		t.Error("PreventDefault did not invoke the underlying callback")
	}

	noop := NewEvent("click", nil, false, nil, nil)
	noop.PreventDefault()
}

func TestStopPropagation(t *testing.T) {
	called := false
	e := NewEvent("click", nil, false, nil, func() { called = true })
	e.StopPropagation()
	if !called {
		t.Error("StopPropagation did not invoke the underlying callback")
	}

	noop := NewEvent("click", nil, false, nil, nil)
	noop.StopPropagation()
}

func TestWithKey(t *testing.T) {
	original := El("li", nil, Text("item"))
	keyed := original.WithKey("item-1")

	if original.Key != "" {
		t.Errorf("WithKey mutated the receiver's Key, got %q", original.Key)
	}
	if keyed.Key != "item-1" {
		t.Errorf("keyed.Key = %q, want %q", keyed.Key, "item-1")
	}
	keyed.Key = ""
	if !reflect.DeepEqual(keyed, original) {
		t.Errorf("WithKey changed more than Key: got %+v, want %+v", keyed, original)
	}
}

func TestHost(t *testing.T) {
	type props struct{ N int }
	n := Host("canvas.X", props{N: 3}, Text("a"))
	if n.Kind != KindHost || n.Tag != "canvas.X" || n.Props.(props).N != 3 || len(n.Children) != 1 {
		t.Fatalf("Host = %+v", n)
	}
	if _, err := RenderString(n); err == nil {
		t.Fatal("RenderString of a host node should fail")
	}
}

func TestMoveFocusIsAHookTheBrowserFills(t *testing.T) {
	if NewEvent("keydown", nil, false, nil, nil).MoveFocus(".w", "li", FocusNext) {
		t.Error("MoveFocus without a hook reported success")
	}
	var got [3]any
	e := NewEvent("keydown", nil, false, nil, nil).WithFocus(func(root, items string, step FocusStep) bool {
		got = [3]any{root, items, step}
		return true
	})
	if !e.MoveFocus(".w", ":scope > li", FocusLast) || got != [3]any{".w", ":scope > li", FocusLast} {
		t.Errorf("hook got %v", got)
	}
}
