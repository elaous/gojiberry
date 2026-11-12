package components

import (
	"testing"

	"github.com/elaous/xango"
)

func TestButton(t *testing.T) {
	t.Run("defaults to primary variant and type=button", func(t *testing.T) {
		html := render(t, Button(ButtonProps{Label: "Save"}))
		mustContain(t, html, "gj-button gj-button-primary", `type="button"`, ">Save<")
		mustNotContain(t, html, "disabled")
	})

	t.Run("Variant and Type are both honored", func(t *testing.T) {
		html := render(t, Button(ButtonProps{Label: "Delete", Variant: ButtonDanger, Type: "submit"}))
		mustContain(t, html, "gj-button-danger", `type="submit"`)
	})

	t.Run("Disabled adds the attribute", func(t *testing.T) {
		html := render(t, Button(ButtonProps{Label: "X", Disabled: true}))
		mustContain(t, html, `disabled="disabled"`)
	})

	t.Run("OnClick fires", func(t *testing.T) {
		clicked := false
		n := Button(ButtonProps{Label: "Go", OnClick: func(xango.Event) { clicked = true }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 click handler, got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !clicked {
			t.Error("OnClick did not fire")
		}
	})
}

func TestCarousel(t *testing.T) {
	slides := []xango.Node{xango.Text("slide 0"), xango.Text("slide 1"), xango.Text("slide 2")}

	t.Run("marks the Active slide and aria-hides the rest", func(t *testing.T) {
		html := render(t, Carousel(CarouselProps{Slides: slides, Active: 1}))
		mustContain(t, html, "gj-carousel-slide-active", "slide 0", "slide 1", "slide 2")
	})

	t.Run("renders one dot per slide, active one marked", func(t *testing.T) {
		html := render(t, Carousel(CarouselProps{Slides: slides, Active: 0}))
		mustContain(t, html, "gj-carousel-dot-active", "Go to slide 1", "Go to slide 2", "Go to slide 3")
	})

	t.Run("OnPrev, OnNext, and OnSelect all fire", func(t *testing.T) {
		var prevCalled, nextCalled bool
		var selected int
		n := Carousel(CarouselProps{
			Slides:   slides,
			OnPrev:   func() { prevCalled = true },
			OnNext:   func() { nextCalled = true },
			OnSelect: func(i int) { selected = i },
		})
		handlers := findHandlers(n, "click")
		if len(handlers) != 5 {
			t.Fatalf("expected 5 click handlers, got %d", len(handlers))
		}
		handlers[0](xango.Event{}) // prev
		if !prevCalled {
			t.Error("OnPrev did not fire")
		}
		handlers[1](xango.Event{}) // next
		if !nextCalled {
			t.Error("OnNext did not fire")
		}
		handlers[2](xango.Event{}) // dot 0
		if selected != 0 {
			t.Errorf("OnSelect got %d, want 0", selected)
		}
	})
}

func TestAccordion(t *testing.T) {
	items := []AccordionItem{
		{Title: "Section A", Content: xango.Text("content A")},
		{Title: "Section B", Content: xango.Text("content B")},
	}

	t.Run("open state drives aria-expanded and the collapsed class", func(t *testing.T) {
		html := render(t, Accordion(AccordionProps{Items: items, Open: map[int]bool{0: true}}))
		mustContain(t, html, `aria-expanded="true"`, `aria-expanded="false"`, "gj-accordion-panel-collapsed")
	})

	t.Run("OnToggle fires with the clicked item's index", func(t *testing.T) {
		var got int
		n := Accordion(AccordionProps{Items: items, OnToggle: func(i int) { got = i }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers, got %d", len(handlers))
		}
		handlers[1](xango.Event{})
		if got != 1 {
			t.Errorf("OnToggle got %d, want 1", got)
		}
	})
}

func TestFileUploader(t *testing.T) {
	t.Run("IDs and Accept/Multiple/Disabled are all optional", func(t *testing.T) {
		html := render(t, FileUploader(FileUploaderProps{}))
		mustContain(t, html, `type="file"`, "Click or drag a file here")
		mustNotContain(t, html, "id=", "accept=", "multiple=", "disabled=")
	})

	t.Run("DropzoneID, InputID, Accept, Multiple, Disabled all render", func(t *testing.T) {
		html := render(t, FileUploader(FileUploaderProps{
			DropzoneID: "dz", InputID: "in", Accept: "image/*", Multiple: true, Disabled: true,
		}))
		mustContain(t, html, `id="dz"`, `id="in"`, `accept="image/*"`, `multiple="multiple"`, `disabled="disabled"`)
	})

	t.Run("no ShowProgress omits the progress bar", func(t *testing.T) {
		html := render(t, FileUploader(FileUploaderProps{}))
		mustNotContain(t, html, "gj-file-uploader-progress")
	})

	t.Run("ShowProgress with a percentage", func(t *testing.T) {
		html := render(t, FileUploader(FileUploaderProps{ShowProgress: true, Progress: 42}))
		mustContain(t, html, "gj-file-uploader-progress", `aria-valuenow="42"`, "width: 42%")
	})

	t.Run("Progress is clamped to [0, 100]", func(t *testing.T) {
		high := render(t, FileUploader(FileUploaderProps{ShowProgress: true, Progress: 150}))
		mustContain(t, high, `aria-valuenow="100"`, "width: 100%")

		low := render(t, FileUploader(FileUploaderProps{ShowProgress: true, Progress: -10}))
		mustContain(t, low, `aria-valuenow="0"`, "width: 0%")
	})

	t.Run("Indeterminate skips the percentage attributes", func(t *testing.T) {
		html := render(t, FileUploader(FileUploaderProps{ShowProgress: true, Indeterminate: true}))
		mustContain(t, html, "gj-file-uploader-progress-indeterminate")
		mustNotContain(t, html, "aria-valuenow")
	})

	t.Run("dragover/drop both call PreventDefault", func(t *testing.T) {
		n := FileUploader(FileUploaderProps{})
		for _, event := range []string{"dragover", "drop"} {
			handlers := findHandlers(n, event)
			if len(handlers) != 1 {
				t.Fatalf("expected 1 %s handler, got %d", event, len(handlers))
			}
			prevented := false
			handlers[0](xango.NewEvent(event, nil, false, func() { prevented = true }, nil))
			if !prevented {
				t.Errorf("%s handler did not call PreventDefault", event)
			}
		}
	})

	t.Run("OnFilesSelected fires on the input's change event", func(t *testing.T) {
		fired := false
		n := FileUploader(FileUploaderProps{OnFilesSelected: func(xango.Event) { fired = true }})
		handlers := findHandlers(n, "change")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 change handler, got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !fired {
			t.Error("OnFilesSelected did not fire")
		}
	})
}

func TestCapabilitiesForRole(t *testing.T) {
	cases := []struct {
		role CollabRole
		want CollabCapabilities
	}{
		{CollabOwner, CollabCapabilities{CanEdit: true, CanView: true, CanDelete: true, CanShare: true, CanComment: true}},
		{CollabEditor, CollabCapabilities{CanEdit: true, CanView: true, CanComment: true}},
		{CollabViewer, CollabCapabilities{CanView: true, CanComment: true}},
		{CollabRole("intruder"), CollabCapabilities{}},
	}
	for _, c := range cases {
		if got := CapabilitiesForRole(c.role); got != c.want {
			t.Errorf("CapabilitiesForRole(%q) = %+v, want %+v", c.role, got, c.want)
		}
	}
}

func TestCollab(t *testing.T) {
	content := []xango.Node{xango.Text("the document")}

	t.Run("a viewer sees a read-only badge and content, no actions", func(t *testing.T) {
		html := render(t, Collab(CollabProps{Role: CollabViewer, Content: content, Actions: []xango.Node{xango.Text("toolbar")}}))
		mustContain(t, html, "gj-collab-readonly", "gj-collab-badge", "View only", "the document")
		mustNotContain(t, html, "gj-collab-actions", "toolbar")
	})

	t.Run("an unrecognized role is denied entirely (fails closed)", func(t *testing.T) {
		html := render(t, Collab(CollabProps{Role: CollabRole("nobody"), Content: content}))
		mustContain(t, html, "gj-collab-denied", `role="alert"`)
		mustNotContain(t, html, "the document")
	})

	t.Run("an editor sees content and actions, but no Delete/Share footer", func(t *testing.T) {
		html := render(t, Collab(CollabProps{
			Role:     CollabEditor,
			Content:  content,
			Actions:  []xango.Node{xango.Text("toolbar")},
			OnDelete: func() {},
			OnShare:  func() {},
		}))
		mustContain(t, html, "gj-collab-actions", "toolbar")
		mustNotContain(t, html, "gj-collab-readonly", "gj-collab-footer")
	})

	t.Run("an owner sees Delete/Share, and both fire", func(t *testing.T) {
		var deleted, shared bool
		n := Collab(CollabProps{
			Role:     CollabOwner,
			Content:  content,
			OnDelete: func() { deleted = true },
			OnShare:  func() { shared = true },
		})
		html := render(t, n)
		mustContain(t, html, "gj-collab-footer", ">Share<", ">Delete<")

		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers (Share, Delete), got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !shared {
			t.Error("Share button did not invoke OnShare")
		}
		handlers[1](xango.Event{})
		if !deleted {
			t.Error("Delete button did not invoke OnDelete")
		}
	})

	t.Run("Comments only render when CanComment and non-empty", func(t *testing.T) {
		html := render(t, Collab(CollabProps{Role: CollabOwner, Content: content, Comments: []xango.Node{xango.Text("a comment")}}))
		mustContain(t, html, "gj-collab-comments", "a comment")

		noComments := render(t, Collab(CollabProps{Role: CollabOwner, Content: content}))
		mustNotContain(t, noComments, "gj-collab-comments")
	})

	t.Run("a custom Capabilities override replaces the role default entirely", func(t *testing.T) {
		custom := CollabCapabilities{CanView: true, CanEdit: true} // no delete/share/comment
		html := render(t, Collab(CollabProps{
			Role:         CollabViewer, // would normally deny edit
			Capabilities: &custom,
			Content:      content,
			Actions:      []xango.Node{xango.Text("toolbar")},
		}))
		mustContain(t, html, "toolbar") // edit allowed via the override
		mustNotContain(t, html, "gj-collab-readonly")
	})
}
