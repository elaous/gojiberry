package gio

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/xango"
)

type ClassStyle struct {
	PaddingY, PaddingX unit.Dp
	Background         color.NRGBA
	BorderColor        color.NRGBA
	BorderWidth        unit.Dp
	CornerRadius       unit.Dp
	Alignment          layout.Alignment
}

// Renderer walks xango.Node trees onto a Gio layout.Context.
type Renderer struct {
	clickables map[string]*widget.Clickable
	checkboxes map[string]*widget.Bool
	editors    map[string]*widget.Editor
	selectOpen map[string]bool

	focusRequests map[string]bool

	toggleRequests map[string]bool

	valueRequests map[string]string

	checkedRequests map[string]bool

	// key is "<id>\x00<event>"
	handlers map[string]xango.EventHandler

	Styles map[string]ClassStyle

	IconFont *IconFont
}

func New() *Renderer {
	return &Renderer{
		clickables:      make(map[string]*widget.Clickable),
		checkboxes:      make(map[string]*widget.Bool),
		editors:         make(map[string]*widget.Editor),
		selectOpen:      make(map[string]bool),
		focusRequests:   make(map[string]bool),
		toggleRequests:  make(map[string]bool),
		valueRequests:   make(map[string]string),
		checkedRequests: make(map[string]bool),
		handlers:        make(map[string]xango.EventHandler),
	}
}

func (r *Renderer) Toggle(id string) bool {
	if _, ok := r.checkboxes[id]; !ok {
		return false
	}
	r.toggleRequests[id] = true
	return true
}

func (r *Renderer) SetValue(id, value string) {
	r.valueRequests[id] = value
}

func (r *Renderer) SetChecked(id string, checked bool) {
	r.checkedRequests[id] = checked
}

func (r *Renderer) Focus(id string) bool {
	if _, ok := r.editors[id]; !ok {
		return false
	}
	r.focusRequests[id] = true
	return true
}

func (r *Renderer) EditorTag(id string) (event.Tag, bool) {
	ed, ok := r.editors[id]
	if !ok {
		return nil, false
	}
	return ed, true
}

func (r *Renderer) On(id, event string, fn xango.EventHandler) {
	r.handlers[id+"\x00"+event] = fn
}

func (r *Renderer) handlerFor(id, event string) xango.EventHandler {
	return r.handlers[id+"\x00"+event]
}

func (r *Renderer) Layout(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	switch n.Kind {
	case xango.KindText:
		return material.Label(th, th.TextSize, n.Text).Layout(gtx)
	case xango.KindFragment:
		return r.layoutChildren(gtx, th, n.Children, layout.Vertical, layout.Start)
	case xango.KindHost:
		return layout.Dimensions{}
	case xango.KindRaw:
		return layout.Dimensions{}
	}

	style := r.styleOf(n)
	var content layout.Widget
	switch n.Tag {
	case "div":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutDivContent(gtx, th, n, style.Alignment)
		}
	case "span", "p":
		if hasElementChild(n) {
			content = func(gtx layout.Context) layout.Dimensions {
				return r.layoutChildren(gtx, th, n.Children, axisOf(n), style.Alignment)
			}
		} else {
			text := textOf(n)
			lbl := material.Label(th, th.TextSize, text)
			if r.IconFont != nil && hasClass(n, r.IconFont.Class) {
				if glyph, ok := r.IconFont.Translate(text); ok {
					lbl = material.Label(th, th.TextSize, string(glyph))
					lbl.Font.Typeface = r.IconFont.Typeface
				}
			}
			content = lbl.Layout
		}
	case "button":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutButton(gtx, th, n)
		}
	case "input", "textarea":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutInput(gtx, th, n)
		}
	case "select":
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutSelect(gtx, th, n)
		}
	default:
		content = func(gtx layout.Context) layout.Dimensions {
			return r.layoutChildren(gtx, th, n.Children, layout.Vertical, layout.Start)
		}
	}
	return r.styled(gtx, style, content)
}

func classesOf(n xango.Node) []string {
	for _, a := range n.Attrs {
		if a.Name == "class" {
			return strings.Fields(a.Value)
		}
	}
	return nil
}

func hasClass(n xango.Node, class string) bool {
	for _, c := range classesOf(n) {
		if c == class {
			return true
		}
	}
	return false
}

func (r *Renderer) styleOf(n xango.Node) ClassStyle {
	var merged ClassStyle
	for _, class := range classesOf(n) {
		s, ok := r.Styles[class]
		if !ok {
			continue
		}
		if s.PaddingY != 0 {
			merged.PaddingY = s.PaddingY
		}
		if s.PaddingX != 0 {
			merged.PaddingX = s.PaddingX
		}
		if s.Background != (color.NRGBA{}) {
			merged.Background = s.Background
		}
		if s.BorderColor != (color.NRGBA{}) {
			merged.BorderColor = s.BorderColor
		}
		if s.BorderWidth != 0 {
			merged.BorderWidth = s.BorderWidth
		}
		if s.CornerRadius != 0 {
			merged.CornerRadius = s.CornerRadius
		}
		if s.Alignment != layout.Start {
			merged.Alignment = s.Alignment
		}
	}
	return merged
}

