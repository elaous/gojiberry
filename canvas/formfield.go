package canvas

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FormKind is what a form field takes.
type FormKind string

const (
	FormText     FormKind = "text"     // one line of text
	FormTextArea FormKind = "textarea" // several lines
	FormEmail    FormKind = "email"
	FormNumber   FormKind = "number"
	FormPhone    FormKind = "phone"
	FormURL      FormKind = "url" // a web address
	FormDate     FormKind = "date"
	FormCheckbox FormKind = "checkbox"
	FormSelect   FormKind = "select" // one of Options
)

var FormKinds = []FormKind{FormText, FormTextArea, FormEmail, FormNumber, FormPhone, FormURL, FormDate, FormCheckbox, FormSelect}

// Valid reports whether k is a kind this package knows.
func (k FormKind) Valid() bool {
	for _, v := range FormKinds {
		if v == k {
			return true
		}
	}
	return false
}

const (
	FormProblemRequired = "required"
	FormProblemEmail    = "email"
	FormProblemNumber   = "number"
	FormProblemPhone    = "phone"
	FormProblemURL      = "url"
	FormProblemDate     = "date"
	FormProblemChoice   = "choice"  // the value is not one of Options
	FormProblemPattern  = "pattern" // the value does not match Pattern
)

type FormFieldObject struct {
	Kind        FormKind
	Name, Label string
	// Placeholder is shown, dimmed, while the field is empty.
	Placeholder string
	// Pattern: a Go regular expression the whole value must match.
	Pattern  string
	Required bool
	// Options are the choices of a FormSelect.
	Options []string
	// Value is what the person wrote, as text.
	Value string
	// Locked fixes the properties; the host refuses changes.
	Locked           bool
	LockKey, LockSig string

	OnFill func(*FormFieldObject)
	OnEdit func(*FormFieldObject)

	w, h         float64
	eye, pen, cb [4]float64
}

const (
	formFieldW   = 260.0
	formFieldH   = 38.0
	formAreaH    = 64.0
	formCheckH   = 26.0
	formIconSize = 16.0
)

// NewFormField is a field of the kind with a label.
func NewFormField(kind FormKind, label string) *FormFieldObject {
	if !kind.Valid() {
		kind = FormText
	}
	return &FormFieldObject{Kind: kind, Label: label}
}

var (
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	phonePattern = regexp.MustCompile(`^\+?[0-9][0-9 ().\-]{3,}$`)
)

// PatternError explains why Pattern is not a regular expression.
func (f *FormFieldObject) PatternError() string {
	if f.Pattern == "" {
		return ""
	}
	if _, err := regexp.Compile(f.Pattern); err != nil {
		return err.Error()
	}
	return ""
}

// Problem returns the FormProblem code for the value, or "".
func (f *FormFieldObject) Problem() string {
	v := strings.TrimSpace(f.Value)
	if f.Kind == FormCheckbox {
		if f.Required && f.Value != "true" {
			return FormProblemRequired
		}
		return ""
	}
	if v == "" {
		if f.Required {
			return FormProblemRequired
		}
		return ""
	}
	switch f.Kind {
	case FormEmail:
		if !emailPattern.MatchString(v) {
			return FormProblemEmail
		}
	case FormNumber:
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return FormProblemNumber
		}
	case FormPhone:
		if !phonePattern.MatchString(v) {
			return FormProblemPhone
		}
	case FormURL:
		if low := strings.ToLower(v); (!strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://")) || !SafeLinkURL(v) || len(v) < 9 {
			return FormProblemURL
		}
	case FormDate:
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return FormProblemDate
		}
	case FormSelect:
		found := false
		for _, o := range f.Options {
			if o == f.Value {
				found = true
			}
		}
		if !found {
			return FormProblemChoice
		}
	}
	if f.Pattern != "" {
		re, err := regexp.Compile(`^(?:` + f.Pattern + `)$`)
		if err == nil && !re.MatchString(f.Value) {
			return FormProblemPattern
		}
	}
	return ""
}

// Empty says nothing was written in the field.
func (f *FormFieldObject) Empty() bool {
	if f.Kind == FormCheckbox {
		return f.Value != "true"
	}
	return strings.TrimSpace(f.Value) == ""
}

// Clear empties the field.
func (f *FormFieldObject) Clear() { f.Value = "" }

func (f *FormFieldObject) Words() string {
	label := strings.TrimSpace(f.Label)
	if f.Kind == FormCheckbox {
		box := "[ ]"
		if f.Value == "true" {
			box = "[x]"
		}
		return strings.TrimSpace(box + " " + label)
	}
	if label == "" {
		return f.Value
	}
	return label + ": " + f.Value
}

