package components

import (
	"testing"

	"github.com/elaous/xango"
)

func TestAvatar(t *testing.T) {
	t.Run("with a Src renders an img", func(t *testing.T) {
		html := render(t, Avatar(AvatarProps{Src: "cat.png", Alt: "A cat"}))
		mustContain(t, html, `<img`, `class="gj-avatar gj-avatar-md"`, `src="cat.png"`, `alt="A cat"`)
	})

	t.Run("without a Src falls back to Initials", func(t *testing.T) {
		html := render(t, Avatar(AvatarProps{Initials: "AB"}))
		mustContain(t, html, `role="img"`, `>AB<`)
		mustNotContain(t, html, `<img`)
	})

	t.Run("Size defaults to md", func(t *testing.T) {
		html := render(t, Avatar(AvatarProps{Initials: "X"}))
		mustContain(t, html, "gj-avatar-md")
	})

	t.Run("an explicit Size is honored", func(t *testing.T) {
		html := render(t, Avatar(AvatarProps{Initials: "X", Size: AvatarLg}))
		mustContain(t, html, "gj-avatar-lg")
	})
}

func TestIcon(t *testing.T) {
	t.Run("a built-in Name resolves to its glyph", func(t *testing.T) {
		html := render(t, Icon(IconProps{Name: IconCheck}))
		mustContain(t, html, "✓", "font-size:20px")
	})

	t.Run("Glyph overrides Name", func(t *testing.T) {
		html := render(t, Icon(IconProps{Name: IconCheck, Glyph: "Q"}))
		mustContain(t, html, ">Q<")
		mustNotContain(t, html, "✓")
	})

	t.Run("no Label marks it decorative", func(t *testing.T) {
		html := render(t, Icon(IconProps{Name: IconStar}))
		mustContain(t, html, `aria-hidden="true"`)
		mustNotContain(t, html, "role=\"img\"")
	})

	t.Run("a Label makes it a real accessible image", func(t *testing.T) {
		html := render(t, Icon(IconProps{Name: IconStar, Label: "Favorite"}))
		mustContain(t, html, `role="img"`, `aria-label="Favorite"`)
		mustNotContain(t, html, "aria-hidden")
	})

	t.Run("Size and Color both reach the style attribute", func(t *testing.T) {
		html := render(t, Icon(IconProps{Name: IconPlus, Size: 32, Color: "red"}))
		mustContain(t, html, "font-size:32px", "color:red")
	})
}

func TestChip(t *testing.T) {
	t.Run("plain chip has no remove button", func(t *testing.T) {
		html := render(t, Chip(ChipProps{Label: "tag"}))
		mustContain(t, html, "gj-chip-label", ">tag<")
		mustNotContain(t, html, "gj-chip-remove")
	})

	t.Run("OnRemove renders and fires a remove button", func(t *testing.T) {
		removed := false
		n := Chip(ChipProps{Label: "tag", OnRemove: func(xango.Event) { removed = true }})
		html := render(t, n)
		mustContain(t, html, "gj-chip-remove", `aria-label="Remove tag"`)

		handlers := findHandlers(n, "click")
		if len(handlers) != 1 {
			t.Fatalf("expected exactly 1 click handler, got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if !removed {
			t.Error("clicking the remove button did not invoke OnRemove")
		}
	})

	t.Run("an Icon renders before the label", func(t *testing.T) {
		icon := Icon(IconProps{Name: IconStar})
		html := render(t, Chip(ChipProps{Label: "starred", Icon: &icon}))
		mustContain(t, html, "gj-chip-icon")
	})
}

func TestLoader(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		html := render(t, Loader(LoaderProps{}))
		mustContain(t, html, "gj-loader-md", `role="status"`, `aria-label="Loading"`)
	})

	t.Run("explicit Size and Label", func(t *testing.T) {
		html := render(t, Loader(LoaderProps{Size: LoaderLarge, Label: "Saving"}))
		mustContain(t, html, "gj-loader-lg", `aria-label="Saving"`)
	})
}

func TestProgress(t *testing.T) {
	t.Run("defaults to the linear variant", func(t *testing.T) {
		html := render(t, Progress(ProgressProps{Value: 40}))
		mustContain(t, html, "gj-progress-linear", `aria-valuenow="40"`, "width: 40%")
	})

	t.Run("the circle variant uses a CSS custom property, not SVG", func(t *testing.T) {
		html := render(t, Progress(ProgressProps{Variant: ProgressCircle, Value: 75}))
		mustContain(t, html, "gj-progress-circle", "--gj-progress-value: 75%")
		mustNotContain(t, html, "<svg", "<circle")
	})

	t.Run("Value is clamped to [0, 100]", func(t *testing.T) {
		high := render(t, Progress(ProgressProps{Value: 150}))
		mustContain(t, high, `aria-valuenow="100"`, "width: 100%")

		low := render(t, Progress(ProgressProps{Value: -10}))
		mustContain(t, low, `aria-valuenow="0"`, "width: 0%")
	})

	t.Run("Indeterminate drops the aria-valuenow triad", func(t *testing.T) {
		html := render(t, Progress(ProgressProps{Indeterminate: true}))
		mustContain(t, html, "gj-progress-linear-indeterminate")
		mustNotContain(t, html, "aria-valuenow")
	})

	t.Run("the circle variant also supports Indeterminate", func(t *testing.T) {
		html := render(t, Progress(ProgressProps{Variant: ProgressCircle, Indeterminate: true}))
		mustContain(t, html, "gj-progress-circle-indeterminate")
		mustNotContain(t, html, "aria-valuenow", "--gj-progress-value")
	})

	t.Run("circle Value is also clamped to [0, 100]", func(t *testing.T) {
		high := render(t, Progress(ProgressProps{Variant: ProgressCircle, Value: 150}))
		mustContain(t, high, "--gj-progress-value: 100%")
	})

	t.Run("Label is optional", func(t *testing.T) {
		withLabel := render(t, Progress(ProgressProps{Label: "Upload progress"}))
		mustContain(t, withLabel, `aria-label="Upload progress"`)

		withoutLabel := render(t, Progress(ProgressProps{}))
		mustNotContain(t, withoutLabel, "aria-label")
	})
}

