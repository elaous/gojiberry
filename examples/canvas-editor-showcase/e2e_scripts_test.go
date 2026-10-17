//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Scripts: a script runs in a sandbox and reaches the editor only by the commands of the registry; the text of the
// document is for a script the person allowed to read it; the page's own network and storage are not there.
func TestScriptsRunInASandboxAndAskForWhatTheyMayHave(t *testing.T) {
	t.Setenv("GOJIBERRY_REQUIRE_BROWSER", "1")
	d := e2etest.NewSite(t).OpenURL(t, "/index.html?lang=en")
	d.WaitSelector("canvas")
	d.Click(`button[data-tab="scripts"]`)
	d.WaitSelector("#scripts-source")

	log := func() string { return d.Str(`Array.from(document.querySelectorAll('#scripts-log li')).map(l => l.textContent).join('|')`) }
	run := func(src string) {
		d.Fill("#scripts-source", src)
		d.Click("#scripts-run")
		d.WaitFor("the script finished", `document.getElementById('scripts-message') && document.getElementById('scripts-message').textContent.length > 0`)
	}

	// the sample: lists the registry and runs a command
	d.Click("#scripts-run")
	d.WaitFor("it finished", `(document.getElementById('scripts-message')||{}).textContent === 'The script finished.'`)
	d.OK(strings.Contains(log(), "commands, for example") && !strings.Contains(log(), "Error"), "it saw the registry and ran bold: "+log())

	// the text of the document is refused until allowed
	run(`try { await editor.text(); editor.log("read"); } catch (e) { editor.log("refused: " + e.message); }`)
	d.OK(strings.Contains(log(), "refused: not allowed"), "reading is refused: "+log())
	d.Click("#scripts-allow-read input")
	d.WaitSelector("#scripts-read-warning")
	run(`const t = await editor.text(); editor.log("length " + (t.length > 0));`)
	d.OK(log() == "length true", "and given when allowed: "+log())

	// a command outside the registry is refused, and the page's own world is not there
	run(`try { await editor.run("not-a-command"); } catch (e) { editor.log(e.message); }
editor.log(typeof fetch, typeof localStorage);
try { await fetch("/"); } catch (e) { editor.log("no network"); }`)
	d.OK(strings.HasPrefix(log(), "no such command|undefined undefined") && strings.HasSuffix(log(), "no network"), "the sandbox: "+log())
	d.OK(d.Str(`typeof window.__done`) == "undefined", "the sandbox is not the page")

	// a script that never ends is stopped by the person
	d.Fill("#scripts-source", `await new Promise(() => {});`)
	d.Click("#scripts-run")
	d.WaitFor("it runs", `!document.getElementById('scripts-stop').disabled`)
	d.Click("#scripts-stop")
	d.WaitFor("it is stopped", `(document.getElementById('scripts-message')||{}).textContent === 'The script was stopped.'`)
	d.OK(d.Int(`document.querySelectorAll('iframe[sandbox]').length`) == 0, "the sandbox is gone")
	d.NoErrors()
}
