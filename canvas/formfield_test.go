package canvas

import "testing"

func TestFormFieldProblems(t *testing.T) {
	for _, c := range []struct {
		kind    FormKind
		pattern string
		req     bool
		opts    []string
		value   string
		want    string
	}{
		{FormText, "", false, nil, "", ""},
		{FormText, "", true, nil, "  ", FormProblemRequired},
		{FormEmail, "", false, nil, "ana@example.com", ""},
		{FormEmail, "", false, nil, "ana@", FormProblemEmail},
		{FormNumber, "", false, nil, "-3.5", ""},
		{FormNumber, "", false, nil, "3,5x", FormProblemNumber},
		{FormPhone, "", false, nil, "+49 (30) 1234-5678", ""},
		{FormPhone, "", false, nil, "call me", FormProblemPhone},
		{FormURL, "", false, nil, "https://example.com", ""},
		{FormURL, "", false, nil, "javascript:alert(1)", FormProblemURL},
		{FormDate, "", false, nil, "2026-02-29", FormProblemDate},
		{FormDate, "", false, nil, "2026-10-17", ""},
		{FormSelect, "", false, []string{"a", "b"}, "c", FormProblemChoice},
		{FormSelect, "", false, []string{"a", "b"}, "b", ""},
		{FormText, `[A-Z]{2}\d{3}`, false, nil, "AB123", ""},
		{FormText, `[A-Z]{2}\d{3}`, false, nil, "AB1234", FormProblemPattern}, // the whole value has to match
		{FormText, `(`, false, nil, "anything", ""},
		{FormCheckbox, "", true, nil, "", FormProblemRequired},
		{FormCheckbox, "", true, nil, "true", ""},
	} {
		f := &FormFieldObject{Kind: c.kind, Pattern: c.pattern, Required: c.req, Options: c.opts, Value: c.value}
		if got := f.Problem(); got != c.want {
			t.Errorf("%s %q pattern %q: problem %q, want %q", c.kind, c.value, c.pattern, got, c.want)
		}
	}
	if (&FormFieldObject{Pattern: "("}).PatternError() == "" {
		t.Error("a broken pattern is reported")
	}
}

func fieldFlow(ff *FormFieldObject) *TextFlow {
	f, _ := newFlow("", 600, 300)
	f.Items = []Item{Chars{Text: "Name "}, Inline{Object: ff}}
	return f
}

func TestFormFieldEyePenAndCheckboxPresses(t *testing.T) {
	var filled, edited int
	ff := NewFormField(FormText, "Name")
	ff.OnFill, ff.OnEdit = func(*FormFieldObject) { filled++ }, func(*FormFieldObject) { edited++ }
	f := fieldFlow(ff)
	f.Draw()
	o := f.Layout().Lines[0].Objects[0]
	press := func(r [4]float64) bool {
		return f.mouseDown(o.X+r[0]+r[2]/2, o.Y+r[1]+r[3]/2)
	}
	if !press(ff.eye) || filled != 1 || edited != 0 {
		t.Fatalf("the eye asks to fill: %d %d", filled, edited)
	}
	if !press(ff.pen) || edited != 1 {
		t.Fatalf("the pen asks to edit: %d", edited)
	}
	ff.Locked = true
	f.Draw()
	if ff.pen[2] != 0 {
		t.Fatal("a locked field has no pen")
	}
	if _, ok := ff.ClickAt(ff.eye[0]+ff.eye[2]+6, 8); ok && edited != 1 {
		t.Fatal("a locked field cannot be edited")
	}

	cb := NewFormField(FormCheckbox, "I agree")
	g := fieldFlow(cb)
	changes := 0
	g.OnChange = func(*TextFlow) { changes++ }
	g.Draw()
	co := g.Layout().Lines[0].Objects[0]
	g.mouseDown(co.X+cb.cb[0]+3, co.Y+cb.cb[1]+3)
	if cb.Value != "true" || changes != 1 {
		t.Fatalf("pressing the box ticks it: %q %d", cb.Value, changes)
	}
	g.mouseDown(co.X+cb.cb[0]+3, co.Y+cb.cb[1]+3)
	if cb.Value != "" {
		t.Fatalf("and unticks it: %q", cb.Value)
	}
}

func TestClearFormFieldsAndWords(t *testing.T) {
	a, b := NewFormField(FormText, "A"), NewFormField(FormCheckbox, "B")
	a.Value, b.Value = "x", "true"
	f, _ := newFlow("", 600, 300)
	f.Items = []Item{Inline{Object: a}, Chars{Text: " "}, Inline{Object: b}}
	if len(f.FormFields()) != 2 || a.Words() != "A: x" || b.Words() != "[x] B" {
		t.Fatalf("fields %d, words %q %q", len(f.FormFields()), a.Words(), b.Words())
	}
	if n := f.ClearFormFields(); n != 2 || a.Value != "" || b.Value != "" {
		t.Fatalf("cleared %d: %q %q", n, a.Value, b.Value)
	}
	if n := f.ClearFormFields(); n != 0 {
		t.Fatalf("nothing left to clear: %d", n)
	}
}
