package components

import (
	"strings"
	"testing"

	"github.com/elaous/xango"
)

func TestCheckbox(t *testing.T) {
	t.Run("unchecked, enabled", func(t *testing.T) {
		html := render(t, Checkbox(CheckboxProps{Label: "Agree"}))
		mustContain(t, html, `type="checkbox"`, "Agree")
		mustNotContain(t, html, "checked=", "disabled=")
	})

	t.Run("Checked and Disabled both render their attribute", func(t *testing.T) {
		html := render(t, Checkbox(CheckboxProps{Checked: true, Disabled: true}))
		mustContain(t, html, `checked="checked"`, `disabled="disabled"`)
	})

	t.Run("OnChange fires with the new checked state", func(t *testing.T) {
		var got bool
		n := Checkbox(CheckboxProps{OnChange: func(checked bool) { got = checked }})
		handlers := findHandlers(n, "change")
		if len(handlers) != 1 {
			t.Fatalf("expected 1 change handler, got %d", len(handlers))
		}
		handlers[0](xango.NewEvent("change", nil, true, nil, nil))
		if !got {
			t.Error("OnChange did not receive Checked=true from the event")
		}
	})
}

func TestCheckboxGroup(t *testing.T) {
	opts := []CheckboxOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}

	t.Run("Selected marks the matching options checked", func(t *testing.T) {
		html := render(t, CheckboxGroup(CheckboxGroupProps{Options: opts, Selected: []string{"b"}}))
		if count := strings.Count(html, `checked="checked"`); count != 1 {
			t.Errorf("expected exactly 1 checked option, found %d in: %s", count, html)
		}
	})

	t.Run("Disabled marks every option disabled", func(t *testing.T) {
		html := render(t, CheckboxGroup(CheckboxGroupProps{Options: opts, Disabled: true}))
		if count := strings.Count(html, `disabled="disabled"`); count != len(opts) {
			t.Errorf("expected %d disabled options, found %d in: %s", len(opts), count, html)
		}
	})

	t.Run("OnChange reports the toggled Value and new state", func(t *testing.T) {
		var gotValue string
		var gotChecked bool
		n := CheckboxGroup(CheckboxGroupProps{
			Options: opts,
			OnChange: func(value string, checked bool) {
				gotValue, gotChecked = value, checked
			},
		})
		handlers := findHandlers(n, "change")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 change handlers (one per option), got %d", len(handlers))
		}
		handlers[1](xango.NewEvent("change", nil, true, nil, nil))
		if gotValue != "b" || !gotChecked {
			t.Errorf("second handler reported (%q, %v), want (\"b\", true)", gotValue, gotChecked)
		}
	})
}

func TestRadioGroup(t *testing.T) {
	opts := []RadioOption{{Value: "x", Label: "X"}, {Value: "y", Label: "Y"}}

	t.Run("shares one name and marks Selected checked", func(t *testing.T) {
		html := render(t, RadioGroup(RadioGroupProps{Name: "choice", Options: opts, Selected: "y"}))
		mustContain(t, html, `name="choice"`, `id="choice-y"`, `for="choice-y"`)
	})

	t.Run("Disabled marks every option disabled", func(t *testing.T) {
		html := render(t, RadioGroup(RadioGroupProps{Name: "choice", Options: opts, Disabled: true}))
		if count := strings.Count(html, `disabled="disabled"`); count != len(opts) {
			t.Errorf("expected %d disabled options, found %d in: %s", len(opts), count, html)
		}
	})

	t.Run("OnChange reports the clicked option's Value", func(t *testing.T) {
		var got string
		n := RadioGroup(RadioGroupProps{Name: "choice", Options: opts, OnChange: func(v string) { got = v }})
		handlers := findHandlers(n, "change")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 change handlers, got %d", len(handlers))
		}
		handlers[0](xango.Event{})
		if got != "x" {
			t.Errorf("first handler reported %q, want \"x\"", got)
		}
	})
}

func TestSwitch(t *testing.T) {
	t.Run("unchecked renders aria-checked=false", func(t *testing.T) {
		html := render(t, Switch(SwitchProps{Label: "Enabled"}))
		mustContain(t, html, `role="switch"`, `aria-checked="false"`, "Enabled")
	})

	t.Run("Checked sets both the checked attribute and aria-checked", func(t *testing.T) {
		html := render(t, Switch(SwitchProps{Checked: true}))
		mustContain(t, html, `checked="checked"`, `aria-checked="true"`)
	})

	t.Run("no Label omits the label span", func(t *testing.T) {
		html := render(t, Switch(SwitchProps{}))
		mustNotContain(t, html, "gj-switch-label")
	})

	t.Run("Disabled renders the attribute", func(t *testing.T) {
		html := render(t, Switch(SwitchProps{Disabled: true}))
		mustContain(t, html, `disabled="disabled"`)
	})

	t.Run("OnChange fires with the checked state", func(t *testing.T) {
		var got bool
		n := Switch(SwitchProps{OnChange: func(checked bool) { got = checked }})
		handlers := findHandlers(n, "change")
		handlers[0](xango.NewEvent("change", nil, true, nil, nil))
		if !got {
			t.Error("OnChange did not receive true")
		}
	})
}

