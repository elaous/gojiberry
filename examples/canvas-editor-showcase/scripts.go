//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/script"
	"github.com/elaous/gojiberry/xango"
)

// The Scripts tab: a script a person wrote or pasted runs in a sandbox (an iframe with an opaque origin, a policy that
// allows no connection and no storage, and none of the page) and reaches the editor only through the requests of package
// script: the commands of the registry, and, if allowed, the text of the document. docs/scripting.md says what is
// guaranteed and what is not.

const scriptTimeoutMs = 10_000

const scriptSample = `// Every command of the editor can be run by name.
const commands = await editor.commands();
editor.log(commands.length + " commands, for example " + commands.slice(0, 3).map(c => c.name).join(", "));
await editor.run("bold");
`

var scriptsUI struct {
	source      string
	started     bool // source was set
	noRun       bool // the person turned "run commands" off
	allowRead   bool
	allowWrite  bool
	allowChange bool // may change the document by the get/set requests (format, style, descriptions, title, Markdown)
	running     bool
	log         []string
	msg         string
	frame       js.Value
	sess        *script.Session
	listener    js.Func
	listening   bool
	timer       js.Value
	renderUI    func()
}

type scriptHost struct {
	script.Document // the get/set requests
	commands        func() []canvas.CommandInfo
	run             func(canvas.Command)
	text            func() string
	insert          func(string) bool
}

func (h scriptHost) Commands() []script.Command {
	var out []script.Command
	for _, c := range h.commands() {
		out = append(out, script.Command{Name: string(c.Name), Title: c.Title})
	}
	return out
}
func (h scriptHost) Run(name string) bool { h.run(canvas.Command(name)); return true }
func (h scriptHost) Text() string         { return h.text() }
func (h scriptHost) Insert(t string) bool { return h.insert(t) }

// sandboxPage is the page of the iframe: a policy that allows nothing but its own inline script, the editor object, and
// the person's script after it. The connections a script could make (fetch, sockets, workers, storage, peer
// connections) are also removed, as a second line behind the policy.
func sandboxPage(source string) string {
	source = strings.ReplaceAll(source, "</script", `<\/script`)
	source = strings.ReplaceAll(source, "<!--", `<\!--`)
	return `<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'">` +
		`<script>(()=>{"use strict";const pending=new Map();let n=0;
const send=o=>new Promise((res,rej)=>{const id=++n;pending.set(id,{res,rej});parent.postMessage(JSON.stringify(Object.assign({id},o)),"*")});
addEventListener("message",e=>{if(e.source!==parent)return;let r;try{r=JSON.parse(e.data)}catch(x){return}const p=pending.get(r.id);if(!p)return;pending.delete(r.id);r.ok?p.res(r.value):p.rej(new Error(r.error))});
for(const k of ["fetch","XMLHttpRequest","WebSocket","EventSource","Worker","SharedWorker","RTCPeerConnection","webkitRTCPeerConnection","indexedDB","caches","localStorage","sessionStorage","navigator"]){try{Object.defineProperty(window,k,{value:undefined})}catch(x){}}
window.editor=Object.freeze({commands:()=>send({op:"commands"}),run:name=>send({op:"run",name}),text:()=>send({op:"text"}),insert:text=>send({op:"insert",text:String(text)}),log:(...a)=>send({op:"log",message:a.join(" ")}),
markdown:()=>send({op:"markdown"}),setMarkdown:text=>send({op:"setMarkdown",text:String(text)}),selection:()=>send({op:"selection"}),setSelection:(from,to)=>send({op:"setSelection",data:{from,to}}),
format:()=>send({op:"format"}),setFormat:f=>send({op:"setFormat",data:f}),styles:()=>send({op:"styles"}),style:()=>send({op:"style"}),setStyle:name=>send({op:"setStyle",name:String(name)}),
images:()=>send({op:"images"}),setImage:(index,d)=>send({op:"setImage",index,data:d}),meta:()=>send({op:"meta"}),setMeta:d=>send({op:"setMeta",data:d})});
window.__done=()=>send({op:"done"});
window.addEventListener("error",e=>{send({op:"log",message:"Error: "+e.message}).then(window.__done)});
})()</script>` +
		`<script>(async()=>{try{` + source + `
}catch(e){editor.log("Error: "+(e&&e.message||e))}})().then(()=>window.__done())</script>`
}

