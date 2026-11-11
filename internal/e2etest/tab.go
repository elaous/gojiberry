//go:build !(js && wasm)

package e2etest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/webauthn"
	"github.com/chromedp/chromedp"
)

const Timeout = 15 * time.Second

// Options for Open.
type Options struct {
	Width, Height int  // viewport; 1000x800 if zero
	ConsoleErrors bool // count console.error output as page errors
	Path          string
}

// Tab is one browser on a page.
type Tab struct {
	t    testing.TB
	ctx  context.Context
	site *Site

	mu         sync.Mutex
	errs       []string
	dialog     *string  // the answer to give the next dialog (one shot)
	dialogQ    []string // answers for the dialogs after it
	dialogSeen []string // the messages of the dialogs that opened
	files      []string
	chooser    chan struct{}
	dlName     chan string
	routes     []*route
	mx, my     float64
	mods       input.Modifier // keys held down with KeyDown
	box        [4]float64     // #stage, in page coordinates
	closeFns   []func()
}

type route struct {
	substr string
	handle func() (status int, contentType string, body []byte)
	hits   int
}

func chromiumPath(t testing.TB) string {
	for _, env := range []string{"CHROMIUM", "PLAYWRIGHT_CHROMIUM"} {
		if p := os.Getenv(env); p != "" {
			return p
		}
	}
	if _, err := os.Stat("/opt/pw-browsers/chromium"); err == nil {
		return "/opt/pw-browsers/chromium"
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if os.Getenv("GOJIBERRY_REQUIRE_BROWSER") != "" {
		t.Fatal("no Chromium found (set CHROMIUM), and GOJIBERRY_REQUIRE_BROWSER is set")
	}
	t.Skip("no Chromium found (set CHROMIUM)")
	return ""
}

func (s *Site) Open(t testing.TB, query string, opts ...Options) *Tab {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Width == 0 {
		o.Width, o.Height = 1000, 800
	}
	tab := s.launch(t, o)
	url := s.URL + "/index.html" + query
	if strings.HasPrefix(query, "/") {
		url = s.URL + query
	}
	tab.must(chromedp.Navigate(url))
	tab.WaitFor("the page to start", "document.getElementById('state') && document.getElementById('state').textContent !== '(pending)'")
	var b []float64
	tab.Eval(`(() => { const c = document.getElementById('stage'); if (!c) return [0, 0, 0, 0]; const r = c.getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; })()`, &b)
	copy(tab.box[:], b)
	return tab
}

func (s *Site) OpenURL(t testing.TB, path string, opts ...Options) *Tab {
	t.Helper()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Width == 0 {
		o.Width, o.Height = 1000, 800
	}
	tab := s.launch(t, o)
	tab.must(chromedp.Navigate(s.URL + path))
	return tab
}

func (s *Site) launch(t testing.TB, o Options) *Tab {
	exe := chromiumPath(t)
	var tab *Tab
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
			append(chromedp.DefaultExecAllocatorOptions[:],
				chromedp.ExecPath(exe), chromedp.NoSandbox, chromedp.Flag("headless", true),
				chromedp.Flag("disable-gpu", true), chromedp.WindowSize(o.Width, o.Height),
				chromedp.Flag("use-fake-device-for-media-stream", true), chromedp.Flag("use-fake-ui-for-media-stream", true),
				chromedp.WSURLReadTimeout(60*time.Second))...)
		ctx, cancel := chromedp.NewContext(alloc)
		tab = &Tab{t: t, ctx: ctx, site: s, chooser: make(chan struct{}, 8), dlName: make(chan string, 64)}
		tab.closeFns = append(tab.closeFns, cancel, cancelAlloc)
		chromedp.ListenTarget(ctx, tab.onEvent(o))
		if err = chromedp.Run(ctx); err == nil {
			break
		}
		tab.Close()
	}
	if err != nil {
		t.Fatalf("starting the browser: %v", err)
	}
	t.Cleanup(tab.Close)
	selectorJS, err := selectorScript()
	if err != nil {
		t.Fatal(err)
	}
	tab.must(chromedp.Tasks{
		chromedp.EmulateViewport(int64(o.Width), int64(o.Height)),
		page.SetInterceptFileChooserDialog(true),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(selectorJS).Do(ctx)
			return err
		}),
	})
	tab.must(chromedp.Evaluate(selectorJS, nil))
	return tab
}