func (r *Renderer) styled(gtx layout.Context, style ClassStyle, content layout.Widget) layout.Dimensions {
	if style == (ClassStyle{}) {
		return content(gtx)
	}
	macro := op.Record(gtx.Ops)
	inset := layout.Inset{Top: style.PaddingY, Bottom: style.PaddingY, Left: style.PaddingX, Right: style.PaddingX}
	dims := inset.Layout(gtx, content)
	call := macro.Stop()

	if style.Background != (color.NRGBA{}) || style.BorderWidth > 0 {
		paintBox(gtx, dims.Size, style)
	}
	call.Add(gtx.Ops)
	return dims
}

func paintBox(gtx layout.Context, size image.Point, style ClassStyle) {
	rr := gtx.Dp(style.CornerRadius)
	rect := clip.RRect{
		Rect: image.Rectangle{Max: size},
		SE:   rr, SW: rr, NE: rr, NW: rr,
	}
	if style.Background != (color.NRGBA{}) {
		paint.FillShape(gtx.Ops, style.Background, rect.Op(gtx.Ops))
	}
	if style.BorderWidth > 0 {
		width := float32(gtx.Dp(style.BorderWidth))
		paint.FillShape(gtx.Ops, style.BorderColor, clip.Stroke{Path: rect.Path(gtx.Ops), Width: width}.Op())
	}
}

func idOf(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "id" {
			return a.Value
		}
	}
	return ""
}

func axisOf(n xango.Node) layout.Axis {
	for _, a := range n.Attrs {
		if a.Name == "data-axis" && a.Value == "horizontal" {
			return layout.Horizontal
		}
	}
	return layout.Vertical
}

func hasElementChild(n xango.Node) bool {
	for _, c := range n.Children {
		if c.Kind != xango.KindText {
			return true
		}
	}
	return false
}

func textOf(n xango.Node) string {
	var b strings.Builder
	for _, c := range n.Children {
		if c.Kind == xango.KindText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func (r *Renderer) layoutChildren(gtx layout.Context, th *material.Theme, children []xango.Node, axis layout.Axis, alignment layout.Alignment) layout.Dimensions {
	flex := layout.Flex{Axis: axis, Alignment: alignment}
	flexChildren := make([]layout.FlexChild, 0, len(children))
	for _, c := range children {
		c := c
		flexChildren = append(flexChildren, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return r.Layout(gtx, th, c)
		}))
	}
	return flex.Layout(gtx, flexChildren...)
}

func clickHandler(n xango.Node) xango.EventHandler {
	for _, a := range n.Attrs {
		if a.Name == "click" && a.Handler != nil {
			return a.Handler
		}
	}
	return nil
}

func (r *Renderer) Click(id string) bool {
	clk, ok := r.clickables[id]
	if !ok {
		return false
	}
	clk.Click()
	return true
}

func (r *Renderer) layoutDivContent(gtx layout.Context, th *material.Theme, n xango.Node, alignment layout.Alignment) layout.Dimensions {
	children := func(gtx layout.Context) layout.Dimensions {
		return r.layoutChildren(gtx, th, n.Children, axisOf(n), alignment)
	}
	id := n.Key
	if id == "" {
		id = idOf(n)
	}
	handler := clickHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "click")
	}
	if handler == nil {
		return children(gtx)
	}
	clk, ok := r.clickables[id]
	if !ok {
		clk = new(widget.Clickable)
		r.clickables[id] = clk
	}
	for clk.Clicked(gtx) {
		handler(xango.NewEvent("click", nil, false, nil, nil))
	}
	return clk.Layout(gtx, children)
}

// mirrors material.Button's hardcoded Inset
var buttonInset = layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}

// aria-label wins: title alone is unreliably announced
func accessibleNameOf(n xango.Node) string {
	var title string
	for _, a := range n.Attrs {
		switch a.Name {
		case "aria-label":
			return a.Value
		case "title":
			title = a.Value
		}
	}
	return title
}

func (r *Renderer) layoutButton(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}
	clk, ok := r.clickables[id]
	if !ok {
		clk = new(widget.Clickable)
		r.clickables[id] = clk
	}
	handler := clickHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "click")
	}
	if handler != nil {
		for clk.Clicked(gtx) {
			handler(xango.NewEvent("click", nil, false, nil, nil))
		}
	}
	if !hasElementChild(n) {
		return material.Button(th, clk, textOf(n)).Layout(gtx)
	}
	return material.ButtonLayoutStyle{
		Background:   th.Palette.ContrastBg,
		CornerRadius: 4,
		Button:       clk,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return buttonInset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if name := accessibleNameOf(n); name != "" {
				semantic.DescriptionOp(name).Add(gtx.Ops)
			}
			return r.layoutChildren(gtx, th, n.Children, layout.Horizontal, layout.Middle)
		})
	})
}

