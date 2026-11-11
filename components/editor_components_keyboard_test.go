package components

import (
	"github.com/elaous/gojiberry/xango"
	"strings"
	"testing"
)

func TestMenuKeyboardEscapeEnterAndSpace(t *testing.T) {
	var opened []int
	var picked []string
	entries := []MenuBarEntry{{Label: "File", Items: []MenuItem{{Label: "New", OnSelect: func() { picked = append(picked, "new") }}}}}
	bar := MenuBar(MenuBarProps{Entries: entries, Open: 0, OnOpen: func(i int) { opened = append(opened, i) }})
	keys := findHandlers(bar, "keydown") // the bar (Escape), the File button and the entry New
	if len(keys) != 3 {
		t.Fatalf("keydown handlers = %d, want 3", len(keys))
	}
	keys = []xango.EventHandler{keys[0], keys[2]}
	press := func(h func(xango.Event), k xango.KeyData) {
		prevented := false
		h(xango.NewEvent("keydown", k, false, func() { prevented = true }, nil))
		_ = prevented
	}
	press(keys[0], xango.KeyData{Key: "a"})
	if len(opened) != 0 {
		t.Errorf("another key closed the menu: %v", opened)
	}
	press(keys[0], xango.KeyData{Key: "Escape"})
	if len(opened) != 1 || opened[0] != -1 {
		t.Errorf("Escape: %v", opened)
	}
	press(keys[1], xango.KeyData{Key: "Enter"})
	press(keys[1], xango.KeyData{Key: " "})
	press(keys[1], xango.KeyData{Key: "x"})
	press(keys[1], xango.KeyData{Key: "Enter", Ctrl: true}) // a chord is not a choice
	if len(picked) != 2 {
		t.Errorf("Enter and Space choose, nothing else does: %v", picked)
	}
	if len(opened) != 3 { // Escape, then each choice closes
		t.Errorf("closing: %v", opened)
	}
	closedBar := MenuBar(MenuBarProps{Entries: entries, Open: -1, OnOpen: func(i int) { opened = append(opened, i) }})
	before := len(opened)
	press(findHandlers(closedBar, "keydown")[0], xango.KeyData{Key: "Escape"})
	if len(opened) != before {
		t.Errorf("Escape with no menu open acted: %v", opened)
	}
}

type focusCall struct {
	root, items string
	step        xango.FocusStep
}

func keyEvent(key string, calls *[]focusCall, prevented *bool) xango.Event {
	return xango.NewEvent("keydown", xango.KeyData{Key: key}, false, func() { *prevented = true }, nil).
		WithFocus(func(root, items string, step xango.FocusStep) bool {
			*calls = append(*calls, focusCall{root, items, step})
			return true
		})
}

