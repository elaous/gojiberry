// Package screenreader is a screen reader for tests. It reads the page the way a screen reader does,
// from the browser's own accessibility tree (the Accessibility domain of the DevTools protocol, the
// tree Chrome gives NVDA, JAWS and VoiceOver through the platform APIs), keeps a cursor that moves
// through it in reading order or by heading, landmark, link, button, form field and table, and turns
// each stop into the phrase a screen reader would say ("Second thoughts, heading, level 2"). A test
// asserts on those phrases, on the order of the page, on what is announced when focus moves and on
// what a live region announces.
//
// It is not NVDA, JAWS or VoiceOver: each has its own speech rules and browse-mode habits, so the
// manual run in docs/accessibility-testing.md stays. What this catches are the regressions that
// need no person: a control with no name, a heading level that skips, a dialog that does not name
// itself, a focus move that says nothing, a status message that is never announced.
//
// Phrases are "<name>, <role>[, <state>...]", with ARIA role names and plain English states. Text is
// read as the text itself.
package screenreader

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// Item is one stop of the cursor.
type Item struct {
	// Role is the ARIA role Chrome computed ("heading", "button", "StaticText", ...).
	Role string
	// Name is the accessible name, or the text of a text node.
	Name string
	// Value is the value of a form control or the text it shows.
	Value string
	// Level is the heading level, 0 for anything else.
	Level int
	// Phrase is what the screen reader says at this stop.
	Phrase string

	node *node
}

// Reader is a screen reader on one page. Create it with New after the page has loaded.
type Reader struct {
	t   testing.TB
	ctx context.Context

	items []Item
	pos   int // index of the current item; -1 before the first
	log   []string
	live  bool
}

// New returns a reader for the page in ctx (the browser context of an e2etest.Tab), with the cursor
// before the first item.
func New(t testing.TB, ctx context.Context) *Reader {
	t.Helper()
	r := &Reader{t: t, ctx: ctx, pos: -1}
	r.Refresh()
	return r
}

type node struct {
	id       accessibility.NodeID
	parent   *node
	role     string
	name     string
	value    string
	desc     string
	ignored  bool
	props    map[string]string
	children []*node
	backend  cdp.BackendNodeID
}

