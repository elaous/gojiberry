//go:build js && wasm

package html

import "syscall/js"

// CanvasElement is an Element known to be a canvas.
type CanvasElement struct{ Element }

// AsCanvas casts e to a CanvasElement, or returns nil.
func (e *Element) AsCanvas() *CanvasElement {
	if e.GetTagName() != "CANVAS" {
		return nil
	}
	return &CanvasElement{Element: *e}
}

// Width returns the canvas's pixel width.
func (c *CanvasElement) Width() int { return c.Get("width").Int() }

// SetWidth sets the canvas's pixel width.
func (c *CanvasElement) SetWidth(w int) { c.Set("width", w) }

// Height returns the canvas's pixel height.
func (c *CanvasElement) Height() int { return c.Get("height").Int() }

// SetHeight sets the canvas's pixel height.
func (c *CanvasElement) SetHeight(h int) { c.Set("height", h) }

// ToDataURL returns the canvas bitmap encoded as mimeType.
func (c *CanvasElement) ToDataURL(mimeType string) string {
	return c.Call("toDataURL", mimeType).String()
}

// AnchorElement is an Element known to be an <a>.
type AnchorElement struct{ Element }

// AsAnchor casts e to an AnchorElement, or returns nil.
func (e *Element) AsAnchor() *AnchorElement {
	if e.GetTagName() != "A" {
		return nil
	}
	return &AnchorElement{Element: *e}
}

// Href returns the anchor's resolved target URL.
func (a *AnchorElement) Href() string { return a.Get("href").String() }

// SetHref sets the anchor's target URL.
func (a *AnchorElement) SetHref(href string) { a.Set("href", href) }

// Target returns the anchor's target browsing context (e.g. "_blank").
func (a *AnchorElement) Target() string { return a.Get("target").String() }

// SetTarget sets the anchor's target browsing context.
func (a *AnchorElement) SetTarget(target string) { a.Set("target", target) }

// Rel returns the anchor's rel attribute (e.g. "noopener noreferrer").
func (a *AnchorElement) Rel() string { return a.Get("rel").String() }

// SetRel sets the anchor's rel attribute.
func (a *AnchorElement) SetRel(rel string) { a.Set("rel", rel) }

// Download returns the anchor's download attribute (the suggested filename).
func (a *AnchorElement) Download() string { return a.Get("download").String() }

// SetDownload marks the anchor as a download.
func (a *AnchorElement) SetDownload(name string) { a.Set("download", name) }

// ButtonElement is an Element known to be a <button>.
type ButtonElement struct{ Element }

// AsButton casts e to a ButtonElement, or returns nil.
func (e *Element) AsButton() *ButtonElement {
	if e.GetTagName() != "BUTTON" {
		return nil
	}
	return &ButtonElement{Element: *e}
}

// Disabled returns whether the button is disabled.
func (b *ButtonElement) Disabled() bool { return b.Get("disabled").Bool() }

// SetDisabled enables or disables the button.
func (b *ButtonElement) SetDisabled(disabled bool) { b.Set("disabled", disabled) }

// Type returns the button's type ("submit", "reset", or "button").
func (b *ButtonElement) Type() string { return b.Get("type").String() }

// SetType sets the button's type.
func (b *ButtonElement) SetType(t string) { b.Set("type", t) }

// SelectElement is an Element known to be a <select>.
type SelectElement struct{ Element }

// AsSelect casts e to a SelectElement, or returns nil.
func (e *Element) AsSelect() *SelectElement {
	if e.GetTagName() != "SELECT" {
		return nil
	}
	return &SelectElement{Element: *e}
}

// SelectedIndex returns the selected option's index, or -1.
func (s *SelectElement) SelectedIndex() int { return s.Get("selectedIndex").Int() }

// SetSelectedIndex selects the option at index.
func (s *SelectElement) SetSelectedIndex(index int) { s.Set("selectedIndex", index) }

// Multiple returns whether multiple options can be selected at once.
func (s *SelectElement) Multiple() bool { return s.Get("multiple").Bool() }

// SetMultiple sets whether multiple options can be selected at once.
func (s *SelectElement) SetMultiple(multiple bool) { s.Set("multiple", multiple) }

// Options returns the select's <option> elements in document order.
func (s *SelectElement) Options() []Element {
	return wrapAll(s.Get("options"))
}

// InputElement is an Element known to be an <input>.
type InputElement struct{ Element }

// AsInput casts e to an InputElement, or returns nil.
func (e *Element) AsInput() *InputElement {
	if e.GetTagName() != "INPUT" {
		return nil
	}
	return &InputElement{Element: *e}
}

// Checked returns whether a checkbox/radio input is checked.
func (i *InputElement) Checked() bool { return i.Get("checked").Bool() }

// SetChecked checks or unchecks a checkbox/radio input.
func (i *InputElement) SetChecked(checked bool) { i.Set("checked", checked) }

// Type returns the input's type (e.g. "text", "checkbox", "file").
func (i *InputElement) Type() string { return i.Get("type").String() }

// SetType sets the input's type.
func (i *InputElement) SetType(t string) { i.Set("type", t) }

// Disabled returns whether the input is disabled.
func (i *InputElement) Disabled() bool { return i.Get("disabled").Bool() }

// SetDisabled enables or disables the input.
func (i *InputElement) SetDisabled(disabled bool) { i.Set("disabled", disabled) }

// Files returns the selected files of a file input.
func (i *InputElement) Files() []js.Value {
	files := i.Get("files")
	if files.IsUndefined() || files.IsNull() {
		return nil
	}
	out := make([]js.Value, files.Length())
	for idx := range out {
		out[idx] = files.Index(idx)
	}
	return out
}