func TestTooltip(t *testing.T) {
	html := render(t, Tooltip(TooltipProps{
		Content:  "More info",
		Children: []xango.Node{xango.Text("hover me")}},
	))
	mustContain(t, html, "gj-tooltip", "gj-tooltip-top", `role="tooltip"`, "More info", "hover me")
}

func TestLink(t *testing.T) {
	t.Run("a plain link", func(t *testing.T) {
		html := render(t, Link(LinkProps{Href: "/home", Children: []xango.Node{xango.Text("Home")}}))
		mustContain(t, html, `<a`, `href="/home"`, ">Home<")
	})

	t.Run("Target _blank adds rel=noopener noreferrer", func(t *testing.T) {
		html := render(t, Link(LinkProps{Href: "/x", Target: "_blank"}))
		mustContain(t, html, `target="_blank"`, `rel="noopener noreferrer"`)
	})

	t.Run("a non-_blank Target has no rel", func(t *testing.T) {
		html := render(t, Link(LinkProps{Href: "/x", Target: "_self"}))
		mustNotContain(t, html, "rel=")
	})

	t.Run("Disabled renders a non-interactive span, not an anchor", func(t *testing.T) {
		html := render(t, Link(LinkProps{Href: "/x", Disabled: true, Children: []xango.Node{xango.Text("nope")}}))
		mustContain(t, html, "<span", "gj-link-disabled", `aria-disabled="true"`, ">nope<")
		mustNotContain(t, html, "<a ", "href")
	})
}

func TestFloating(t *testing.T) {
	t.Run("defaults to bottom-right and fixed positioning", func(t *testing.T) {
		html := render(t, Floating(FloatingProps{Children: []xango.Node{xango.Text("badge")}}))
		mustContain(t, html, "gj-floating-bottom-right", "badge")
		mustNotContain(t, html, "gj-floating-absolute")
	})

	t.Run("Absolute adds the absolute-positioning class", func(t *testing.T) {
		html := render(t, Floating(FloatingProps{Absolute: true}))
		mustContain(t, html, "gj-floating-absolute")
	})

	t.Run("a non-zero Offset sets the CSS variable", func(t *testing.T) {
		html := render(t, Floating(FloatingProps{Offset: 24}))
		mustContain(t, html, "--gj-floating-offset:24px")
	})

	t.Run("Offset 0 omits the style attribute entirely", func(t *testing.T) {
		html := render(t, Floating(FloatingProps{}))
		mustNotContain(t, html, "style=")
	})
}

func TestBox(t *testing.T) {
	t.Run("defaults Height to 100% and mixes division units", func(t *testing.T) {
		html := render(t, Box(BoxProps{
			Divisions: []BoxDivision{
				{Height: "48px", Content: []xango.Node{xango.Text("header")}},
				{Content: []xango.Node{xango.Text("body")}},
				{Height: "20%", Content: []xango.Node{xango.Text("footer")}},
			},
		}))
		mustContain(t, html, "height:100%", "grid-template-rows:48px 1fr 20%", "header", "body", "footer")
	})

	t.Run("an explicit Height overrides the 100% default", func(t *testing.T) {
		html := render(t, Box(BoxProps{Height: "300px", Divisions: []BoxDivision{{}}}))
		mustContain(t, html, "height:300px")
	})
}

func TestListRendersItemsThroughTheRenderProp(t *testing.T) {
	type person struct{ Name string }
	people := []person{{"Ada"}, {"Grace"}}
	n := List(ListProps[person]{
		Items: people,
		Item:  func(p person) xango.Node { return xango.El("b", nil, xango.Text(p.Name)) },
		Key:   func(p person) string { return "k-" + p.Name },
	})
	html := render(t, n)
	mustContain(t, html, `<ul class="gj-list">`, `<li class="gj-list-item" data-key="k-Ada"><b>Ada</b></li>`, `data-key="k-Grace"`)

	mustContain(t, render(t, List(ListProps[int]{Items: []int{1}, Ordered: true, Item: func(i int) xango.Node { return xango.Text("x") }})), "<ol")
	empty := xango.Text("nothing here")
	mustContain(t, render(t, List(ListProps[int]{Empty: &empty})), "nothing here")
	mustContain(t, render(t, List(ListProps[int]{})), `<ul class="gj-list"></ul>`)
	mustContain(t, render(t, List(ListProps[int]{Items: []int{1, 2}})), `<li class="gj-list-item"></li>`)
}