func str(v *accessibility.Value) string {
	if v == nil || len(v.Value) == 0 {
		return ""
	}
	var x any
	if json.Unmarshal(v.Value, &x) != nil {
		return ""
	}
	switch t := x.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

func (r *Reader) call(a chromedp.Action) error {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return chromedp.Run(ctx, a)
}

// Refresh reads the page again. The movement commands do this themselves, so a test rarely needs to;
// the cursor stays on the same element when it is still there.
func (r *Reader) Refresh() {
	r.t.Helper()
	var current cdp.BackendNodeID
	if r.pos >= 0 && r.pos < len(r.items) && r.items[r.pos].node != nil {
		current = r.items[r.pos].node.backend
	}
	var raw []*accessibility.Node
	err := r.call(chromedp.ActionFunc(func(ctx context.Context) (err error) {
		if err = accessibility.Enable().Do(ctx); err != nil {
			return err
		}
		raw, err = accessibility.GetFullAXTree().Do(ctx)
		return err
	}))
	if err != nil {
		r.t.Fatalf("screenreader: reading the accessibility tree: %v", err)
	}
	byID := make(map[accessibility.NodeID]*node, len(raw))
	for _, n := range raw {
		x := &node{id: n.NodeID, role: str(n.Role), name: str(n.Name), value: str(n.Value), desc: str(n.Description), ignored: n.Ignored,
			backend: n.BackendDOMNodeID, props: map[string]string{}}
		for _, p := range n.Properties {
			x.props[string(p.Name)] = str(p.Value)
		}
		byID[n.NodeID] = x
	}
	var root *node
	for _, n := range raw {
		x := byID[n.NodeID]
		for _, c := range n.ChildIDs {
			if child := byID[c]; child != nil {
				child.parent = x
				x.children = append(x.children, child)
			}
		}
	}
	for _, n := range raw {
		if x := byID[n.NodeID]; x.parent == nil && root == nil {
			root = x
		}
	}
	r.items = nil
	if root != nil {
		r.walk(root, false)
	}
	r.items = mergeText(r.items)
	r.pos = -1
	if current != 0 {
		for i, it := range r.items {
			if it.node != nil && it.node.backend == current {
				r.pos = i
			}
		}
	}
}

// namesFromContent are the roles whose descendants' text is already in their name.
var namesFromContent = map[string]bool{
	"heading": true, "link": true, "button": true, "tab": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true,
	"option": true, "treeitem": true, "checkbox": true, "radio": true, "switch": true, "cell": true, "columnheader": true,
	"rowheader": true, "gridcell": true, "caption": true, "legend": true, "img": true, "image": true,
}

// structural roles are never a stop by themselves.
var structural = map[string]bool{
	"": true, "generic": true, "none": true, "presentation": true, "paragraph": true, "listitem": true, "row": true, "rowgroup": true,
	"InlineTextBox": true, "LineBreak": true, "RootWebArea": true, "WebArea": true, "Section": true, "LabelText": true, "caption": true, "legend": true, "DescriptionList": true,
	"DescriptionListTerm": true, "DescriptionListDetail": true, "blockquote": true, "strong": true, "emphasis": true, "mark": true,
	"time": true, "abbr": true, "code": true, "figure": true, "label": true, "Legend": true, "ListMarker": true, "Ignored": true,
}

// spokenElsewhere are roles whose text is already in another item's name (a label's text is the
// field's name, a caption is the table's).
var spokenElsewhere = map[string]bool{"LabelText": true, "caption": true, "legend": true}

// inlineRoles hold text that belongs to the line around them ("Some <strong>bold</strong> text").
var inlineRoles = map[string]bool{"strong": true, "emphasis": true, "mark": true, "code": true, "abbr": true, "time": true, "generic": true}

func (r *Reader) walk(n *node, muted bool) {
	if spokenElsewhere[n.role] {
		muted = true // its text is somebody's name; a field inside it is still read
	}
	if !n.ignored {
		if it, ok := r.item(n); ok && !(muted && it.Role == "StaticText") {
			r.items = append(r.items, it)
			if namesFromContent[n.role] && n.name != "" || n.role == "textbox" || n.role == "searchbox" || n.role == "combobox" {
				return // what is inside is already said
			}
		}
	}
	for _, c := range n.children {
		r.walk(c, muted)
	}
}

func (r *Reader) item(n *node) (Item, bool) {
	role := n.role
	switch {
	case role == "StaticText" || role == "text":
		if strings.TrimSpace(n.name) == "" {
			return Item{}, false
		}
		return Item{Role: "StaticText", Name: strings.TrimSpace(n.name), Phrase: strings.TrimSpace(n.name), node: n}, true
	case structural[role]:
		return Item{}, false
	case (role == "status" || role == "log" || role == "alert") && strings.TrimSpace(n.name) == "" && !hasText(n):
		return Item{}, false
	case (role == "group" || role == "region" || role == "form") && n.name == "":
		return Item{}, false
	}
	it := Item{Role: role, Name: strings.TrimSpace(n.name), Value: n.value, node: n}
	if lv, err := strconv.Atoi(n.props["level"]); err == nil && role == "heading" {
		it.Level = lv
	}
	it.Phrase = phrase(n, it)
	return it, true
}

var landmarks = map[string]string{
	"banner": "banner landmark", "navigation": "navigation landmark", "main": "main landmark", "complementary": "complementary landmark",
	"contentinfo": "content information landmark", "region": "region landmark", "search": "search landmark", "form": "form landmark",
}

func phrase(n *node, it Item) string {
	var parts []string
	name := it.Name
	label := n.role
	if rd := n.props["roledescription"]; rd != "" {
		label = rd
	}
	var states []string
	add := func(s string) { states = append(states, s) }
	switch n.role {
	case "heading":
		add("level " + n.props["level"])
	case "list":
		count := 0
		for _, c := range n.children {
			if c.role == "listitem" {
				count++
			}
		}
		add(fmt.Sprintf("%d items", count))
	case "table", "grid":
		rows := 0
		var count func(*node)
		count = func(x *node) {
			for _, c := range x.children {
				if c.role == "row" {
					rows++
				} else {
					count(c)
				}
			}
		}
		count(n)
		add(fmt.Sprintf("%d rows", rows))
	case "textbox", "searchbox":
		label = "edit text"
		if n.role == "searchbox" {
			label = "search edit text"
		}
		if n.props["multiline"] == "true" {
			label = "multi line edit text"
		}
		if n.value != "" {
			add(n.value)
		} else {
			add("blank")
		}
	case "combobox":
		label = "combo box"
		if n.value != "" {
			add(n.value)
		}
	case "img", "image":
		label = "graphic"
	case "slider", "spinbutton", "progressbar", "scrollbar":
		if v := n.props["valuetext"]; v != "" {
			add(v)
		} else if n.value != "" {
			add(n.value)
		}
	case "tab":
		pos, total := siblingPosition(n, "tab")
		if total > 0 {
			add(fmt.Sprintf("%d of %d", pos, total))
		}
	case "menuitem", "menuitemcheckbox", "menuitemradio":
		label = "menu item"
	case "StaticText":
		return name
	}
	if l, ok := landmarks[n.role]; ok {
		label = l
	}
	// states
	switch n.props["checked"] {
	case "true":
		add("checked")
	case "false":
		if n.role == "checkbox" || n.role == "radio" || n.role == "switch" || n.role == "menuitemcheckbox" {
			add("not checked")
		}
	case "mixed":
		add("partially checked")
	}
	switch n.props["pressed"] {
	case "true":
		add("pressed")
	case "false":
		add("not pressed")
	case "mixed":
		add("partially pressed")
	}
	switch n.props["expanded"] {
	case "true":
		add("expanded")
	case "false":
		add("collapsed")
	}
	if n.props["selected"] == "true" {
		add("selected")
	} else if n.props["selected"] == "false" && (n.role == "tab" || n.role == "option") {
		add("not selected")
	}
	if hp := n.props["hasPopup"]; hp != "" && hp != "false" {
		add("has popup " + hp)
	}
	if n.props["required"] == "true" {
		add("required")
	}
	if inv := n.props["invalid"]; inv != "" && inv != "false" {
		add("invalid entry")
	}
	if n.props["disabled"] == "true" {
		add("unavailable")
	}
	if n.props["readonly"] == "true" {
		add("read only")
	}
	if n.props["modal"] == "true" {
		add("modal")
	}
	if l, ok := roleLabels[n.role]; ok && n.props["roledescription"] == "" {
		label = l
	}
	if name != "" {
		parts = append(parts, name)
	} else if interactive[n.role] || n.role == "img" || n.role == "image" {
		label = "unlabelled " + label
	}
	parts = append(parts, label)
	parts = append(parts, states...)
	out := strings.Join(parts, ", ")
	if n.desc != "" {
		out += ". " + n.desc
	}
	return out
}

var roleLabels = map[string]string{"columnheader": "column header", "rowheader": "row header", "gridcell": "cell", "listbox": "list box",
	"menuitemcheckbox": "menu item", "menuitemradio": "menu item", "tablist": "tab list", "treeitem": "tree item", "alertdialog": "alert dialog"}

var interactive = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true, "combobox": true, "checkbox": true, "radio": true, "switch": true,
	"slider": true, "spinbutton": true, "tab": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true, "option": true,
	"treeitem": true, "listbox": true,
}

