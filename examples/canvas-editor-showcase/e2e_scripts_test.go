//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestScriptsRunInASandboxAndAskForWhatTheyMayHave(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Click(`button[data-tab="scripts"]`)
	d.WaitSelector("#scripts-source")

	log := func() string {
		return d.Str(`Array.from(document.querySelectorAll('#scripts-log li')).map(l => l.textContent).join('|')`)
	}
	run := func(src string) {
		d.Fill("#scripts-source", src)
		d.Click("#scripts-run")
		d.WaitFor("the script finished", `document.getElementById('scripts-message') && document.getElementById('scripts-message').textContent.length > 0`)
	}

	d.Click("#scripts-run")
	d.WaitFor("it finished", `(document.getElementById('scripts-message')||{}).textContent === 'The script finished.'`)
	d.OK(strings.Contains(log(), "commands, for example") && !strings.Contains(log(), "Error"), "it saw the registry and ran bold: "+log())

	run(`try { await editor.text(); editor.log("read"); } catch (e) { editor.log("refused: " + e.message); }`)
	d.OK(strings.Contains(log(), "refused: not allowed"), "reading is refused: "+log())
	d.Click("#scripts-allow-read input")
	d.WaitSelector("#scripts-read-warning")
	run(`const t = await editor.text(); editor.log("length " + (t.length > 0));`)
	d.OK(log() == "length true", "and given when allowed: "+log())

	run(`try { await editor.insert("X"); } catch (e) { editor.log("refused: " + e.message); }`)
	d.OK(strings.Contains(log(), "refused: not allowed"), "writing is refused: "+log())
	d.Click("#scripts-allow-write input")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	run(`editor.log(String(await editor.insert("ZZ")));`)
	d.OK(log() == "true", "the text went in: "+log())
	run(`editor.log(String((await editor.text()).includes("ZZ")));`)
	d.OK(log() == "true", "and is in the document: "+log())

	run(`try { await editor.run("not-a-command"); } catch (e) { editor.log(e.message); }
editor.log(typeof fetch, typeof localStorage);
try { await fetch("/"); } catch (e) { editor.log("no network"); }`)
	d.OK(strings.HasPrefix(log(), "no such command|undefined undefined") && strings.HasSuffix(log(), "no network"), "the sandbox: "+log())
	d.OK(d.Str(`typeof window.__done`) == "undefined", "the sandbox is not the page")

	d.Fill("#scripts-source", `await new Promise(() => {});`)
	d.Click("#scripts-run")
	d.WaitFor("it runs", `!document.getElementById('scripts-stop').disabled`)
	d.Click("#scripts-stop")
	d.WaitFor("it is stopped", `(document.getElementById('scripts-message')||{}).textContent === 'The script was stopped.'`)
	d.OK(d.Int(`document.querySelectorAll('iframe[sandbox]').length`) == 0, "the sandbox is gone")
	d.NoErrors()
}

func TestScriptsGetAndSetTheDocument(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="scripts"]`)
	d.WaitSelector("#scripts-source")
	log := func() string {
		return d.Str(`Array.from(document.querySelectorAll('#scripts-log li')).map(l => l.textContent).join('|')`)
	}
	run := func(src string) {
		d.Fill("#scripts-source", src)
		d.Click("#scripts-run")
		d.WaitFor("the script finished", `(document.getElementById('scripts-message')||{}).textContent === 'The script finished.'`)
	}
	run(`for (const f of [() => editor.meta(), () => editor.setMeta({title: "x"})]) { try { await f(); editor.log("done"); } catch (e) { editor.log("refused"); } }`)
	d.OK(log() == "refused|refused", "refused until allowed: "+log())
	d.Click("#scripts-allow-read input")
	d.Click("#scripts-allow-change input")
	run(`const m = await editor.meta(); editor.log(m.title);
await editor.setMeta({title: "Scripted", language: "fr"});
const m2 = await editor.meta(); editor.log(m2.title + " " + m2.language);
try { await editor.setMeta({language: "not a tag"}); } catch (e) { editor.log("refused: " + e.message); }
try { await editor.setFormat({color: "red; background:url(x)"}); } catch (e) { editor.log("refused: " + e.message); }
editor.log((await editor.styles()).includes("Heading 1"));`)
	d.OK(strings.Contains(log(), "Scripted fr") && strings.Count(log(), "refused:") == 2 && strings.HasSuffix(log(), "true"), "meta and checks: "+log())
	d.WaitFor("the title is set", `JSON.parse(document.getElementById("state").textContent).title === "Scripted"`)

	run(`await editor.setMarkdown("# Heading\n\nSome **bold** words here.\n");
const md = await editor.markdown();
editor.log(md.includes("Heading") && md.includes("bold"));
await editor.setSelection(0, 7);
const sel = await editor.selection();
editor.log(sel.text);
await editor.setFormat({italic: true, color: "#c01c28"});
const f = await editor.format();
editor.log(f.italic + " " + f.color);
await editor.setStyle("Heading 2");
editor.log(await editor.style());`)
	d.OK(log() == "true|Heading|true #c01c28|Heading 2", "Markdown, selection, format and style: "+log())
	d.NoErrors()
}