func TestMenuArrowKeysMoveTheFocus(t *testing.T) {
	var opened []int
	entries := []MenuBarEntry{
		{Label: "File", Items: []MenuItem{{Label: "New", OnSelect: func() {}}, {Label: "More", Items: []MenuItem{{Label: "Deep", OnSelect: func() {}}}}}},
		{Label: "Edit", Items: []MenuItem{{Label: "Undo", OnSelect: func() {}}}},
		{Label: "View", Items: []MenuItem{{Label: "Zoom", OnSelect: func() {}}}},
	}
	bar := MenuBar(MenuBarProps{Entries: entries, Open: 0, OnOpen: func(i int) { opened = append(opened, i) }})
	keys := findHandlers(bar, "keydown") // in tree order: nav, File, New, More, Deep, Edit, View
	if len(keys) != 7 {
		t.Fatalf("keydown handlers = %d, want 7", len(keys))
	}
	press := func(h func(xango.Event), key string) ([]focusCall, bool) {
		var calls []focusCall
		prevented := false
		h(keyEvent(key, &calls, &prevented))
		return calls, prevented
	}
	for key, want := range map[string]xango.FocusStep{"ArrowDown": xango.FocusNext, "ArrowUp": xango.FocusPrev, "Home": xango.FocusFirst, "End": xango.FocusLast} {
		calls, prevented := press(keys[2], key)
		if len(calls) != 1 || calls[0].step != want || calls[0].root != ".gj-menu" || !strings.Contains(calls[0].items, "li.gj-menu-item") || !prevented {
			t.Errorf("%s in a menu: %+v prevented %v", key, calls, prevented)
		}
	}
	if calls, _ := press(keys[3], "ArrowRight"); len(calls) != 1 || calls[0].root != ".gj-menu-item-sub" || calls[0].step != xango.FocusFirst || !strings.Contains(calls[0].items, "> .gj-menu >") {
		t.Errorf("Right into the submenu: %+v", calls)
	}
	if calls, _ := press(keys[4], "ArrowLeft"); len(calls) != 1 || calls[0].root != ".gj-menu-item-sub" || calls[0].items != ":scope" {
		t.Errorf("Left out of the submenu: %+v", calls)
	}
	if calls, _ := press(keys[1], "ArrowRight"); len(calls) != 1 || calls[0].step != xango.FocusNext || len(opened) != 1 || opened[0] != 1 {
		t.Errorf("Right from File with its menu open: %+v %v", calls, opened)
	}
	opened = nil
	if calls, _ := press(keys[1], "ArrowLeft"); len(calls) != 1 || calls[0].step != xango.FocusPrev || len(opened) != 1 || opened[0] != 2 {
		t.Errorf("Left from File wraps to View: %+v %v", calls, opened)
	}
	opened = nil
	if calls, _ := press(keys[1], "ArrowDown"); len(calls) != 1 || calls[0].step != xango.FocusFirst || len(opened) != 0 {
		t.Errorf("Down on the open button: %+v %v", calls, opened)
	}
	if calls, _ := press(keys[1], "ArrowUp"); len(calls) != 1 || calls[0].step != xango.FocusLast {
		t.Errorf("Up on the open button: %+v", calls)
	}
	if calls, _ := press(keys[5], "ArrowDown"); len(opened) != 1 || opened[0] != 1 || len(calls) != 1 {
		t.Errorf("Down on a closed button: %+v %v", calls, opened)
	}
	opened = nil
	if calls, _ := press(keys[0], "Escape"); len(calls) != 1 || !strings.Contains(calls[0].items, "gj-menubar-entry-open") || len(opened) != 1 || opened[0] != -1 {
		t.Errorf("Escape: %+v %v", calls, opened)
	}
	var calls []focusCall
	prevented := false
	keys[2](xango.NewEvent("keydown", xango.KeyData{Key: "ArrowDown", Ctrl: true}, false, func() { prevented = true }, nil).
		WithFocus(func(root, items string, step xango.FocusStep) bool {
			calls = append(calls, focusCall{root, items, step})
			return true
		}))
	if len(calls) != 0 || prevented {
		t.Errorf("Ctrl+Down acted: %+v", calls)
	}
	keys[2](xango.NewEvent("keydown", xango.KeyData{Key: "ArrowDown"}, false, nil, nil)) // no focus hook: must not panic
}

func TestTabsKeyboard(t *testing.T) {
	var picked []int
	tabs := Tabs(TabsProps{Tabs: []Tab{{Label: "A"}, {Label: "B"}, {Label: "C"}}, Active: 1, OnSelect: func(i int) { picked = append(picked, i) }})
	html := render(t, tabs)
	if strings.Count(html, `tabindex="0"`) != 1 || strings.Count(html, `tabindex="-1"`) != 2 {
		t.Errorf("roving tabindex: %s", html)
	}
	keys := findHandlers(tabs, "keydown")
	if len(keys) != 3 {
		t.Fatalf("keydown handlers = %d, want 3", len(keys))
	}
	press := func(h xango.EventHandler, k xango.KeyData) (steps []xango.FocusStep, prevented bool) {
		h(xango.NewEvent("keydown", k, false, func() { prevented = true }, nil).
			WithFocus(func(root, items string, step xango.FocusStep) bool {
				if root != `[role="tablist"]` || items != `[role="tab"]` {
					t.Errorf("focus asked in %q for %q", root, items)
				}
				steps = append(steps, step)
				return true
			}))
		return
	}
	for key, want := range map[string]struct {
		step xango.FocusStep
		pick int
	}{
		"ArrowRight": {xango.FocusNext, 2}, "ArrowLeft": {xango.FocusPrev, 0}, "Home": {xango.FocusFirst, 0}, "End": {xango.FocusLast, 2},
	} {
		picked = nil
		steps, prevented := press(keys[1], xango.KeyData{Key: key})
		if len(steps) != 1 || steps[0] != want.step || len(picked) != 1 || picked[0] != want.pick || !prevented {
			t.Errorf("%s on B: steps %v picked %v prevented %v", key, steps, picked, prevented)
		}
	}
	picked = nil
	if steps, _ := press(keys[2], xango.KeyData{Key: "ArrowRight"}); len(steps) != 1 || len(picked) != 1 || picked[0] != 0 {
		t.Errorf("Right from the last wraps to the first: %v", picked)
	}
	if steps, _ := press(keys[0], xango.KeyData{Key: "ArrowLeft"}); len(steps) != 1 || picked[len(picked)-1] != 2 {
		t.Errorf("Left from the first wraps to the last: %v", picked)
	}
	picked = nil
	if steps, prevented := press(keys[1], xango.KeyData{Key: "ArrowRight", Ctrl: true}); len(steps) != 0 || prevented || len(picked) != 0 {
		t.Errorf("a chord acted: %v %v", steps, picked)
	}
	if steps, _ := press(keys[1], xango.KeyData{Key: "a"}); len(steps) != 0 {
		t.Errorf("another key acted")
	}
}

