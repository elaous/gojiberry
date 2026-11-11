//go:build !(js && wasm)

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

const stopJS = `(() => { const a = document.activeElement; return a.tagName + '#' + a.id + (a.getAttribute('role') === 'tab' ? '[tab]' : '') + ' ' + (a.getAttribute('aria-label') || a.textContent || a.value || '').trim().slice(0, 40).replace(/\s+/g, ' '); })()`

func TestTabOrderIsACleanCycle(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	var stops []string
	seen := map[string]bool{}
	for range 120 {
		d.Press("Tab")
		s := d.Str(stopJS)
		if strings.HasPrefix(s, "BODY#") {
			break // out of the page's elements: the cycle is over
		}
		if seen[s] {
			t.Fatalf("a stop is visited twice before the cycle ends: %q (after %q)", s, stops)
		}
		seen[s] = true
		stops = append(stops, s)
		if strings.HasPrefix(s, "P#state") {
			t.Errorf("the state dump takes a Tab stop: %q", stops)
		}
		hidden := d.Bool(`(() => { const a = document.activeElement; const r = a.getBoundingClientRect(); const inMirror = !!a.closest('#a11y-text'); return !inMirror && (r.width === 0 || r.height === 0) && a.id !== 'input'; })()`)
		if hidden {
			t.Errorf("a stop with no size: %q", s)
		}
	}
	if len(stops) < 20 || len(stops) > 110 {
		t.Fatalf("the cycle has %d stops: %q", len(stops), stops)
	}
	if !strings.Contains(stops[0], "Skip to the document") || !strings.Contains(stops[1], "Skip to the side panel") {
		t.Errorf("the first stops are the skip links: %q", stops[:2])
	}
	for i := len(stops) - 1; i >= len(stops)-5 && i >= 0; i-- {
		d.Press("Shift+Tab")
		if got := d.Str(stopJS); got != stops[i] {
			t.Errorf("Shift+Tab to stop %d: %q, want %q", i, got, stops[i])
		}
	}
	d.NoErrors()
}

func TestEveryMenuEntryIsReachableByKeyboard(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Eval(`document.querySelector('.gj-menubar-button').focus()`, nil)
	buttons := d.Int(`document.querySelectorAll('.gj-menubar-button').length`)
	if buttons < 6 {
		t.Fatalf("%d menus", buttons)
	}
	total := 0
	for i := range buttons {
		d.OK(d.Bool(`document.activeElement.classList.contains('gj-menubar-button')`), fmt.Sprintf("the menu %d button has the focus", i))
		d.Press("ArrowDown")
		d.WaitSelector(`.gj-menubar-panel .gj-menu`)
		want := d.Int(`document.querySelectorAll('.gj-menubar-panel > .gj-menu > li.gj-menu-item[tabindex]').length`)
		var visited []string
		for range want + 2 {
			label := d.Str(`document.activeElement.textContent.trim().slice(0, 50)`)
			if len(visited) > 0 && label == visited[0] {
				break
			}
			visited = append(visited, label)
			if d.Bool(`document.activeElement.getAttribute('aria-haspopup') === 'menu'`) {
				d.Press("ArrowRight")
				sub := d.Int(`document.activeElement.parentElement.querySelectorAll(':scope > li.gj-menu-item[tabindex]').length`)
				if sub < 1 || !d.Bool(`!!document.activeElement.closest('.gj-menu-item-sub .gj-menu')`) {
					t.Errorf("menu %d, %q: Right did not enter the submenu", i, label)
				}
				seen := map[string]bool{}
				for range sub + 1 {
					seen[d.Str(`document.activeElement.textContent.trim()`)] = true
					d.Press("ArrowDown")
				}
				if len(seen) != sub {
					t.Errorf("menu %d, %q: %d of %d submenu entries reached", i, label, len(seen), sub)
				}
				total += sub
				d.Press("ArrowLeft")
			}
			d.Press("ArrowDown")
		}
		if len(visited) != want {
			t.Errorf("menu %d: %d of %d entries reached with the keyboard: %q", i, len(visited), want, visited)
		}
		total += len(visited)
		d.Press("Escape")
		d.WaitFor("the menu closing", `!document.querySelector('.gj-menubar-panel .gj-menu')`)
		d.OK(d.Bool(`document.activeElement.classList.contains('gj-menubar-button')`), "Escape gives the focus back to the menu button")
		if i < buttons-1 {
			d.Press("ArrowRight")
		}
	}
	t.Logf("%d menu entries reached with the keyboard", total)
	if total < 50 {
		t.Errorf("only %d entries in all", total)
	}
	d.NoErrors()
}

func tabUntil(t *testing.T, d *e2etest.Tab, sel string, max int) {
	t.Helper()
	for range max {
		d.Press("Tab")
		if d.Bool(`document.activeElement.matches(` + e2etest.J(sel) + `)`) {
			return
		}
	}
	t.Fatalf("Tab did not reach %s in %d presses; the focus is on %s", sel, max, d.Str(stopJS))
}

func TestTheEditorWithoutAPointer(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).Open(t, "?lang=en")

	d.Press("Tab")
	d.OK(strings.Contains(d.Str(stopJS), "Skip to the document"), "the first stop is the skip link: "+d.Str(stopJS))
	d.Press("Enter")
	d.OK(d.Bool(`document.activeElement.id === 'input' || !!document.activeElement.closest('[data-gj-document]') || document.activeElement.id === 'doc'`) || d.Str(`document.activeElement.tagName`) != "BODY", "the focus is in the document area")

	d.Eval(`document.body.focus()`, nil)
	tabUntil(t, d, "#mode", 60)
	d.Press("Enter")
	d.Wait(`s.mode === 'edit'`)

	tabUntil(t, d, "#input", 20)
	d.Press("Control+Home")
	d.Type("Typed with the keyboard only. ")
	d.Wait(`s.text.includes('Typed with the keyboard only.')`)

	d.Eval(`document.body.focus()`, nil)
	tabUntil(t, d, ".gj-menubar-button", 60)
	for !strings.HasPrefix(d.Str(`document.activeElement.textContent.trim()`), "Format") {
		d.Press("ArrowRight")
	}
	d.Press("ArrowDown")
	for range 30 {
		if strings.HasPrefix(d.Str(`document.activeElement.textContent.trim()`), "Style editor") {
			break
		}
		d.Press("ArrowDown")
	}
	d.Press("Enter")
	d.WaitSelector(".gj-styleeditor-size")

	d.Eval(`document.body.focus()`, nil)
	tabUntil(t, d, `[role="tab"][aria-selected="true"]`, 80)
	for range 30 {
		if d.Str(`document.activeElement.dataset.tab`) == "save" {
			break
		}
		d.Press("ArrowRight")
	}
	d.OK(d.Str(`document.activeElement.dataset.tab`) == "save", "the Save tab by arrow keys")
	d.WaitSelector(".gj-saver-name")
	tabUntil(t, d, ".gj-saver-name", 20)
	d.Type("keyboard notes")
	tabUntil(t, d, ".gj-saver-save", 12)
	d.Press("Enter")
	d.WaitFor("the saved document in the list", `document.querySelector('.gj-doclist-item')`)
	d.OK(strings.Contains(d.Text(".gj-saver-status"), "keyboard notes"), "saved: "+d.Text(".gj-saver-status"))
	d.NoErrors()
}