// Close ends the browser.
func (d *Tab) Close() {
	for _, f := range d.closeFns {
		f()
	}
	d.closeFns = nil
}

const CallTimeout = 45 * time.Second

func (d *Tab) run(a chromedp.Action) error {
	ctx, cancel := context.WithTimeout(d.ctx, CallTimeout)
	defer cancel()
	return chromedp.Run(ctx, a)
}

func (d *Tab) must(a chromedp.Action) {
	d.t.Helper()
	if err := d.run(a); err != nil {
		d.t.Fatalf("browser: %v", err)
	}
}

func (d *Tab) onEvent(o Options) func(any) {
	return func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventExceptionThrown:
			msg := e.ExceptionDetails.Text
			if e.ExceptionDetails.Exception != nil {
				msg = e.ExceptionDetails.Exception.Description
			}
			d.addErr(msg)
		case *runtime.EventConsoleAPICalled:
			for _, a := range e.Args {
				if v := string(a.Value); strings.Contains(v, "panic: ") || strings.Contains(v, "fatal error: ") {
					d.addErr(strings.Trim(v, `"`))
				}
			}
			if o.ConsoleErrors && e.Type == runtime.APITypeError {
				var parts []string
				for _, a := range e.Args {
					parts = append(parts, strings.Trim(string(a.Value), `"`))
				}
				d.addErr(strings.Join(parts, " "))
			}
		case *page.EventJavascriptDialogOpening:
			d.mu.Lock()
			ans := d.dialog
			d.dialog = nil
			if ans == nil && len(d.dialogQ) > 0 { // answers lined up for a page that asks again
				ans, d.dialogQ = &d.dialogQ[0], d.dialogQ[1:]
			}
			d.dialogSeen = append(d.dialogSeen, e.Message)
			d.mu.Unlock()
			go func() {
				text := ""
				if ans != nil {
					text = *ans
				}
				_ = chromedp.Run(d.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
					return page.HandleJavaScriptDialog(ans != nil).WithPromptText(text).Do(ctx)
				}))
			}()
		case *page.EventFileChooserOpened:
			d.mu.Lock()
			files := d.files
			d.mu.Unlock()
			id := e.BackendNodeID
			go func() {
				_ = chromedp.Run(d.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
					return dom.SetFileInputFiles(files).WithBackendNodeID(id).Do(ctx)
				}))
				select {
				case d.chooser <- struct{}{}:
				default:
				}
			}()
		case *browser.EventDownloadWillBegin:
			select {
			case d.dlName <- e.SuggestedFilename:
			default: // nobody is waiting for it: never block the event loop
			}
		case *fetch.EventRequestPaused:
			go d.fulfil(e)
		}
	}
}

func (d *Tab) addErr(s string) { d.mu.Lock(); d.errs = append(d.errs, s); d.mu.Unlock() }

// Errors are the uncaught page errors so far.
func (d *Tab) Errors() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.errs...)
}

// NoErrors checks that the page has had none.
func (d *Tab) NoErrors() {
	d.t.Helper()
	d.OK(len(d.Errors()) == 0, "no page errors: "+strings.Join(d.Errors(), "; "))
}

func (d *Tab) OK(cond bool, msg string) {
	d.t.Helper()
	OK(d.t, cond, msg)
}

func OK(t testing.TB, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Fatalf("FAIL: %s", msg)
	}
	t.Logf("ok: %s", msg)
}