func stopScript(reason string) {
	if !scriptsUI.running {
		return
	}
	scriptsUI.running = false
	if scriptsUI.frame.Truthy() {
		scriptsUI.frame.Call("remove")
	}
	scriptsUI.frame, scriptsUI.sess = js.Undefined(), nil
	if scriptsUI.timer.Truthy() {
		js.Global().Call("clearTimeout", scriptsUI.timer)
	}
	scriptsUI.msg = reason
	if scriptsUI.renderUI != nil {
		scriptsUI.renderUI()
	}
}

// runScript starts the script in a new sandbox.
func runScript(docAPI script.Document, commands func() []canvas.CommandInfo, run func(canvas.Command), text func() string, insert func(string) bool, renderUI func()) {
	stopScript("")
	scriptsUI.renderUI = renderUI
	scriptsUI.log, scriptsUI.msg, scriptsUI.running = nil, "", true
	scriptsUI.sess = &script.Session{
		Host:  scriptHost{docAPI, commands, run, text, insert},
		Allow: map[script.Permission]bool{script.Run: !scriptsUI.noRun, script.Read: scriptsUI.allowRead, script.Insert: scriptsUI.allowWrite, script.Write: scriptsUI.allowChange},
		Log:   func(l string) { scriptsUI.log = append(scriptsUI.log, l); renderUI() },
		Done:  func() { go stopScript(T("scripts.finished", "The script finished.")) },
	}
	if !scriptsUI.listening {
		scriptsUI.listening = true
		scriptsUI.listener = js.FuncOf(func(_ js.Value, args []js.Value) any {
			ev := args[0]
			if scriptsUI.sess == nil || !scriptsUI.frame.Truthy() || !ev.Get("source").Equal(scriptsUI.frame.Get("contentWindow")) || ev.Get("data").Type() != js.TypeString {
				return nil
			}
			resp := scriptsUI.sess.Handle([]byte(ev.Get("data").String()))
			scriptsUI.frame.Get("contentWindow").Call("postMessage", string(resp), "*")
			if scriptsUI.sess.Stopped() {
				go stopScript(T("scripts.tooMany", "The script made too many requests and was stopped."))
			}
			return nil
		})
		js.Global().Call("addEventListener", "message", scriptsUI.listener)
	}
	doc := js.Global().Get("document")
	frame := doc.Call("createElement", "iframe")
	frame.Call("setAttribute", "sandbox", "allow-scripts") // no allow-same-origin: an opaque origin
	frame.Call("setAttribute", "aria-hidden", "true")
	frame.Call("setAttribute", "tabindex", "-1")
	frame.Call("setAttribute", "style", "display:none")
	frame.Set("srcdoc", sandboxPage(scriptsUI.source))
	scriptsUI.frame = frame
	doc.Get("body").Call("appendChild", frame)
	scriptsUI.timer = js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
		go stopScript(T("scripts.timeout", "The script took too long and was stopped."))
		return nil
	}), scriptTimeoutMs)
	renderUI()
}

