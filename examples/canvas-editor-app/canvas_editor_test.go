//go:build !(js && wasm)

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/e2etest"
)

func TestCanvasEditorEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).OpenURL(t, "/index.html", e2etest.Options{Width: 900, Height: 700, ConsoleErrors: true})
	d.Sleep(800)
	ok := d.OK

	docState := func() string { return d.Text("#doc-state") }
	field := func(name string) string {
		state := docState()
		if name == "text" {
			m := regexp.MustCompile(`(?s)^text=(.*) cursor=`).FindStringSubmatch(state)
			if m == nil {
				return ""
			}
			return m[1]
		}
		m := regexp.MustCompile(name + `=([^ ]*)`).FindStringSubmatch(state)
		if m == nil {
			return ""
		}
		return m[1]
	}
	var box []float64
	d.Eval(`(r => [r.x, r.y, r.width, r.height])(document.getElementById('editor-canvas').getBoundingClientRect())`, &box)
	at := func(x, y float64) (float64, float64) { return box[0] + x, box[1] + y }
	click := func(x, y float64) { px, py := at(x, y); d.MouseClick(px, py, 1) }
	clearAll := func() { d.Press("Control+a"); d.Press("Backspace") }

	click(10, 10)
	clearAll()
	ok(field("text") == "", "select-all + backspace clears the document")
	d.Type("hello")
	ok(field("text") == "hello", "typing after a click inserts at the cursor")

	x0, y0 := at(10, 20)
	x1, _ := at(50, 20)
	d.MouseMove(x0, y0)
	d.CanvasDragPage(x0, y0, x1, y0)
	ok(field("sel") != "0-0" && field("sel") != "", "drag produced a non-empty selection, got sel="+field("sel"))
	d.Click("#toolbar-bold")
	ok(field("selBold") == "true", "clicking Bold toggled the dragged selection bold")

	click(10, 10)

	clearAll()
	d.Type("line one\nline two\nline three")
	ok(field("text") == "line one\nline two\nline three", fmt.Sprintf("typed three lines, got: %q", field("text")))

	d.Press("Home")
	d.Press("Alt+ArrowUp")
	ok(field("text") == "line one\nline three\nline two", fmt.Sprintf("Alt+ArrowUp swapped line three above line two, got: %q", field("text")))
	d.Press("Alt+ArrowDown")
	ok(field("text") == "line one\nline two\nline three", fmt.Sprintf("Alt+ArrowDown swapped it back, got: %q", field("text")))

	d.Press("Home") // still on "line three"
	d.Press("Shift+Alt+ArrowDown")
	ok(field("text") == "line one\nline two\nline three\nline three", fmt.Sprintf("Shift+Alt+ArrowDown duplicated the current line, got: %q", field("text")))

	clearAll()
	d.Type("hello world")
	d.Press("Home")
	d.Press("Control+Shift+ArrowRight")
	ok(field("sel") == "0-5", `Ctrl+Shift+ArrowRight selected exactly "hello" (0-5), got sel=`+field("sel"))
	d.Press("Control+Shift+U")
	ok(field("text") == "HELLO world", fmt.Sprintf("Ctrl+Shift+U uppercased the selection, got: %q", field("text")))
	d.Press("Control+Shift+L")
	ok(field("text") == "hello world", fmt.Sprintf("Ctrl+Shift+L lowercased it back, got: %q", field("text")))
	d.Press("End")
	d.Press("Control+Shift+ArrowLeft")
	ok(field("sel") == "6-11", `Ctrl+Shift+ArrowLeft from end selected "world" (6-11), got sel=`+field("sel"))

	d.GrantClipboard()
	clipboard := func(want string) (got string) {
		for i := 0; i < 40; i++ {
			d.Eval(`navigator.clipboard.readText()`, &got)
			if got == want {
				return got
			}
			d.Sleep(50)
		}
		return got
	}
	shortcut := func(key string) {
		d.Eval(`document.getElementById('editor-hidden-input').dispatchEvent(new KeyboardEvent('keydown', {key: `+e2etest.J(key)+`, ctrlKey: true, bubbles: true, cancelable: true}))`, nil)
	}
	shortcut("c")
	clip := clipboard("world")
	ok(clip == "world", fmt.Sprintf("Ctrl+C puts the selection on the clipboard, got %q", clip))
	ok(field("text") == "hello world", "and leaves the text alone")
	shortcut("x")
	clip = clipboard("world")
	for i := 0; i < 40 && field("text") != "hello "; i++ { // the cut finishes when the clipboard write does
		d.Sleep(50)
	}
	ok(clip == "world" && field("text") == "hello ", fmt.Sprintf("Ctrl+X copies and removes it, got clipboard %q and text %q", clip, field("text")))
	d.Press("Control+z")
	ok(field("text") == "hello world", fmt.Sprintf("Ctrl+Z undoes the cut in one step, got %q", field("text")))
	d.Press("Control+Shift+z")
	ok(field("text") == "hello ", fmt.Sprintf("Ctrl+Shift+Z redoes it, got %q", field("text")))
	d.Press("Control+z")
	d.Press("Control+y")
	ok(field("text") == "hello ", fmt.Sprintf("Ctrl+Y redoes too, got %q", field("text")))
	d.Press("Control+z")
	d.Type("there")
	d.Press("Control+z")
	ok(field("text") == "hello world", fmt.Sprintf("typing is one step: Ctrl+Z takes back the whole word, got %q", field("text")))

	clearAll()
	var lines []string
	for i := range 60 {
		lines = append(lines, "line number "+strconv.Itoa(i))
	}
	d.Type(strings.Join(lines, "\n"))
	afterTyping, _ := strconv.Atoi(field("scrollY"))
	ok(afterTyping > 0, fmt.Sprintf("typing past the viewport auto-scrolled (scrollY=%d > 0)", afterTyping))
	d.Hover("#editor-canvas")
	d.Wheel(0, -100000)
	ok(field("scrollY") == "0", "wheel-scrolling far up clamps to scrollY=0, got "+field("scrollY"))
	d.Wheel(0, 100000) // and ceil it at the document's actual max scroll
	atBottom, _ := strconv.Atoi(field("scrollY"))
	ok(atBottom > afterTyping-50, fmt.Sprintf("wheel-scrolling far down clamps near the bottom, got scrollY=%d", atBottom))

	clearAll()
	d.Type("aaaaaaaaaa")
	click(10, 10)
	ok(len(strings.Split(field("cursors"), ",")) == 1, "plain click leaves a single cursor, got cursors="+field("cursors"))
	d.KeyDown("Alt")
	click(60, 10)
	d.KeyUp("Alt")
	ok(len(strings.Split(field("cursors"), ",")) == 2, "Alt+Click added a second cursor, got cursors="+field("cursors"))

	lenBefore := len(field("text"))
	d.Type("X")
	text := field("text")
	ok(len(text) == lenBefore+2, fmt.Sprintf(`typing "X" inserted at both cursors, got: %q`, text))
	ok(strings.Count(text, "X") == 2, fmt.Sprintf("exactly two X characters were inserted, got: %q", text))
	ok(len(strings.Split(field("cursors"), ",")) == 2, "both cursors are still live after typing, got cursors="+field("cursors"))

	d.Press("Escape")
	ok(len(strings.Split(field("cursors"), ",")) == 1, "Escape collapsed back to a single cursor, got cursors="+field("cursors"))

	lenBefore = len(field("text"))
	d.Type("Y")
	text = field("text")
	ok(len(text) == lenBefore+1, fmt.Sprintf(`typing "Y" after Escape inserted only once, got: %q`, text))
	ok(strings.Count(text, "Y") == 1, fmt.Sprintf("exactly one Y character was inserted, got: %q", text))

	var real []string
	for _, e := range d.Errors() {
		if !strings.Contains(e, "the server responded with a status of 404") {
			real = append(real, e)
		}
	}
	ok(len(real) == 0, "no console or page errors: "+strings.Join(real, "; "))
}
