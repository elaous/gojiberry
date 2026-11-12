package components

import (
	"testing"

	"github.com/elaous/xango"
)

func TestBreadcrumbs(t *testing.T) {
	items := []BreadcrumbItem{
		{Label: "Home", Href: "/"},
		{Label: "Docs", Href: "/docs"},
		{Label: "Current"}, // last item, no Href needed
	}

	t.Run("non-last items with an Href render as links", func(t *testing.T) {
		html := render(t, Breadcrumbs(BreadcrumbsProps{Items: items}))
		mustContain(t, html, `<a class="gj-breadcrumb-link" href="/"`, `<a class="gj-breadcrumb-link" href="/docs"`)
	})

	t.Run("the last item is plain text with aria-current", func(t *testing.T) {
		html := render(t, Breadcrumbs(BreadcrumbsProps{Items: items}))
		mustContain(t, html, `aria-current="page"`, ">Current<")
		mustNotContain(t, html, `href`+`="/Current"`)
	})

	t.Run("an item with no Href always renders as a span", func(t *testing.T) {
		html := render(t, Breadcrumbs(BreadcrumbsProps{Items: []BreadcrumbItem{{Label: "Solo"}}}))
		mustContain(t, html, "<span", ">Solo<")
		mustNotContain(t, html, "<a ")
	})

	t.Run("OnClick fires for a link item", func(t *testing.T) {
		clicked := false
		n := Breadcrumbs(BreadcrumbsProps{Items: []BreadcrumbItem{
			{Label: "Home", Href: "/", OnClick: func(xango.Event) { clicked = true }},
			{Label: "Here"},
		}})
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

func TestPagination(t *testing.T) {
	t.Run("TotalPages < 1 renders nothing", func(t *testing.T) {
		html := render(t, Pagination(PaginationProps{TotalPages: 0}))
		if html != "" {
			t.Errorf("expected empty output, got: %s", html)
		}
	})

	t.Run("a short run has no ellipsis", func(t *testing.T) {
		html := render(t, Pagination(PaginationProps{Page: 1, TotalPages: 5}))
		mustContain(t, html, ">1<", ">2<", ">3<", ">4<", ">5<")
		mustNotContain(t, html, "…")
	})

	t.Run("a long run collapses the middle with an ellipsis", func(t *testing.T) {
		html := render(t, Pagination(PaginationProps{Page: 5, TotalPages: 12}))
		mustContain(t, html, "…", ">1<", ">12<", ">4<", ">5<", ">6<")
	})

	t.Run("Prev is disabled on page 1, Next disabled on the last page", func(t *testing.T) {
		first := render(t, Pagination(PaginationProps{Page: 1, TotalPages: 5}))
		mustContain(t, first, `disabled="disabled"`)

		last := render(t, Pagination(PaginationProps{Page: 5, TotalPages: 5}))
		mustContain(t, last, `disabled="disabled"`)
	})

	t.Run("the active page gets aria-current", func(t *testing.T) {
		html := render(t, Pagination(PaginationProps{Page: 3, TotalPages: 5}))
		mustContain(t, html, `aria-current="page"`)
	})

	t.Run("OnChange fires with the clicked page", func(t *testing.T) {
		var got int
		n := Pagination(PaginationProps{Page: 1, TotalPages: 5, OnChange: func(p int) { got = p }})
		handlers := findHandlers(n, "click")
		if len(handlers) == 0 {
			t.Fatal("expected at least one click handler")
		}
		handlers[1](xango.Event{})
		if got != 2 {
			t.Errorf("OnChange got %d, want 2", got)
		}
	})
}

func TestPaginationWindow(t *testing.T) {
	cases := []struct {
		page, total int
		want        []int
	}{
		{1, 5, []int{1, 2, 3, 4, 5}},
		{1, 7, []int{1, 2, 3, 4, 5, 6, 7}},
		{5, 12, []int{1, 0, 4, 5, 6, 0, 12}},
		{1, 12, []int{1, 2, 0, 12}},
		{12, 12, []int{1, 0, 11, 12}},
	}
	for _, c := range cases {
		got := paginationWindow(c.page, c.total)
		if len(got) != len(c.want) {
			t.Errorf("paginationWindow(%d, %d) = %v, want %v", c.page, c.total, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("paginationWindow(%d, %d) = %v, want %v", c.page, c.total, got, c.want)
				break
			}
		}
	}
}

func TestTabs(t *testing.T) {
	tabs := []Tab{
		{Label: "One", Content: xango.Text("content one")},
		{Label: "Two", Content: xango.Text("content two")},
	}

	t.Run("only the Active tab's content renders", func(t *testing.T) {
		html := render(t, Tabs(TabsProps{Tabs: tabs, Active: 1}))
		mustContain(t, html, "content two")
		mustNotContain(t, html, "content one")
	})

	t.Run("the active tab button gets aria-selected", func(t *testing.T) {
		html := render(t, Tabs(TabsProps{Tabs: tabs, Active: 0}))
		mustContain(t, html, `aria-selected="true"`, `aria-selected="false"`)
	})

	t.Run("an out-of-range Active renders no content, without panicking", func(t *testing.T) {
		html := render(t, Tabs(TabsProps{Tabs: tabs, Active: 5}))
		mustNotContain(t, html, "content one", "content two")
	})

	t.Run("OnSelect fires with the clicked tab's index", func(t *testing.T) {
		var got int
		n := Tabs(TabsProps{Tabs: tabs, OnSelect: func(i int) { got = i }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers, got %d", len(handlers))
		}
		handlers[1](xango.Event{})
		if got != 1 {
			t.Errorf("OnSelect got %d, want 1", got)
		}
	})
}

func TestWizard(t *testing.T) {
	steps := []WizardStep{
		{Title: "Step A", Content: xango.Text("content A")},
		{Title: "Step B", Content: xango.Text("content B")},
		{Title: "Step C", Content: xango.Text("content C")},
	}

	t.Run("shows the active step's content and header state classes", func(t *testing.T) {
		html := render(t, Wizard(WizardProps{Steps: steps, Active: 1}))
		mustContain(t, html, "content B", "gj-wizard-step-header-active", "gj-wizard-step-header-done")
		mustNotContain(t, html, "content A", "content C")
	})

	t.Run("Back is disabled on the first step", func(t *testing.T) {
		html := render(t, Wizard(WizardProps{Steps: steps, Active: 0}))
		mustContain(t, html, `class="gj-wizard-back" type="button" disabled="disabled"`)
	})

	t.Run("Next reads Finish on the last step", func(t *testing.T) {
		html := render(t, Wizard(WizardProps{Steps: steps, Active: 2}))
		mustContain(t, html, ">Finish<")
		mustNotContain(t, html, ">Next<")
	})

	t.Run("OnBack/OnNext/OnStep all fire", func(t *testing.T) {
		var backCalled, nextCalled bool
		var stepped int
		n := Wizard(WizardProps{
			Steps:  steps,
			Active: 1,
			OnBack: func() { backCalled = true },
			OnNext: func() { nextCalled = true },
			OnStep: func(i int) { stepped = i },
		})
		clickHandlers := findHandlers(n, "click")
		if len(clickHandlers) != 5 {
			t.Fatalf("expected 5 click handlers, got %d", len(clickHandlers))
		}
		clickHandlers[2](xango.Event{}) // third step header
		if stepped != 2 {
			t.Errorf("OnStep got %d, want 2", stepped)
		}
		clickHandlers[3](xango.Event{}) // Back
		if !backCalled {
			t.Error("OnBack did not fire")
		}
		clickHandlers[4](xango.Event{}) // Next
		if !nextCalled {
			t.Error("OnNext did not fire")
		}
	})
}