// J is v as a JavaScript literal, for building expressions.
func J(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func (d *Tab) Eval(js string, out any) {
	d.t.Helper()
	d.must(chromedp.Evaluate(js, out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
}

func (d *Tab) Str(js string) (s string)    { d.t.Helper(); d.Eval(js, &s); return }
func (d *Tab) Int(js string) (n int)       { d.t.Helper(); d.Eval(js, &n); return }
func (d *Tab) Bool(js string) (b bool)     { d.t.Helper(); d.Eval(js, &b); return }
func (d *Tab) Strs(js string) (s []string) { d.t.Helper(); d.Eval(js, &s); return }
func (d *Tab) Float(js string) (f float64) { d.t.Helper(); d.Eval(js, &f); return }

const stateJS = `JSON.parse(document.getElementById('state').textContent)`

// State is the page's #state JSON.
func (d *Tab) State() State {
	d.t.Helper()
	var raw string
	d.Eval(`document.getElementById('state').textContent`, &raw)
	var s State
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		d.t.Fatalf("#state is not JSON: %v: %.200s", err, raw)
	}
	return s
}

func (d *Tab) Is(expr string) bool {
	d.t.Helper()
	var ok bool
	d.Eval(`(s => !!(`+expr+`))(`+stateJS+`)`, &ok)
	return ok
}

// WaitFor polls a JavaScript expression until it is true.
func (d *Tab) WaitFor(what, expr string) { d.t.Helper(); d.WaitForTimeout(what, expr, Timeout) }

// WaitForTimeout is WaitFor with its own limit.
func (d *Tab) WaitForTimeout(what, expr string, limit time.Duration) {
	d.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		var ok bool
		_ = d.run(chromedp.Evaluate(`(() => { try { return !!(`+expr+`); } catch (e) { return false; } })()`, &ok))
		if ok {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("timed out waiting for %s: %s\npage errors: %v", what, expr, d.Errors())
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (d *Tab) Wait(expr string) {
	d.t.Helper()
	d.WaitFor("state "+expr, `(s => !!(`+expr+`))(`+stateJS+`)`)
}

// Sleep waits, for the cases with nothing to poll.
func (d *Tab) Sleep(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func sel(s string) string { return J(s) }

// Count is how many elements match the selector.
func (d *Tab) Count(selector string) int {
	d.t.Helper()
	return d.Int(`window.__e2e.all(` + sel(selector) + `).length`)
}

// Has is whether any element matches.
func (d *Tab) Has(selector string) bool { d.t.Helper(); return d.Count(selector) > 0 }

func (d *Tab) need(selector string) {
	d.t.Helper()
	d.WaitFor("an element "+selector, `window.__e2e.first(`+sel(selector)+`)`)
}

// WaitSelector waits for an element matching the selector to exist.
func (d *Tab) WaitSelector(selector string) { d.t.Helper(); d.need(selector) }

func (d *Tab) Text(selector string) string {
	d.t.Helper()
	d.need(selector)
	return d.Str(`window.__e2e.first(` + sel(selector) + `).textContent`)
}

// Texts are the text of every match.
func (d *Tab) Texts(selector string) []string {
	d.t.Helper()
	return d.Strs(`window.__e2e.all(` + sel(selector) + `).map(e => e.textContent)`)
}

func (d *Tab) Attr(selector, name string) string {
	d.t.Helper()
	d.need(selector)
	return d.Str(`window.__e2e.first(` + sel(selector) + `).getAttribute(` + J(name) + `) || ''`)
}

// Value is the value of the first match, an input.
func (d *Tab) Value(selector string) string {
	d.t.Helper()
	d.need(selector)
	return d.Str(`window.__e2e.first(` + sel(selector) + `).value`)
}

// Disabled is whether the first match is disabled.
func (d *Tab) Disabled(selector string) bool {
	d.t.Helper()
	d.need(selector)
	return d.Bool(`window.__e2e.first(` + sel(selector) + `).disabled === true`)
}

// Visible is whether the first match is on screen.
func (d *Tab) Visible(selector string) bool {
	d.t.Helper()
	return d.Bool(`(e => !!e && window.__e2e.visible(e))(window.__e2e.first(` + sel(selector) + `))`)
}

// Focus focuses the first match.
func (d *Tab) Focus(selector string) {
	d.t.Helper()
	d.need(selector)
	d.Eval(`window.__e2e.first(`+sel(selector)+`).focus()`, nil)
}

// Fill replaces an input's value and fires its input event.
func (d *Tab) Fill(selector, value string) {
	d.t.Helper()
	d.need(selector)
	d.Eval(`(e => { e.focus(); e.value = `+J(value)+`; e.dispatchEvent(new Event('input', {bubbles: true})); })(window.__e2e.first(`+sel(selector)+`))`, nil)
}

func (d *Tab) Select(selector, value string) {
	d.t.Helper()
	d.need(selector)
	d.Eval(`(e => { e.value = `+J(value)+`; e.dispatchEvent(new Event('input', {bubbles: true})); e.dispatchEvent(new Event('change', {bubbles: true})); })(window.__e2e.first(`+sel(selector)+`))`, nil)
}

// Dispatch fires an event on the first match.
func (d *Tab) Dispatch(selector, event string) {
	d.t.Helper()
	d.need(selector)
	d.Eval(`window.__e2e.first(`+sel(selector)+`).dispatchEvent(new Event(`+J(event)+`, {bubbles: true}))`, nil)
}

func (d *Tab) SetChecked(selector string, on bool) {
	d.t.Helper()
	d.need(selector)
	if d.Bool(`window.__e2e.first(`+sel(selector)+`).checked`) != on {
		d.Click(selector)
	}
}

// Checked is whether a checkbox is ticked.
func (d *Tab) Checked(selector string) bool {
	d.t.Helper()
	d.need(selector)
	return d.Bool(`window.__e2e.first(` + sel(selector) + `).checked`)
}

func (d *Tab) mouse(typ input.MouseType, x, y float64, button input.MouseButton, clicks int64, dx, dy float64) {
	d.t.Helper()
	d.mx, d.my = x, y
	var buttons int64
	if typ == input.MousePressed || (typ == input.MouseMoved && button != input.None) {
		buttons = 1
	}
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(typ, x, y).WithModifiers(d.mods).WithButton(button).WithButtons(buttons).WithClickCount(clicks).WithDeltaX(dx).WithDeltaY(dy).Do(ctx)
	}))
}

// MouseMove moves the pointer to page coordinates.
func (d *Tab) MouseMove(x, y float64) {
	d.t.Helper()
	d.mouse(input.MouseMoved, x, y, input.None, 0, 0, 0)
}

func (d *Tab) MouseClick(x, y float64, clicks int) {
	d.t.Helper()
	d.MouseMove(x, y)
	for i := 1; i <= clicks; i++ {
		d.mouse(input.MousePressed, x, y, input.Left, int64(i), 0, 0)
		d.mouse(input.MouseReleased, x, y, input.Left, int64(i), 0, 0)
	}
}

// Wheel scrolls at the pointer.
func (d *Tab) Wheel(dx, dy float64) {
	d.t.Helper()
	d.mouse(input.MouseWheel, d.mx, d.my, input.None, 0, dx, dy)
}

func (d *Tab) Click(selector string) { d.t.Helper(); d.clickSel(selector, 1) }

// DblClick double-clicks it.
func (d *Tab) DblClick(selector string) { d.t.Helper(); d.clickSel(selector, 2) }

// Hover moves the pointer onto it.
func (d *Tab) Hover(selector string) {
	d.t.Helper()
	x, y := d.centre(selector)
	d.MouseMove(x, y)
}

func (d *Tab) Reveal(selector string) {
	d.t.Helper()
	d.WaitFor("an element "+selector, `!!window.__e2e.first(`+sel(selector)+`)`)
	d.Eval(`(() => { const e = window.__e2e.first(`+sel(selector)+`); if (e) e.scrollIntoView({ block: 'nearest' }); return true; })()`, nil)
}

func (d *Tab) centre(selector string) (x, y float64) {
	d.t.Helper()
	d.WaitFor("a visible "+selector, `window.__e2e.centre(`+sel(selector)+`)`)
	var prev []float64
	for i := 0; i < 50; i++ {
		var c []float64
		d.Eval(`window.__e2e.centre(`+sel(selector)+`)`, &c)
		if len(c) == 2 && len(prev) == 2 && c[0] == prev[0] && c[1] == prev[1] {
			return c[0], c[1]
		}
		prev = c
		time.Sleep(30 * time.Millisecond)
	}
	d.t.Fatalf("%s is not on screen or keeps moving", selector)
	return
}

func (d *Tab) clickSel(selector string, clicks int) {
	d.t.Helper()
	x, y := d.centre(selector)
	d.MouseClick(x, y, clicks)
}

// At turns canvas (#stage) coordinates into page coordinates.
func (d *Tab) At(x, y float64) (float64, float64) {
	d.t.Helper()
	d.refreshBox()
	return d.box[0] + x, d.box[1] + y
}

func (d *Tab) refreshBox() {
	var b []float64
	d.Eval(`(() => { const c = document.getElementById('stage'); if (!c) return [0, 0, 0, 0]; const r = c.getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; })()`, &b)
	copy(d.box[:], b)
}

// CanvasClick clicks at canvas coordinates.
func (d *Tab) CanvasClick(x, y float64) { d.t.Helper(); px, py := d.At(x, y); d.MouseClick(px, py, 1) }

// CanvasDblClick double-clicks at canvas coordinates.
func (d *Tab) CanvasDblClick(x, y float64) {
	d.t.Helper()
	px, py := d.At(x, y)
	d.MouseClick(px, py, 2)
}

// CanvasMove moves the pointer to canvas coordinates.
func (d *Tab) CanvasMove(x, y float64) { d.t.Helper(); px, py := d.At(x, y); d.MouseMove(px, py) }

// CanvasDragPage presses at one page point and releases at another.
func (d *Tab) CanvasDragPage(x0, y0, x1, y1 float64) {
	d.t.Helper()
	d.MouseMove(x0, y0)
	d.mouse(input.MousePressed, x0, y0, input.Left, 1, 0, 0)
	d.mouse(input.MouseMoved, (x0+x1)/2, (y0+y1)/2, input.Left, 0, 0, 0)
	d.mouse(input.MouseMoved, x1, y1, input.Left, 0, 0, 0)
	d.mouse(input.MouseReleased, x1, y1, input.Left, 1, 0, 0)
}

// CanvasDrag presses at one point and releases at another.
func (d *Tab) CanvasDrag(x0, y0, x1, y1 float64) {
	d.t.Helper()
	ax, ay := d.At(x0, y0)
	bx, by := d.At(x1, y1)
	d.MouseMove(ax, ay)
	d.mouse(input.MousePressed, ax, ay, input.Left, 1, 0, 0)
	for _, f := range []float64{1.0 / 6, 2.0 / 6, 3.0 / 6, 4.0 / 6, 5.0 / 6, 1} {
		d.mouse(input.MouseMoved, ax+(bx-ax)*f, ay+(by-ay)*f, input.Left, 0, 0, 0)
	}
	d.mouse(input.MouseReleased, bx, by, input.Left, 1, 0, 0)
}

func (d *Tab) Pixel(x, y int) [4]int {
	d.t.Helper()
	var p []int
	d.Eval(fmt.Sprintf(`Array.from(document.getElementById('stage').getContext('2d').getImageData(%d, %d, 1, 1).data)`, x, y), &p)
	return [4]int{p[0], p[1], p[2], p[3]}
}

// ToggleMode clicks the page's #mode button.
func (d *Tab) ToggleMode() { d.t.Helper(); d.Click("#mode") }

func (d *Tab) key(typ input.KeyType, k keyDef, mods input.Modifier, text string) {
	d.t.Helper()
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		p := input.DispatchKeyEvent(typ).WithModifiers(mods).WithKey(k.key).WithCode(k.code).
			WithWindowsVirtualKeyCode(k.vk).WithNativeVirtualKeyCode(k.vk)
		if text != "" {
			p = p.WithText(text).WithUnmodifiedText(text)
		}
		return p.Do(ctx)
	}))
}