func TestSlider(t *testing.T) {
	t.Run("renders min/max/value, no step by default", func(t *testing.T) {
		html := render(t, Slider(SliderProps{Min: 0, Max: 10, Value: 5}))
		mustContain(t, html, `type="range"`, `min="0"`, `max="10"`, `value="5"`)
		mustNotContain(t, html, "step=")
	})

	t.Run("a non-zero Step is rendered", func(t *testing.T) {
		html := render(t, Slider(SliderProps{Step: 0.5}))
		mustContain(t, html, `step="0.5"`)
	})

	t.Run("Disabled renders the attribute", func(t *testing.T) {
		html := render(t, Slider(SliderProps{Disabled: true}))
		mustContain(t, html, `disabled="disabled"`)
	})

	t.Run("OnChange parses the string event value to float64", func(t *testing.T) {
		var got float64
		n := Slider(SliderProps{OnChange: func(v float64) { got = v }})
		handlers := findHandlers(n, "input")
		handlers[0](xango.NewEvent("input", "7.5", false, nil, nil))
		if got != 7.5 {
			t.Errorf("OnChange got %v, want 7.5", got)
		}
	})
}

func TestDatePicker(t *testing.T) {
	t.Run("Min/Max are optional", func(t *testing.T) {
		html := render(t, DatePicker(DatePickerProps{Value: "2024-01-01"}))
		mustContain(t, html, `type="date"`, `value="2024-01-01"`)
		mustNotContain(t, html, "min=", "max=")
	})

	t.Run("Min/Max render when set", func(t *testing.T) {
		html := render(t, DatePicker(DatePickerProps{Min: "2024-01-01", Max: "2024-12-31"}))
		mustContain(t, html, `min="2024-01-01"`, `max="2024-12-31"`)
	})

	t.Run("Disabled renders the attribute", func(t *testing.T) {
		html := render(t, DatePicker(DatePickerProps{Disabled: true}))
		mustContain(t, html, `disabled="disabled"`)
	})

	t.Run("OnChange receives the new value", func(t *testing.T) {
		var got string
		n := DatePicker(DatePickerProps{OnChange: func(v string) { got = v }})
		handlers := findHandlers(n, "change")
		handlers[0](xango.NewEvent("change", "2024-06-01", false, nil, nil))
		if got != "2024-06-01" {
			t.Errorf("OnChange got %q, want %q", got, "2024-06-01")
		}
	})
}

func TestColorPicker(t *testing.T) {
	html := render(t, ColorPicker(ColorPickerProps{Value: "#ff0000"}))
	mustContain(t, html, `type="color"`, `value="#ff0000"`)

	var got string
	n := ColorPicker(ColorPickerProps{OnChange: func(v string) { got = v }})
	findHandlers(n, "change")[0](xango.NewEvent("change", "#00ff00", false, nil, nil))
	if got != "#00ff00" {
		t.Errorf("OnChange got %q, want %q", got, "#00ff00")
	}

	t.Run("Disabled renders the attribute", func(t *testing.T) {
		html := render(t, ColorPicker(ColorPickerProps{Disabled: true}))
		mustContain(t, html, `disabled="disabled"`)
	})
}

func TestTextarea(t *testing.T) {
	t.Run("Rows only renders when positive", func(t *testing.T) {
		html := render(t, Textarea(TextareaProps{Value: "hi"}))
		mustContain(t, html, `value="hi"`)
		mustNotContain(t, html, "rows=")

		withRows := render(t, Textarea(TextareaProps{Rows: 4}))
		mustContain(t, withRows, `rows="4"`)
	})

	t.Run("Disabled and Placeholder", func(t *testing.T) {
		html := render(t, Textarea(TextareaProps{Disabled: true, Placeholder: "Type here"}))
		mustContain(t, html, `disabled="disabled"`, `placeholder="Type here"`)
	})

	t.Run("OnChange receives the new value", func(t *testing.T) {
		var got string
		n := Textarea(TextareaProps{OnChange: func(v string) { got = v }})
		findHandlers(n, "input")[0](xango.NewEvent("input", "new text", false, nil, nil))
		if got != "new text" {
			t.Errorf("OnChange got %q, want %q", got, "new text")
		}
	})
}

