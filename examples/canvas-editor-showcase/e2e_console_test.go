//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestConsoleTabRunsCommandsOnTheDocuments types commands in the Console tab: folders and moves by pattern,
// a file added and imported, an export that downloads, the Up key, a purge that asks Yes or No in the panel,
// and the Save tab's list follows.
func TestConsoleTabRunsCommandsOnTheDocuments(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Click(`button[data-tab="console"]`)
	d.WaitSelector(".gj-console-input")

	run := func(line string) {
		t.Helper()
		d.Fill(".gj-console-input", line)
		d.Focus(".gj-console-input")
		d.Press("Enter")
	}

	d.OK(d.Attr(".gj-console-log", "role") == "log" && d.Attr(".gj-console-log", "aria-live") == "polite", "the output is a live log")
	run("mkdir Reports")
	d.WaitFor("the folder made", `document.querySelector('.gj-console-log').textContent.includes('mkdir')`)
	d.OK(d.Value(".gj-console-input") == "", "the line is cleared after Enter")

	// A file added with the button, then imported by pattern.
	d.ChooseFile(func() { d.Click(".gj-console-addfiles") }, "letter.md", []byte("# Letter\n\nDear reader"))
	d.WaitFor("the inbox", `document.querySelector('.gj-console-inbox') && document.querySelector('.gj-console-inbox').textContent.includes('letter.md')`)
	run("import *.md")
	d.WaitFor("the import", `document.querySelector('.gj-console-log').textContent.includes('import letter.md')`)
	run("mv letter Reports/")
	run("ls Reports/*")
	d.WaitFor("the listing", `(() => { const l = document.querySelector('.gj-console-log').textContent; return l.lastIndexOf('letter') > l.lastIndexOf('ls Reports/*'); })()`)

	// The Up key brings the last command back.
	d.Focus(".gj-console-input")
	d.Press("ArrowUp")
	d.WaitFor("the last command", `document.querySelector('.gj-console-input').value === 'ls Reports/*'`)
	d.Press("ArrowUp")
	d.WaitFor("the one before", `document.querySelector('.gj-console-input').value === 'mv letter Reports/'`)
	d.Fill(".gj-console-input", "")

	// An export is downloaded.
	run("cd Reports")
	d.WaitFor("the prompt follows cd", `document.querySelector('.gj-console-prompt').textContent === '/Reports>'`)
	name, data := d.Download(func() { run("export -f markdown letter") })
	d.OK(strings.HasSuffix(name, ".md") && strings.Contains(string(data), "Dear reader"), "downloaded "+name+": "+string(data))

	// Purge asks in the panel: No leaves it, Yes deletes it.
	run("rm letter")
	d.WaitFor("in the trash", `document.querySelector('.gj-console-log').textContent.includes('trash letter')`)
	run("purge letter")
	d.WaitSelector(".gj-console-yes")
	d.OK(strings.Contains(d.Text(".gj-console-question"), "cannot be undone"), "the question: "+d.Text(".gj-console-question"))
	d.Click(".gj-console-no")
	d.WaitFor("left alone", `document.querySelector('.gj-console-log').textContent.includes('left alone')`)
	d.OK(!d.Has(".gj-console-question"), "the question is gone")
	run("trash")
	d.WaitFor("still in the trash", `(() => { const l = document.querySelector('.gj-console-log').textContent; return l.lastIndexOf('letter') > l.lastIndexOf('> trash'); })()`)
	run("purge letter")
	d.WaitSelector(".gj-console-yes")
	d.Click(".gj-console-yes")
	d.WaitFor("purged", `document.querySelector('.gj-console-log').textContent.includes('purge')`)

	// An error is a line of its own and the console goes on.
	run("nosuchcommand")
	d.WaitFor("the error", `document.querySelector('.gj-console-err') && document.querySelector('.gj-console-err').textContent.includes('unknown command')`)

	// The Save tab's list follows what the console did.
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the folder", `Array.from(document.querySelectorAll('.gj-doclist-folder .gj-doc-name')).some((e) => e.textContent.includes('Reports'))`)
	d.NoErrors()
}