func inputType(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "type" {
			return a.Value
		}
	}
	return "text"
}

func valueOf(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "value" {
			return a.Value
		}
	}
	return ""
}

func checkedOf(n xango.Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "checked" {
			return true
		}
	}
	return false
}

func placeholderOf(n xango.Node) string {
	for _, a := range n.Attrs {
		if a.Name == "placeholder" {
			return a.Value
		}
	}
	return ""
}

func changeHandler(n xango.Node) xango.EventHandler {
	for _, a := range n.Attrs {
		if a.Name == "change" && a.Handler != nil {
			return a.Handler
		}
	}
	return nil
}

func (r *Renderer) layoutInput(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}

	if inputType(n) == "checkbox" {
		cb, ok := r.checkboxes[id]
		if !ok {
			cb = &widget.Bool{Value: checkedOf(n)}
			r.checkboxes[id] = cb
		}
		if checked, pending := r.checkedRequests[id]; pending {
			delete(r.checkedRequests, id)
			cb.Value = checked
		}
		changed := cb.Update(gtx)
		if r.toggleRequests[id] {
			delete(r.toggleRequests, id)
			cb.Value = !cb.Value
			changed = true
		}
		if changed {
			handler := changeHandler(n)
			if handler == nil {
				handler = r.handlerFor(id, "change")
			}
			if handler != nil {
				handler(xango.NewEvent("change", cb.Value, cb.Value, nil, nil))
			}
		}
		return material.CheckBox(th, cb, "").Layout(gtx)
	}

	ed, ok := r.editors[id]
	if !ok {
		ed = &widget.Editor{SingleLine: n.Tag != "textarea"}
		ed.SetText(valueOf(n))
		r.editors[id] = ed
	}
	if v, pending := r.valueRequests[id]; pending {
		delete(r.valueRequests, id)
		ed.SetText(v)
	}
	if r.focusRequests[id] {
		delete(r.focusRequests, id)
		gtx.Execute(key.FocusCmd{Tag: ed})
	}
	changed := false
	for {
		ev, ok := ed.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			changed = true
		}
	}
	if changed {
		handler := changeHandler(n)
		if handler == nil {
			handler = r.handlerFor(id, "change")
		}
		if handler != nil {
			handler(xango.NewEvent("change", ed.Text(), false, nil, nil))
		}
	}
	return material.Editor(th, ed, placeholderOf(n)).Layout(gtx)
}

func selectedAttr(n xango.Node) bool {
	for _, a := range n.Attrs {
		if a.Name == "selected" {
			return true
		}
	}
	return false
}

func selectedOption(n xango.Node) (value, label string, ok bool) {
	for _, c := range n.Children {
		if c.Tag != "option" {
			continue
		}
		if selectedAttr(c) {
			return valueOf(c), textOf(c), true
		}
	}
	return "", "", false
}

func (r *Renderer) layoutSelect(gtx layout.Context, th *material.Theme, n xango.Node) layout.Dimensions {
	id := n.Key
	if id == "" {
		id = idOf(n)
	}

	trigger, ok := r.clickables[id]
	if !ok {
		trigger = new(widget.Clickable)
		r.clickables[id] = trigger
	}
	for trigger.Clicked(gtx) {
		r.selectOpen[id] = !r.selectOpen[id]
	}

	_, label, hasSelection := selectedOption(n)
	if !hasSelection {
		label = "(select)"
	}

	if !r.selectOpen[id] {
		return material.Button(th, trigger, label+" ▾").Layout(gtx)
	}

	handler := changeHandler(n)
	if handler == nil {
		handler = r.handlerFor(id, "change")
	}
	rows := make([]layout.FlexChild, 0, len(n.Children)+1)
	rows = append(rows, layout.Rigid(material.Button(th, trigger, label+" ▴").Layout))
	for _, c := range n.Children {
		if c.Tag != "option" {
			continue
		}
		value := valueOf(c)
		optLabel := textOf(c)
		rowID := id + "\x00" + value
		clk, ok := r.clickables[rowID]
		if !ok {
			clk = new(widget.Clickable)
			r.clickables[rowID] = clk
		}
		for clk.Clicked(gtx) {
			r.selectOpen[id] = false
			if handler != nil {
				handler(xango.NewEvent("change", value, false, nil, nil))
			}
		}
		rows = append(rows, layout.Rigid(material.Button(th, clk, optLabel).Layout))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}