func (f *FormFieldObject) Measure(p Painter, maxW, _ float64) (float64, float64) {
	switch f.Kind {
	case FormCheckbox:
		p.SetFont("14px sans-serif")
		f.w, f.h = min(maxW, 34+p.MeasureTextWidth(f.Label)+2*formIconSize+12), formCheckH
	case FormTextArea:
		f.w, f.h = min(maxW, formFieldW), formAreaH
	default:
		f.w, f.h = min(maxW, formFieldW), formFieldH
	}
	if f.w <= 0 {
		f.w = formFieldW
	}
	return f.w, f.h
}

func (f *FormFieldObject) DrawAt(p Painter, x, y float64) {
	problem := f.Problem()
	border := "#8a94a6"
	if problem != "" {
		border = "#d93025"
	}
	bg := "#ffffff"
	if f.Locked {
		bg = "#f1f3f4"
	}
	p.SetFillStyle(border)
	p.FillRect(x, y+1, f.w, f.h-2)
	p.SetFillStyle(bg)
	p.FillRect(x+1, y+2, f.w-2, f.h-4)
	f.eye, f.pen, f.cb = [4]float64{}, [4]float64{}, [4]float64{}
	right := f.w - 4
	if !f.Locked {
		f.pen = [4]float64{right - formIconSize, 3, formIconSize, formIconSize}
		right -= formIconSize + 2
	}
	f.eye = [4]float64{right - formIconSize, 3, formIconSize, formIconSize}
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	if f.Kind == FormCheckbox {
		f.cb = [4]float64{6, 5, 15, 15}
		p.SetFillStyle("#1f2328")
		p.FillRect(x+f.cb[0], y+f.cb[1], f.cb[2], f.cb[3])
		p.SetFillStyle("#ffffff")
		p.FillRect(x+f.cb[0]+1.5, y+f.cb[1]+1.5, f.cb[2]-3, f.cb[3]-3)
		if f.Value == "true" {
			p.SetStrokeStyle("#1f2328")
			p.SetLineWidth(2)
			p.BeginPath()
			p.MoveTo(x+f.cb[0]+3, y+f.cb[1]+8)
			p.LineTo(x+f.cb[0]+6.5, y+f.cb[1]+11.5)
			p.LineTo(x+f.cb[0]+12, y+f.cb[1]+3.5)
			p.Stroke()
		}
		p.SetFont("14px sans-serif")
		p.SetFillStyle("#1f2328")
		p.FillText(clipText(p, f.Label, f.eye[0]-30), x+30, y+18)
		f.icons(p, x, y)
		return
	}
	p.SetFont("11px sans-serif")
	p.SetFillStyle("#5f6b7a")
	label := f.Label
	if f.Required {
		label += " *"
	}
	p.FillText(clipText(p, label, f.eye[0]-10), x+6, y+14)
	p.SetFont("14px sans-serif")
	room := f.w - 12
	switch {
	case f.Value != "" && f.Kind != FormTextArea:
		p.SetFillStyle("#1f2328")
		p.FillText(clipText(p, f.Value, room), x+6, y+f.h-9)
	case f.Value != "":
		p.SetFillStyle("#1f2328")
		for i, line := range strings.Split(f.Value, "\n") {
			if i > 2 {
				break
			}
			p.FillText(clipText(p, line, room), x+6, y+32+float64(i)*(16))
		}
	case f.Placeholder != "":
		p.SetFillStyle("#8a94a6")
		p.FillText(clipText(p, f.Placeholder, room), x+6, y+f.h-9)
	}
	f.icons(p, x, y)
}