// Press presses a key or a chord: "Enter", "Control+Shift+c".
func (d *Tab) Press(spec string) {
	d.t.Helper()
	mods, name := splitKeys(spec)
	bits := d.mods // keys held with KeyDown count
	for _, m := range mods {
		bits |= modBits[m]
		d.key(input.KeyType("rawKeyDown"), named[m], bits, "")
	}
	k := keyOf(name)
	text := k.text
	if bits&(input.ModifierCtrl|input.ModifierAlt|input.ModifierMeta) != 0 {
		text = ""
	}
	if text != "" {
		d.key(input.KeyDown, k, bits, text)
	} else {
		d.key(input.KeyType("rawKeyDown"), k, bits, "")
	}
	d.key(input.KeyUp, k, bits, "")
	for i := len(mods) - 1; i >= 0; i-- {
		if d.mods&modBits[mods[i]] == 0 {
			bits &^= modBits[mods[i]]
		}
		d.key(input.KeyUp, named[mods[i]], bits, "")
	}
}

func (d *Tab) KeyDown(name string) {
	d.t.Helper()
	d.mods |= modBits[name]
	d.key(input.KeyType("rawKeyDown"), named[name], d.mods, "")
}

// KeyUp releases a key held with KeyDown.
func (d *Tab) KeyUp(name string) {
	d.t.Helper()
	d.key(input.KeyUp, named[name], d.mods, "")
	d.mods &^= modBits[name]
}