// scriptsPanel is the Scripts tab.
func scriptsPanel(state *State, doc script.Document, commands func() []canvas.CommandInfo, run func(canvas.Command), text func() string, insert func(string) bool, renderUI func()) xango.Node {
	if !scriptsUI.started {
		scriptsUI.started, scriptsUI.source = true, scriptSample
	}
	scriptsUI.renderUI = renderUI
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("scripts.title", "Scripts"))),
		para("scripts-about", T("scripts.about", "A script runs in a sandbox that has nothing of this page: no network, no storage, no document. It can only ask for the editor's commands by name, and, if you allow it, for the text of the document. Run only scripts you wrote or trust."), false),
		textarea("scripts-source", T("scripts.source", "Script"), scriptsUI.source, "", "10", func(v string) { scriptsUI.source = v }),
		xango.El("div", []xango.Attr{xango.A("id", "scripts-allow-run")}, components.Checkbox(components.CheckboxProps{Label: T("scripts.allowRun", "May run the editor's commands"), Checked: !scriptsUI.noRun,
			OnChange: func(on bool) { scriptsUI.noRun = !on; renderUI() }})),
		xango.El("div", []xango.Attr{xango.A("id", "scripts-allow-read")}, components.Checkbox(components.CheckboxProps{Label: T("scripts.allowRead", "May read the text of the document"), Checked: scriptsUI.allowRead,
			OnChange: func(on bool) { scriptsUI.allowRead = on; renderUI() }})),
		xango.El("div", []xango.Attr{xango.A("id", "scripts-allow-write")}, components.Checkbox(components.CheckboxProps{Label: T("scripts.allowWrite", "May write text into the document, at the caret"), Checked: scriptsUI.allowWrite,
			OnChange: func(on bool) { scriptsUI.allowWrite = on; renderUI() }})),
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "scripts-allow-change")}, components.Checkbox(components.CheckboxProps{Label: T("scripts.allowChange", "May change the document: formatting, styles, picture descriptions, title, language, or all of it from Markdown"), Checked: scriptsUI.allowChange,
		OnChange: func(on bool) { scriptsUI.allowChange = on; renderUI() }})))
	if scriptsUI.allowRead {
		nodes = append(nodes, para("scripts-read-warning", T("scripts.readWarning", "A script that may read the text can show it to whoever wrote the script, for example in a line it asks you to copy. Allow it only for a script you trust."), true))
	}
	if state.Mode != canvas.ModeEdit {
		nodes = append(nodes, para("scripts-needs-edit", T("scripts.needEdit", "Switch to edit mode: commands that change the document do nothing otherwise."), false))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")},
		btn("scripts-run", T("scripts.run", "Run the script"), func() { runScript(doc, commands, run, text, insert, renderUI) }, scriptsUI.running || strings.TrimSpace(scriptsUI.source) == ""),
		btn("scripts-stop", T("scripts.stop", "Stop"), func() { stopScript(T("scripts.stopped", "The script was stopped.")) }, !scriptsUI.running),
	))
	if scriptsUI.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "scripts-message"), xango.A("role", "status")}, xango.Text(scriptsUI.msg)))
	}
	var lines []xango.Node
	for _, l := range scriptsUI.log {
		lines = append(lines, xango.El("li", nil, xango.Text(l)))
	}
	nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "scripts-log"), xango.A("role", "log"), xango.A("aria-label", T("scripts.log", "What the script said")),
		xango.A("style", "font-family:monospace;font-size:12px;padding-left:1.2em")}, lines...))
	return xango.El("div", []xango.Attr{xango.A("id", "scripts-panel")}, nodes...)
}

// insertAtCaret puts text where the caret is, replacing the selection, as one change the undo history has.
func insertAtCaret(stage *canvas.Stage, text string, redraw func()) bool {
	t := stage.EditTarget()
	if t == nil || text == "" {
		return false
	}
	cs := t.Cursors()
	if len(cs) == 0 {
		return false
	}
	from, to := cs[0].Range()
	t.Replace([]canvas.Edit{{From: from, To: to, Items: []canvas.Item{canvas.Chars{Text: text}}}})
	end := from + len([]rune(text))
	t.SetCursors([]canvas.Cursor{{Anchor: end, Caret: end}})
	redraw()
	return true
}