func (f *FormFieldObject) icons(p Painter, x, y float64) {
	p.SetStrokeStyle("#5f6b7a")
	p.SetFillStyle("#5f6b7a")
	p.SetLineWidth(1.4)
	e := f.eye
	cx, cy := x+e[0]+e[2]/2, y+e[1]+e[3]/2
	p.BeginPath() // the eye: a lens with a pupil
	p.MoveTo(cx-7, cy)
	p.LineTo(cx-3.5, cy-3.5)
	p.LineTo(cx+3.5, cy-3.5)
	p.LineTo(cx+7, cy)
	p.LineTo(cx+3.5, cy+3.5)
	p.LineTo(cx-3.5, cy+3.5)
	p.LineTo(cx-7, cy)
	p.Stroke()
	p.FillRect(cx-1.5, cy-1.5, 3, 3)
	if f.pen[2] > 0 {
		pn := f.pen
		px, py := x+pn[0], y+pn[1]
		p.BeginPath() // the pen: a slanted bar with a point
		p.MoveTo(px+12, py+3)
		p.LineTo(px+4, py+11)
		p.LineTo(px+3, py+14)
		p.LineTo(px+6, py+13)
		p.LineTo(px+14, py+5)
		p.LineTo(px+12, py+3)
		p.Stroke()
	} else { // locked: a small padlock where the pen would be
		lx, ly := x+f.eye[0]+f.eye[2]+4, y+5
		p.FillRect(lx, ly+5, 9, 7)
		p.BeginPath()
		p.MoveTo(lx+2, ly+5)
		p.LineTo(lx+2, ly+2)
		p.LineTo(lx+7, ly+2)
		p.LineTo(lx+7, ly+5)
		p.Stroke()
	}
}

func clipText(p Painter, s string, w float64) string {
	if w <= 0 || p.MeasureTextWidth(s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && p.MeasureTextWidth(string(rs)+"…") > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}

// ClickObject takes presses; (x, y) are relative to its top-left.
type ClickObject interface {
	ClickAt(x, y float64) (used, changed bool)
}

func (f *FormFieldObject) ClickAt(x, y float64) (used, changed bool) {
	in := func(r [4]float64) bool { return r[2] > 0 && x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3] }
	switch {
	case in(f.eye):
		if f.OnFill != nil {
			f.OnFill(f)
		}
		return true, false
	case in(f.pen) && !f.Locked:
		if f.OnEdit != nil {
			f.OnEdit(f)
		}
		return true, false
	case f.Kind == FormCheckbox && x >= 0 && x < f.eye[0]-2:
		if f.Value == "true" {
			f.Value = ""
		} else {
			f.Value = "true"
		}
		return true, true
	}
	return false, false
}

// DragObject is an inline object that can be dragged.
type DragObject interface {
	DragStart(x, y float64) bool
	DragMove(x, y float64) (changed bool)
	DragEnd()
}

var _ ClickObject = (*FormFieldObject)(nil)

// FormFields lists the form fields in reading order.
func (f *TextFlow) FormFields() []*FormFieldObject {
	var out []*FormFieldObject
	for _, it := range f.Items {
		if in, ok := it.(Inline); ok {
			switch o := in.Object.(type) {
			case *FormFieldObject:
				out = append(out, o)
			case *TableObject:
				if o != nil && o.View != nil && o.View.Table != nil {
					out = append(out, o.View.Table.FieldList()...)
				}
			}
		}
	}
	return out
}

func (f *TextFlow) Tables() []*TableObject {
	var out []*TableObject
	for _, it := range f.Items {
		if in, ok := it.(Inline); ok {
			if o, ok := in.Object.(*TableObject); ok && o != nil && o.View != nil && o.View.Table != nil {
				out = append(out, o)
			}
		}
	}
	return out
}

// ClearFormFields empties every form field; reports how many were filled.
func (f *TextFlow) ClearFormFields() int {
	n := 0
	for _, ff := range f.FormFields() {
		if !ff.Empty() {
			ff.Clear()
			n++
		}
	}
	for _, o := range f.Tables() {
		o.View.Table.SyncFields()
	}
	if n > 0 {
		f.changed()
	}
	return n
}

func (f *TextFlow) objectDragStart(x, y float64) bool {
	for _, ln := range f.Layout().Lines {
		for _, o := range ln.Objects {
			if x < o.X || x >= o.X+o.W || y < o.Y || y >= o.Y+o.H {
				continue
			}
			if do, ok := o.Object.(DragObject); ok && do.DragStart(x-o.X, y-o.Y) {
				f.objDrag, f.objDragX, f.objDragY = do, o.X, o.Y
				return true
			}
		}
	}
	return false
}

func (f *TextFlow) objectClickAt(x, y float64) bool {
	for _, ln := range f.Layout().Lines {
		for _, o := range ln.Objects {
			if x < o.X || x >= o.X+o.W || y < o.Y || y >= o.Y+o.H {
				continue
			}
			co, ok := o.Object.(ClickObject)
			if !ok {
				continue
			}
			used, changed := co.ClickAt(x-o.X, y-o.Y)
			if changed {
				f.changed()
			}
			if t, ok := o.Object.(*TableObject); ok && used && f.OnTableSelect != nil && t.Selected().Kind != SelNone {
				f.OnTableSelect(t, t.Selected())
			}
			if used {
				return true
			}
		}
	}
	return false
}