// Type types text, a key press for each character.
func (d *Tab) Type(text string) {
	d.t.Helper()
	for _, r := range text {
		s := string(r)
		switch r {
		case '\n':
			d.Press("Enter")
			continue
		case '\t':
			d.Press("Tab")
			continue
		}
		k := keyOf(s)
		d.key(input.KeyDown, k, d.mods, s)
		d.key(input.KeyUp, k, d.mods, "")
	}
}

// AnswerDialog accepts the next prompt or confirm with text.
func (d *Tab) AnswerDialog(text string) { d.mu.Lock(); d.dialog = &text; d.mu.Unlock() }

func (d *Tab) AnswerDialogs(texts ...string) {
	d.mu.Lock()
	d.dialogQ = append(d.dialogQ[:0:0], texts...)
	d.dialogSeen = nil
	d.mu.Unlock()
}

func (d *Tab) DialogsSeen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dialogSeen...)
}

func (d *Tab) Download(action func()) (name string, data []byte) {
	d.t.Helper()
	dir := d.t.TempDir()
	d.must(browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(dir).WithEventsEnabled(true))
	for len(d.dlName) > 0 {
		<-d.dlName
	}
	action()
	select {
	case name = <-d.dlName:
	case <-time.After(Timeout):
		d.t.Fatal("no download started")
	}
	path := filepath.Join(dir, name)
	for deadline := time.Now().Add(Timeout); ; {
		b, err := os.ReadFile(path)
		if _, partial := os.Stat(path + ".crdownload"); err == nil && partial != nil {
			return name, b
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("download %q did not finish", name)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (d *Tab) ChooseFile(action func(), name string, data []byte) {
	d.t.Helper()
	path := filepath.Join(d.t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		d.t.Fatal(err)
	}
	d.mu.Lock()
	d.files = []string{path}
	d.mu.Unlock()
	for len(d.chooser) > 0 {
		<-d.chooser
	}
	action()
	select {
	case <-d.chooser:
	case <-time.After(Timeout):
		d.t.Fatal("no file chooser opened")
	}
}

// GrantClipboard lets the page read and write the clipboard.
func (d *Tab) GrantClipboard() {
	d.t.Helper()
	for _, name := range []string{"clipboard-read", "clipboard-write"} {
		d.must(browser.SetPermission(&browser.PermissionDescriptor{Name: name}, browser.PermissionSettingGranted).WithOrigin(d.site.URL))
	}
}

func (d *Tab) Route(substr string, handle func() (status int, contentType string, body []byte)) (hits func() int) {
	d.t.Helper()
	r := &route{substr: substr, handle: handle}
	d.mu.Lock()
	d.routes = append(d.routes, r)
	d.mu.Unlock()
	var patterns []*fetch.RequestPattern
	d.mu.Lock()
	for _, c := range d.routes {
		patterns = append(patterns, &fetch.RequestPattern{URLPattern: "*" + c.substr + "*"})
	}
	d.mu.Unlock()
	d.must(fetch.Enable().WithPatterns(patterns))
	return func() int { d.mu.Lock(); defer d.mu.Unlock(); return r.hits }
}

func (d *Tab) fulfil(e *fetch.EventRequestPaused) {
	d.mu.Lock()
	var r *route
	for _, c := range d.routes {
		if strings.Contains(e.Request.URL, c.substr) {
			r = c
		}
	}
	preflight := e.Request.Method == "OPTIONS"
	if r != nil && !preflight {
		r.hits++
	}
	d.mu.Unlock()
	_ = d.run(chromedp.ActionFunc(func(ctx context.Context) error {
		if r == nil {
			return fetch.ContinueRequest(e.RequestID).Do(ctx)
		}
		if preflight {
			return fetch.FulfillRequest(e.RequestID, 204).WithResponseHeaders([]*fetch.HeaderEntry{
				{Name: "access-control-allow-origin", Value: "*"}, {Name: "access-control-allow-headers", Value: "*"}}).Do(ctx)
		}
		status, ctype, body := r.handle()
		return fetch.FulfillRequest(e.RequestID, int64(status)).WithResponseHeaders([]*fetch.HeaderEntry{
			{Name: "content-type", Value: ctype}, {Name: "access-control-allow-origin", Value: "*"},
		}).WithBody(b64(body)).Do(ctx)
	}))
}

// Reload reloads the page and waits for #state again.
func (d *Tab) Reload() {
	d.t.Helper()
	d.must(chromedp.Reload())
	d.WaitFor("the page to start", "document.getElementById('state') && document.getElementById('state').textContent !== '(pending)'")
}

func (d *Tab) Goto(s *Site, path string) {
	d.t.Helper()
	d.must(chromedp.Navigate(s.URL + path))
	d.WaitFor("the page to start", "document.getElementById('state') && document.getElementById('state').textContent !== '(pending)'")
}

// URL is the page's address.
func (d *Tab) URL() string { d.t.Helper(); return d.Str(`location.href`) }

// Screenshot saves the whole page as a PNG.
func (d *Tab) Screenshot(path string) {
	d.t.Helper()
	var buf []byte
	d.must(chromedp.FullScreenshot(&buf, 90))
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *Tab) ScreenshotView(path string) {
	d.t.Helper()
	var buf []byte
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatWebp).WithQuality(55).Do(ctx)
		return err
	}))
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func (d *Tab) ScreenshotOf(selector, path string) {
	d.t.Helper()
	var buf []byte
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		var r struct{ X, Y, W, H float64 }
		if err := chromedp.Evaluate(`(() => { const r = document.querySelector(`+jsString(selector)+`).getBoundingClientRect(); return {X: r.x + window.scrollX, Y: r.y + window.scrollY, W: r.width, H: r.height}; })()`, &r).Do(ctx); err != nil {
			return err
		}
		var err error
		buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatWebp).WithQuality(55).
			WithClip(&page.Viewport{X: r.X, Y: r.Y, Width: r.W, Height: r.H, Scale: 1}).Do(ctx)
		return err
	}))
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		d.t.Fatal(err)
	}
}

