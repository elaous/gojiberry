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

// FormKinds lists the kinds in the order a bar of them shows.
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

// What a form field's Problem reports; a host turns them into words.
const (
	FormProblemRequired = "required" // a required field is empty (or a required checkbox is not ticked)
	FormProblemEmail    = "email"
	FormProblemNumber   = "number"
	FormProblemPhone    = "phone"
	FormProblemURL      = "url"
	FormProblemDate     = "date"
	FormProblemChoice   = "choice"  // the value is not one of Options
	FormProblemPattern  = "pattern" // the value does not match Pattern
)

// FormFieldObject is a field of a form in the text: it keeps what the person writes in it (Value) and shows, next to its
// label, a small eye (fill the field in) and, unless the field is locked, a small pen (change what the field is: its
// label, kind, placeholder, pattern, options). A locked field cannot have its properties changed, only its value.
//
// The canvas draws the field and tells the host which icon was pressed (OnFill, OnEdit); the host asks for the value and
// the properties, as a panel or a dialog, because the canvas has no text boxes of its own. A checkbox is ticked by
// pressing it.
type FormFieldObject struct {
	Kind FormKind
	// Name identifies the field in the data a form gives back; Label is what the person sees.
	Name, Label string
	// Placeholder is shown, dimmed, while the field is empty.
	Placeholder string
	// Pattern is a regular expression (Go syntax) the whole value has to match; "" for none.
	Pattern  string
	Required bool
	// Options are the choices of a FormSelect.
	Options []string
	// Value is what the person wrote: the text, the number as text, the date as 2006-01-02, "true" for a ticked box.
	Value string
	// Locked keeps the properties as they are: no pen, and the host refuses to change them.
	Locked bool

	// OnFill and OnEdit are called when the eye or the pen is pressed.
	OnFill func(*FormFieldObject)
	OnEdit func(*FormFieldObject)

	w, h         float64
	eye, pen, cb [4]float64 // the icons and the box of a checkbox, relative to the field's top-left (zero width: none)
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

// PatternError is why Pattern is not a regular expression, "" when it is (or is empty).
func (f *FormFieldObject) PatternError() string {
	if f.Pattern == "" {
		return ""
	}
	if _, err := regexp.Compile(f.Pattern); err != nil {
		return err.Error()
	}
	return ""
}

// Problem says what is wrong with the value: one of the FormProblem codes, or "" when it is fine. An empty value is fine
// unless the field is Required.
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
		if low := strings.ToLower(v); !(strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")) || !SafeLinkURL(v) || len(v) < 9 {
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

// Words is what the exports write for the field: its label and what is in it.
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

// Measure implements InlineObject.
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

// DrawAt implements InlineObject.
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
	// the icons, at the right of the top line
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

// icons draws the eye, the pen (when there is one) and the lock.
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

// clipText cuts s with an ellipsis so that it is at most w wide.
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

// ClickObject is an InlineObject that takes presses: (x, y) are relative to its top-left corner. It reports whether it
// used the press, and whether that changed the content (so that the document is saved).
type ClickObject interface {
	ClickAt(x, y float64) (used, changed bool)
}

// ClickAt implements ClickObject: the eye asks the host to fill the field in, the pen to change it, and the box of a
// checkbox ticks or unticks it.
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

var _ ClickObject = (*FormFieldObject)(nil)

// FormFields lists the form fields in the flow's content, in reading order.
func (f *TextFlow) FormFields() []*FormFieldObject {
	var out []*FormFieldObject
	for _, it := range f.Items {
		if in, ok := it.(Inline); ok {
			if ff, ok := in.Object.(*FormFieldObject); ok {
				out = append(out, ff)
			}
		}
	}
	return out
}

// ClearFormFields empties every form field and reports how many had something in them.
func (f *TextFlow) ClearFormFields() int {
	n := 0
	for _, ff := range f.FormFields() {
		if !ff.Empty() {
			ff.Clear()
			n++
		}
	}
	if n > 0 {
		f.changed()
	}
	return n
}

// objectClickAt gives a press to the inline object under (x, y) that takes presses.
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
			if used {
				return true
			}
		}
	}
	return false
}