func TestModalKeyboard(t *testing.T) {
	closed := 0
	m := Modal(ModalProps{Open: true, Label: "Sure?", Content: []xango.Node{xango.Text("x")}, OnClose: func() { closed++ }})
	html := render(t, m)
	if !strings.Contains(html, `tabindex="-1"`) || !strings.Contains(html, `data-gj-modal="true"`) || !strings.Contains(html, `aria-modal="true"`) {
		t.Errorf("the box takes the focus and gives it back: %s", html)
	}
	keys := findHandlers(m, "keydown")
	if len(keys) != 1 {
		t.Fatalf("keydown handlers = %d, want 1", len(keys))
	}
	press := func(k xango.KeyData) (steps []xango.FocusStep, prevented bool) {
		keys[0](xango.NewEvent("keydown", k, false, func() { prevented = true }, nil).
			WithFocus(func(root, items string, step xango.FocusStep) bool {
				if root != ".gj-modal" || !strings.Contains(items, "button:not([disabled])") {
					t.Errorf("focus asked in %q for %q", root, items)
				}
				steps = append(steps, step)
				return true
			}))
		return
	}
	if steps, prevented := press(xango.KeyData{Key: "Tab"}); len(steps) != 1 || steps[0] != xango.FocusNext || !prevented {
		t.Errorf("Tab: %v %v", steps, prevented)
	}
	if steps, prevented := press(xango.KeyData{Key: "Tab", Shift: true}); len(steps) != 1 || steps[0] != xango.FocusPrev || !prevented {
		t.Errorf("Shift+Tab: %v %v", steps, prevented)
	}
	if _, prevented := press(xango.KeyData{Key: "Escape"}); closed != 1 || !prevented {
		t.Errorf("Escape: closed %d prevented %v", closed, prevented)
	}
	if steps, prevented := press(xango.KeyData{Key: "Tab", Ctrl: true}); len(steps) != 0 || prevented {
		t.Errorf("Ctrl+Tab belongs to the browser: %v %v", steps, prevented)
	}
	press(xango.KeyData{Key: "a"})
	none := findHandlers(Modal(ModalProps{Open: true, Content: []xango.Node{xango.Text("x")}}), "keydown")
	prevented := false
	none[0](xango.NewEvent("keydown", xango.KeyData{Key: "Escape"}, false, func() { prevented = true }, nil))
	if prevented {
		t.Error("Escape was taken from a modal that cannot close")
	}
}

