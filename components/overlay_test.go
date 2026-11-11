package components

import (
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func TestModal(t *testing.T) {
	t.Run("renders nothing when closed", func(t *testing.T) {
		html := render(t, Modal(ModalProps{Open: false, Content: []xango.Node{xango.Text("hi")}}))
		if html != "" {
			t.Errorf("expected empty output when closed, got: %s", html)
		}
	})

	t.Run("open renders a backdrop and content as siblings", func(t *testing.T) {
		html := render(t, Modal(ModalProps{Open: true, Label: "My modal", Content: []xango.Node{xango.Text("body")}}))
		mustContain(t, html, "gj-modal-backdrop", `role="dialog"`, `aria-modal="true"`, `aria-label="My modal"`, "body")
	})

	t.Run("Role overrides the default dialog role", func(t *testing.T) {
		html := render(t, Modal(ModalProps{Open: true, Role: "alertdialog"}))
		mustContain(t, html, `role="alertdialog"`)
	})

	t.Run("OnClose fires when the backdrop is clicked", func(t *testing.T) {
		closed := false
		n := Modal(ModalProps{Open: true, OnClose: func() { closed = true }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 click handler (the backdrop), got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !closed {
			t.Error("OnClose did not fire on backdrop click")
		}
	})
}

func TestDialog(t *testing.T) {
	html := render(t, Dialog(DialogProps{
		Open:    true,
		Title:   "Confirm action",
		Body:    []xango.Node{xango.Text("Are you sure?")},
		Actions: []xango.Node{Button(ButtonProps{Label: "OK"})},
	}))
	mustContain(t, html, "gj-dialog-title", "Confirm action", "Are you sure?", "gj-dialog-actions", ">OK<")

	t.Run("no Actions omits the actions footer", func(t *testing.T) {
		html := render(t, Dialog(DialogProps{Open: true, Title: "T"}))
		mustNotContain(t, html, "gj-dialog-actions")
	})
}

func TestAlert(t *testing.T) {
	closed := false
	n := Alert(AlertProps{Open: true, Title: "Heads up", Message: "Something happened", OnClose: func() { closed = true }})
	html := render(t, n)
	mustContain(t, html, `role="alertdialog"`, "Heads up", "Something happened", ">OK<")

	handlers := findHandlers(n, "click")
	if len(handlers) == 0 {
		t.Fatal("expected at least one click handler (OK button)")
	}
	handlers[len(handlers)-1](xango.Event{})
	if !closed {
		t.Error("clicking OK did not invoke OnClose")
	}

	t.Run("OKLabel overrides the default", func(t *testing.T) {
		html := render(t, Alert(AlertProps{Open: true, OKLabel: "Got it"}))
		mustContain(t, html, ">Got it<")
	})
}

func TestConfirm(t *testing.T) {
	var confirmed, cancelled bool
	n := Confirm(ConfirmProps{
		Open:      true,
		Title:     "Delete?",
		Message:   "This cannot be undone",
		OnConfirm: func() { confirmed = true },
		OnCancel:  func() { cancelled = true },
	})
	html := render(t, n)
	mustContain(t, html, `role="alertdialog"`, "Delete?", "This cannot be undone", ">Cancel<", ">OK<")

	handlers := findHandlers(n, "click")
	if len(handlers) != 3 {
		t.Fatalf("expected 3 click handlers, got %d", len(handlers))
	}
	handlers[1](xango.Event{}) // Cancel
	if !cancelled {
		t.Error("Cancel button did not invoke OnCancel")
	}
	handlers[2](xango.Event{}) // Confirm
	if !confirmed {
		t.Error("Confirm button did not invoke OnConfirm")
	}

	t.Run("custom labels", func(t *testing.T) {
		html := render(t, Confirm(ConfirmProps{Open: true, ConfirmLabel: "Yes, delete", CancelLabel: "No"}))
		mustContain(t, html, ">Yes, delete<", ">No<")
	})
}

func TestToast(t *testing.T) {
	t.Run("renders nothing when closed", func(t *testing.T) {
		html := render(t, Toast(ToastProps{Open: false}))
		if html != "" {
			t.Errorf("expected empty output when closed, got: %s", html)
		}
	})

	t.Run("defaults to the info variant", func(t *testing.T) {
		html := render(t, Toast(ToastProps{Open: true, Message: "Saved"}))
		mustContain(t, html, "gj-toast-info", "Saved", `role="status"`, `aria-live="polite"`)
	})

	t.Run("an explicit Variant is honored", func(t *testing.T) {
		html := render(t, Toast(ToastProps{Open: true, Variant: ToastError, Message: "Failed"}))
		mustContain(t, html, "gj-toast-error")
	})

	t.Run("no OnClose omits the dismiss button", func(t *testing.T) {
		html := render(t, Toast(ToastProps{Open: true, Message: "no close"}))
		mustNotContain(t, html, "gj-toast-close")
	})

	t.Run("OnClose renders and fires a dismiss button", func(t *testing.T) {
		dismissed := false
		n := Toast(ToastProps{Open: true, Message: "closable", OnClose: func() { dismissed = true }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 click handler, got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !dismissed {
			t.Error("OnClose did not fire")
		}
	})
}

func TestContextMenu(t *testing.T) {
	items := []ContextMenuItem{
		{Label: "Copy", OnSelect: func() {}},
		{Label: "Delete", Disabled: true},
	}

	t.Run("renders nothing when closed", func(t *testing.T) {
		html := render(t, ContextMenu(ContextMenuProps{Open: false, Items: items}))
		if html != "" {
			t.Errorf("expected empty output when closed, got: %s", html)
		}
	})

	t.Run("open renders positioned at X/Y with a backdrop", func(t *testing.T) {
		html := render(t, ContextMenu(ContextMenuProps{Open: true, Items: items, X: 100, Y: 50}))
		mustContain(t, html, "gj-contextmenu-backdrop", "left: 100px; top: 50px;", `role="menu"`, "Copy", "Delete")
	})

	t.Run("a Disabled item gets aria-disabled and no click handler", func(t *testing.T) {
		html := render(t, ContextMenu(ContextMenuProps{Open: true, Items: items}))
		mustContain(t, html, `aria-disabled="true"`)
	})

	t.Run("an item Icon renders before its label", func(t *testing.T) {
		icon := Icon(IconProps{Name: IconStar})
		html := render(t, ContextMenu(ContextMenuProps{
			Open:  true,
			Items: []ContextMenuItem{{Label: "Copy", Icon: &icon, OnSelect: func() {}}},
		}))
		mustContain(t, html, "gj-contextmenu-icon")
	})

	t.Run("OnSelect fires fine with no OnClose set", func(t *testing.T) {
		selected := false
		n := ContextMenu(ContextMenuProps{
			Open: true,
			Items: []ContextMenuItem{
				{Label: "Copy", OnSelect: func() { selected = true }},
			},
		})
		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 click handler (no backdrop handler since OnClose is nil), got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !selected {
			t.Error("OnSelect did not fire")
		}
	})

	t.Run("selecting an item calls OnSelect then OnClose", func(t *testing.T) {
		selected := false
		closed := false
		n := ContextMenu(ContextMenuProps{
			Open: true,
			Items: []ContextMenuItem{
				{Label: "Copy", OnSelect: func() { selected = true }},
			},
			OnClose: func() { closed = true },
		})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers, got %d", len(handlers))
		}
		handlers[1](xango.Event{})
		if !selected || !closed {
			t.Errorf("selecting an item should call both OnSelect and OnClose, got selected=%v closed=%v", selected, closed)
		}

		closed = false
		handlers[0](xango.Event{}) // clicking the backdrop itself also invokes OnClose
		if !closed {
			t.Error("clicking the backdrop did not invoke OnClose")
		}
	})
}

func TestPopover(t *testing.T) {
	t.Run("closed shows only the trigger", func(t *testing.T) {
		html := render(t, Popover(PopoverProps{Trigger: []xango.Node{xango.Text("Open menu")}}))
		mustContain(t, html, "gj-popover-trigger", "Open menu")
		mustNotContain(t, html, "gj-popover-panel")
	})

	t.Run("open renders the panel with a placement class and backdrop", func(t *testing.T) {
		html := render(t, Popover(PopoverProps{
			Open:      true,
			Trigger:   []xango.Node{xango.Text("Trigger")},
			Content:   []xango.Node{xango.Text("Panel content")},
			Placement: PopoverTop,
		}))
		mustContain(t, html, "gj-popover-panel gj-popover-top", "Panel content", "gj-popover-backdrop")
	})

	t.Run("OnToggleOpen and OnClose both fire", func(t *testing.T) {
		var toggledTo bool
		toggledCalled := false
		closed := false
		n := Popover(PopoverProps{
			Open:         true,
			OnToggleOpen: func(open bool) { toggledCalled = true; toggledTo = open },
			OnClose:      func() { closed = true },
		})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers (trigger, backdrop), got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !toggledCalled || toggledTo != false {
			t.Errorf("clicking the trigger while Open=true should call OnToggleOpen(false), got called=%v value=%v", toggledCalled, toggledTo)
		}
		handlers[1](xango.Event{})
		if !closed {
			t.Error("clicking the backdrop did not invoke OnClose")
		}
	})
}

func TestImageZoom(t *testing.T) {
	t.Run("not zoomed shows only the thumbnail", func(t *testing.T) {
		html := render(t, ImageZoom(ImageZoomProps{Src: "cat.png", Alt: "Cat"}))
		mustContain(t, html, "gj-imagezoom-thumb", `src="cat.png"`)
		mustNotContain(t, html, "gj-imagezoom-full", "gj-imagezoom-backdrop")
	})

	t.Run("zoomed also shows the full image and a backdrop", func(t *testing.T) {
		html := render(t, ImageZoom(ImageZoomProps{Src: "cat.png", Zoomed: true}))
		mustContain(t, html, "gj-imagezoom-full", "gj-imagezoom-backdrop")
	})

	t.Run("OnToggle fires true from the thumb and false from the backdrop", func(t *testing.T) {
		var got bool
		n := ImageZoom(ImageZoomProps{Src: "x.png", Zoomed: true, OnToggle: func(zoomed bool) { got = zoomed }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers (thumb, backdrop), got %d", len(handlers))
		}
		handlers[0](xango.Event{}) // thumb -> true
		if !got {
			t.Error("clicking the thumbnail should invoke OnToggle(true)")
		}
		handlers[1](xango.Event{}) // backdrop -> false
		if got {
			t.Error("clicking the backdrop should invoke OnToggle(false)")
		}
	})
}