// siblingPosition is the 1-based place of n among its siblings of the same role, and how many there are.
func siblingPosition(n *node, role string) (pos, total int) {
	if n.parent == nil {
		return 0, 0
	}
	for _, s := range n.parent.children {
		if s.ignored || s.role != role {
			continue
		}
		total++
		if s == n {
			pos = total
		}
	}
	return pos, total
}

func hasText(n *node) bool {
	for _, c := range n.children {
		if (c.role == "StaticText" && strings.TrimSpace(c.name) != "") || hasText(c) {
			return true
		}
	}
	return false
}

// block is the element whose line a text node is on: its parent, past any inline wrappers.
func block(n *node) *node {
	p := n.parent
	for p != nil && inlineRoles[p.role] && p.parent != nil {
		p = p.parent
	}
	return p
}

// mergeText joins neighbouring text nodes of one line ("Some", "bold", "text.") into one stop, as a
// screen reader reads a line.
func mergeText(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.Role == "StaticText" && len(out) > 0 {
			prev := &out[len(out)-1]
			if prev.Role == "StaticText" && prev.node != nil && it.node != nil && block(prev.node) == block(it.node) {
				prev.Name += " " + it.Name
				prev.Phrase = prev.Name
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// say records what was spoken and returns it.
func (r *Reader) say(s string) string {
	r.log = append(r.log, s)
	return s
}

// Items is every stop of the page in reading order.
func (r *Reader) Items() []Item { r.Refresh(); return append([]Item(nil), r.items...) }

// Current is the item the cursor is on, if it is on one.
func (r *Reader) Current() (Item, bool) {
	if r.pos < 0 || r.pos >= len(r.items) {
		return Item{}, false
	}
	return r.items[r.pos], true
}

// Reset puts the cursor before the first item.
func (r *Reader) Reset() { r.Refresh(); r.pos = -1 }

// Next moves to the next item and says it. ok is false, and nothing is said, at the end of the page.
func (r *Reader) Next() (string, bool) { return r.NextWhere(func(Item) bool { return true }) }

// Previous moves to the previous item and says it.
func (r *Reader) Previous() (string, bool) { return r.PreviousWhere(func(Item) bool { return true }) }

// NextWhere moves to the next item for which match is true.
func (r *Reader) NextWhere(match func(Item) bool) (string, bool) {
	r.t.Helper()
	r.Refresh()
	for i := r.pos + 1; i < len(r.items); i++ {
		if match(r.items[i]) {
			r.pos = i
			return r.say(r.items[i].Phrase), true
		}
	}
	return "", false
}

// PreviousWhere moves to the previous item for which match is true.
func (r *Reader) PreviousWhere(match func(Item) bool) (string, bool) {
	r.t.Helper()
	r.Refresh()
	start := r.pos - 1
	if r.pos < 0 {
		start = len(r.items) - 1
	}
	for i := start; i >= 0; i-- {
		if match(r.items[i]) {
			r.pos = i
			return r.say(r.items[i].Phrase), true
		}
	}
	return "", false
}

// NextHeading moves to the next heading, of one of the levels if any are given.
func (r *Reader) NextHeading(levels ...int) (string, bool) {
	return r.NextWhere(func(it Item) bool {
		if it.Role != "heading" {
			return false
		}
		if len(levels) == 0 {
			return true
		}
		for _, l := range levels {
			if l == it.Level {
				return true
			}
		}
		return false
	})
}

// NextLandmark moves to the next landmark (banner, navigation, main, ...).
func (r *Reader) NextLandmark() (string, bool) {
	return r.NextWhere(func(it Item) bool { _, ok := landmarks[it.Role]; return ok })
}

// NextLink, NextButton, NextFormField and NextTable move by kind.
func (r *Reader) NextLink() (string, bool) {
	return r.NextWhere(func(it Item) bool { return it.Role == "link" })
}
func (r *Reader) NextButton() (string, bool) {
	return r.NextWhere(func(it Item) bool { return it.Role == "button" })
}
func (r *Reader) NextTable() (string, bool) {
	return r.NextWhere(func(it Item) bool { return it.Role == "table" || it.Role == "grid" })
}
func (r *Reader) NextFormField() (string, bool) {
	return r.NextWhere(func(it Item) bool {
		switch it.Role {
		case "textbox", "searchbox", "combobox", "checkbox", "radio", "switch", "slider", "spinbutton", "listbox":
			return true
		}
		return false
	})
}

// MoveTo puts the cursor on the first item with this role and name (the name may be a part), without
// saying it. It reports whether there is one.
func (r *Reader) MoveTo(role, name string) bool {
	r.Refresh()
	for i, it := range r.items {
		if it.Role == role && strings.Contains(it.Name, name) {
			r.pos = i
			return true
		}
	}
	return false
}

// ReadAll reads the whole page from the top and returns what was said, in order.
func (r *Reader) ReadAll() []string {
	r.Reset()
	var out []string
	for {
		s, ok := r.Next()
		if !ok {
			return out
		}
		out = append(out, s)
	}
}

// Headings is the outline of the page: each heading's phrase in order.
func (r *Reader) Headings() []string {
	r.Refresh()
	var out []string
	for _, it := range r.items {
		if it.Role == "heading" {
			out = append(out, it.Phrase)
		}
	}
	return out
}

// Unlabelled lists the controls and graphics a screen reader could only call "unlabelled".
func (r *Reader) Unlabelled() []string {
	r.Refresh()
	var out []string
	for _, it := range r.items {
		if (interactive[it.Role] || it.Role == "img" || it.Role == "image") && it.Name == "" {
			out = append(out, it.Phrase)
		}
	}
	return out
}

// HeadingProblems lists heading outline faults: no level 1, more than one, or a level skipped on the way down.
func (r *Reader) HeadingProblems() []string {
	r.Refresh()
	var out []string
	last, h1 := 0, 0
	for _, it := range r.items {
		if it.Role != "heading" {
			continue
		}
		if it.Level == 1 {
			h1++
		}
		if last != 0 && it.Level > last+1 {
			out = append(out, fmt.Sprintf("%q is level %d after level %d", it.Name, it.Level, last))
		}
		last = it.Level
	}
	if last != 0 && h1 == 0 {
		out = append(out, "the page has headings but no level 1")
	}
	if h1 > 1 {
		out = append(out, fmt.Sprintf("the page has %d level 1 headings", h1))
	}
	return out
}

// Focused is what a screen reader says for the element that has the keyboard focus, or "" if none.
func (r *Reader) Focused() string {
	r.t.Helper()
	r.Refresh()
	for i, it := range r.items {
		if it.node != nil && it.node.props["focused"] == "true" && it.Role != "StaticText" {
			r.pos = i
			return it.Phrase
		}
	}
	return ""
}

// Press sends a key to the page (see chromedp's kb package: kb.Tab, kb.Enter, kb.ArrowDown, ...) and
// says what is focused afterwards. It returns "" when focus is on nothing.
func (r *Reader) Press(key string) string {
	r.t.Helper()
	if err := r.call(chromedp.KeyEvent(key)); err != nil {
		r.t.Fatalf("screenreader: key: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the page react
	if s := r.Focused(); s != "" {
		return r.say(s)
	}
	return ""
}

// Tab moves focus to the next focusable element and says it; ShiftTab goes back.
func (r *Reader) Tab() string { return r.Press(kb.Tab) }

// ShiftTab moves focus to the previous focusable element and says it.
func (r *Reader) ShiftTab() string {
	r.t.Helper()
	if err := r.call(chromedp.KeyEvent(kb.Tab, chromedp.KeyModifiers(input.ModifierShift))); err != nil {
		r.t.Fatalf("screenreader: key: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if s := r.Focused(); s != "" {
		return r.say(s)
	}
	return ""
}

// Spoken is everything said so far; ClearSpoken forgets it.
func (r *Reader) Spoken() []string { return append([]string(nil), r.log...) }

// ClearSpoken empties the log.
func (r *Reader) ClearSpoken() { r.log = nil }

// Announcement is a message a live region made.
type Announcement struct {
	Text string
	// Assertive is true for an alert or aria-live="assertive": it would interrupt the speech.
	Assertive bool
}

const watchJS = `(() => {
  if (window.__gjLive) return;
  window.__gjLive = [];
  const sel = '[aria-live]:not([aria-live="off"]), [role="status"], [role="alert"], [role="log"]';
  const last = new WeakMap();
  new MutationObserver((muts) => {
    const seen = new Set();
    for (const m of muts) {
      const base = m.target.nodeType === 1 ? m.target : m.target.parentElement;
      const el = base && base.closest(sel);
      if (!el || seen.has(el)) continue;
      seen.add(el);
      const text = (el.textContent || '').replace(/\s+/g, ' ').trim();
      if (!text || last.get(el) === text) continue;
      last.set(el, text);
      const assertive = el.getAttribute('role') === 'alert' || el.getAttribute('aria-live') === 'assertive';
      window.__gjLive.push({text, assertive});
    }
  }).observe(document.documentElement, {subtree: true, childList: true, characterData: true});
})()`

// WatchLive starts recording what live regions (aria-live, role status, alert and log) say from now
// on. Call it before the action that should make an announcement.
func (r *Reader) WatchLive() {
	r.t.Helper()
	if err := r.call(chromedp.Evaluate(watchJS, nil)); err != nil {
		r.t.Fatalf("screenreader: watching live regions: %v", err)
	}
	r.live = true
}

// Announcements returns what the live regions announced since the last call, and forgets it.
func (r *Reader) Announcements() []Announcement {
	r.t.Helper()
	if !r.live {
		r.t.Fatal("screenreader: call WatchLive before Announcements")
	}
	var out []Announcement
	if err := r.call(chromedp.Evaluate(`(() => { const a = window.__gjLive || []; window.__gjLive = []; return a; })()`, &out)); err != nil {
		r.t.Fatalf("screenreader: reading announcements: %v", err)
	}
	return out
}