func TestOTPInput(t *testing.T) {
	t.Run("renders Length boxes with per-box ids and values", func(t *testing.T) {
		html := render(t, OTPInput(OTPInputProps{ID: "otp", Length: 4, Values: []string{"1", "2"}}))
		mustContain(t, html, `id="otp-0"`, `id="otp-3"`, `value="1"`, `aria-label="Digit 1"`)
	})

	t.Run("OnChange reports the box index and new value", func(t *testing.T) {
		var gotIndex int
		var gotValue string
		n := OTPInput(OTPInputProps{Length: 3, OnChange: func(i int, v string) { gotIndex, gotValue = i, v }})
		handlers := findHandlers(n, "input")
		if len(handlers) != 3 {
			t.Fatalf("expected 3 input handlers, got %d", len(handlers))
		}
		handlers[2](xango.NewEvent("input", "9", false, nil, nil))
		if gotIndex != 2 || gotValue != "9" {
			t.Errorf("got (%d, %q), want (2, \"9\")", gotIndex, gotValue)
		}
	})

	t.Run("Disabled marks every box disabled", func(t *testing.T) {
		html := render(t, OTPInput(OTPInputProps{Length: 3, Disabled: true}))
		if count := strings.Count(html, `disabled="disabled"`); count != 3 {
			t.Errorf("expected 3 disabled boxes, found %d in: %s", count, html)
		}
	})
}

func TestSearch(t *testing.T) {
	t.Run("suggestions only render when Open and non-empty", func(t *testing.T) {
		closedHTML := render(t, Search(SearchProps{Suggestions: []string{"a", "b"}}))
		mustNotContain(t, closedHTML, "gj-search-list")

		openHTML := render(t, Search(SearchProps{Open: true, Suggestions: []string{"apple", "banana"}}))
		mustContain(t, openHTML, "gj-search-list", "apple", "banana")
	})

	t.Run("Placeholder and Disabled both render", func(t *testing.T) {
		html := render(t, Search(SearchProps{Placeholder: "Search...", Disabled: true}))
		mustContain(t, html, `placeholder="Search..."`, `disabled="disabled"`)
	})

	t.Run("OnSelect fires with the clicked suggestion", func(t *testing.T) {
		var got string
		n := Search(SearchProps{Open: true, Suggestions: []string{"apple", "banana"}, OnSelect: func(s string) { got = s }})
		handlers := findHandlers(n, "click")
		if len(handlers) != 2 {
			t.Fatalf("expected 2 click handlers, got %d", len(handlers))
		}
		handlers[1](xango.Event{})
		if got != "banana" {
			t.Errorf("OnSelect got %q, want %q", got, "banana")
		}
	})

	t.Run("OnChange fires on input", func(t *testing.T) {
		var got string
		n := Search(SearchProps{OnChange: func(v string) { got = v }})
		findHandlers(n, "input")[0](xango.NewEvent("input", "query", false, nil, nil))
		if got != "query" {
			t.Errorf("OnChange got %q, want %q", got, "query")
		}
	})
}

func TestDropdown(t *testing.T) {
	opts := []DropdownOption{{Value: "a", Label: "Alpha"}, {Value: "b", Label: "Beta"}}

	t.Run("closed shows only the control", func(t *testing.T) {
		html := render(t, Dropdown(DropdownProps{Options: opts, Placeholder: "Pick one"}))
		mustContain(t, html, "Pick one", `aria-expanded="false"`)
		mustNotContain(t, html, "gj-dropdown-list")
	})

	t.Run("Selected joins the matching labels", func(t *testing.T) {
		html := render(t, Dropdown(DropdownProps{Options: opts, Selected: []string{"a", "b"}}))
		mustContain(t, html, "Alpha, Beta")
	})

	t.Run("open, multi-select renders a listbox with checkboxes", func(t *testing.T) {
		html := render(t, Dropdown(DropdownProps{Options: opts, Selected: []string{"a"}, Open: true, Multiple: true}))
		mustContain(t, html, "gj-dropdown-list", `role="listbox"`, `aria-multiselectable="true"`, "gj-dropdown-checkbox", `aria-selected="true"`)
	})

	t.Run("an option Icon renders before its label", func(t *testing.T) {
		icon := Icon(IconProps{Name: IconStar})
		html := render(t, Dropdown(DropdownProps{
			Options: []DropdownOption{{Value: "a", Label: "Alpha", Icon: &icon}},
			Open:    true,
		}))
		mustContain(t, html, "gj-dropdown-icon")
	})

	t.Run("OnToggleOpen and OnSelect both fire", func(t *testing.T) {
		toggled := true // sentinel that must change
		var toggledTo bool
		var selected string
		n := Dropdown(DropdownProps{
			Options: opts,
			Open:    true, // already open, so clicking the control should close it
			OnToggleOpen: func(open bool) {
				toggled = true
				toggledTo = open
			},
			OnSelect: func(v string) { selected = v },
		})
		clickHandlers := findHandlers(n, "click")
		if len(clickHandlers) != 1+len(opts) {
			t.Fatalf("expected %d click handlers, got %d", 1+len(opts), len(clickHandlers))
		}
		clickHandlers[0](xango.Event{})
		if !toggled || toggledTo != false {
			t.Errorf("clicking the control while Open=true should invoke OnToggleOpen(false), got called=%v with %v", toggled, toggledTo)
		}
		clickHandlers[2](xango.Event{})
		if selected != "b" {
			t.Errorf("OnSelect got %q, want \"b\"", selected)
		}
	})
}