func jsString(s string) string { b, _ := json.Marshal(s); return string(b) }

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func (d *Tab) VirtualAuthenticator() string {
	d.t.Helper()
	var id webauthn.AuthenticatorID
	d.must(chromedp.Tasks{
		webauthn.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			id, err = webauthn.AddVirtualAuthenticator(&webauthn.VirtualAuthenticatorOptions{
				Protocol: webauthn.AuthenticatorProtocolCtap2, Transport: webauthn.AuthenticatorTransportInternal,
				HasResidentKey: true, HasPrf: true, HasUserVerification: true, IsUserVerified: true, AutomaticPresenceSimulation: true,
			}).Do(ctx)
			return err
		}),
	})
	return string(id)
}

// RemoveVirtualAuthenticator takes an authenticator out, with the passkeys it holds.
func (d *Tab) RemoveVirtualAuthenticator(id string) {
	d.t.Helper()
	d.must(webauthn.RemoveVirtualAuthenticator(webauthn.AuthenticatorID(id)))
}

func (d *Tab) ReloadPage() {
	d.t.Helper()
	d.must(chromedp.Reload())
}

func (d *Tab) OnNewDocument(script string) {
	d.t.Helper()
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
		return err
	}))
}

func (d *Tab) Context() context.Context { return d.ctx }

func (d *Tab) Offline(off bool) {
	d.t.Helper()
	d.must(chromedp.ActionFunc(func(ctx context.Context) error {
		if err := network.Enable().Do(ctx); err != nil {
			return err
		}
		return chromedp.FromContext(ctx).Target.Execute(ctx, "Network.emulateNetworkConditions",
			map[string]any{"offline": off, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil)
	}))
}