func TestCheckPanelListsFindingsWithWordsAndAGoButton(t *testing.T) {
	var went []int
	checked := false
	props := CheckPanelProps{
		Checked: true,
		Findings: []CheckFinding{
			{Rule: "image-alt", Severity: "error", Message: "An image has no description.", Fix: "Describe it."},
			{Rule: "contrast", Severity: "warning", Message: "Faint text.", Fix: "Darken it."},
		},
		OnCheck: func() { checked = true },
		OnGo:    func(i int) { went = append(went, i) },
	}
	html := render(t, CheckPanel(props))
	for _, want := range []string{`id="gj-check-run"`, "2 findings: 1 errors, 1 warnings.", `role="status"`, "error: ", "warning: ", "An image has no description.", "Describe it.", `aria-label="Go to finding 2: Faint text."`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	gos := findHandlers(CheckPanel(props), "click")
	if len(gos) != 3 { // Check, then one per finding
		t.Fatalf("click handlers: %d", len(gos))
	}
	gos[0](xango.NewEvent("click", nil, false, nil, nil))
	gos[2](xango.NewEvent("click", nil, false, nil, nil))
	if !checked || len(went) != 1 || went[0] != 1 {
		t.Errorf("checked %v, went %v", checked, went)
	}
	if h := render(t, CheckPanel(CheckPanelProps{})); !strings.Contains(h, "Not checked yet.") || strings.Contains(h, "<ul") {
		t.Errorf("before a check: %s", h)
	}
	if h := render(t, CheckPanel(CheckPanelProps{Checked: true})); !strings.Contains(h, "Nothing found.") {
		t.Errorf("a clean check: %s", h)
	}
}

func TestCheckPanelFixButtonsAndTheExportGate(t *testing.T) {
	var fixed []int
	var gate []string
	overrides := 0
	props := CheckPanelProps{
		Checked:  true,
		Findings: []CheckFinding{{Rule: "heading-skip", Severity: "warning", Message: "m", Fix: "f", FixLabel: "Make it level 2"}, {Rule: "contrast", Severity: "warning", Message: "n", Fix: "g"}},
		OnFix:    func(i int) { fixed = append(fixed, i) },
		Gate:     "block", OnGate: func(m string) { gate = append(gate, m) },
		OnOverride: func() { overrides++ },
	}
	html := render(t, CheckPanel(props))
	if strings.Count(html, "gj-check-fix-button") != 1 || !strings.Contains(html, "Make it level 2") {
		t.Errorf("only a finding with a fix has a button:\n%s", html)
	}
	for _, want := range []string{`data-gate="block" aria-pressed="true"`, `data-gate="warn" aria-pressed="false"`, `aria-label="Before exporting"`, `id="gj-check-override"`, "Allow the next export anyway"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	clicks := findHandlers(CheckPanel(props), "click")
	if len(clicks) != 6 {
		t.Fatalf("click handlers: %d", len(clicks))
	}
	ev := xango.NewEvent("click", nil, false, nil, nil)
	clicks[2](ev) // the warn button
	clicks[4](ev) // override
	clicks[5](ev) // the fix of finding 0
	if len(gate) != 1 || gate[0] != "warn" || overrides != 1 || len(fixed) != 1 || fixed[0] != 0 {
		t.Errorf("gate %v overrides %d fixed %v", gate, overrides, fixed)
	}
	if h := render(t, CheckPanel(CheckPanelProps{Checked: true})); strings.Contains(h, "gj-check-gate") {
		t.Error("the gate is shown without a handler")
	}
}

func TestCheckPanelGateFloorCannotBeLowered(t *testing.T) {
	var gate []string
	overrides := 0
	props := func(gate_, floor string) CheckPanelProps {
		return CheckPanelProps{Checked: true, Gate: gate_, GateFloor: floor, OnOverride: func() { overrides++ },
			OnGate: func(m string) { gate = append(gate, m) }}
	}
	html := render(t, CheckPanel(props("block", "block")))
	for _, want := range []string{`data-gate="" aria-pressed="false" disabled="disabled"`, `data-gate="warn" aria-pressed="false" disabled="disabled"`, `id="gj-check-floor"`, "cannot be allowed anyway"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, `data-gate="block" aria-pressed="true" disabled`) || strings.Contains(html, "gj-check-override") {
		t.Errorf("the strictest gate is pressable and has no override:\n%s", html)
	}
	clicks := findHandlers(CheckPanel(props("block", "block")), "click")
	if len(clicks) != 2 {
		t.Fatalf("click handlers: %d", len(clicks))
	}
	ev := xango.NewEvent("click", nil, false, nil, nil)
	clicks[1](ev)
	if len(gate) != 1 || gate[0] != "block" || overrides != 0 {
		t.Errorf("gate %v overrides %d", gate, overrides)
	}
	html = render(t, CheckPanel(props("block", "warn")))
	if !strings.Contains(html, `data-gate="" aria-pressed="false" disabled="disabled"`) || strings.Contains(html, `data-gate="warn" aria-pressed="false" disabled`) ||
		!strings.Contains(html, "gj-check-override") || !strings.Contains(html, "cannot be lowered here") {
		t.Errorf("warn floor:\n%s", html)
	}
	if html = render(t, CheckPanel(props("", ""))); strings.Contains(html, "disabled") || strings.Contains(html, "gj-check-floor") {
		t.Errorf("no floor:\n%s", html)
	}
}

func TestAccessibleTextHearsSuggestedChanges(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{{Kind: A11yParagraph, Text: "the red fox", Runs: []A11yRun{
		{Text: "the "}, {Text: "brown", Change: "del", Author: "Ana"}, {Text: "red", Change: "ins", Author: "Ana"}, {Text: " fox", Lang: "fr"}, {Text: "!", Change: "ins"}}}}}))
	for _, want := range []string{"<del><span class=\"gj-sr-only\">Suggested removal by Ana: </span>brown</del>", "<ins><span class=\"gj-sr-only\">Suggested addition by Ana: </span>red</ins>", `<span lang="fr"> fox</span>`, "Suggested addition by Someone: "} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
}
