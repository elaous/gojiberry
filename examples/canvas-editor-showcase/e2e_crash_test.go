//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// Crash codes are their own choice: with it off a crash leaves nothing behind; with it on, an error in the page is
// kept as two words, counted at the next visit, shown in the preview as exactly that, and nothing of the error's
// message or address goes with it. The usage counts stay off throughout.
func TestCrashCodesAreSeparateAndScrubbed(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.OK(true, "opened")
	d.Eval(`setTimeout(() => { throw new Error('secret document text at https://example.org/x?token=abc'); }, 0)`, nil)
	d.Sleep(300)
	d.OK(d.Str(`localStorage.getItem('showcase:crash-pending') || ''`) == "", "with no agreement a crash leaves nothing behind")

	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy-crash input")
	d.OK(!d.Bool(`document.querySelector('#privacy-crash input').checked`), "crash codes are off by default")
	d.Click("#privacy-crash input")
	d.WaitFor("agreed", `localStorage.getItem('showcase:crash') === 'on'`)
	d.OK(d.Str(`localStorage.getItem('showcase:telemetry') || ''`) == "", "the usage counts did not turn on with it")

	d.Eval(`setTimeout(() => { throw new Error('secret document text at https://example.org/x?token=abc'); }, 0)`, nil)
	d.WaitFor("kept as a code", `(localStorage.getItem('showcase:crash-pending') || '').includes('page_error')`)
	pending := d.Str(`localStorage.getItem('showcase:crash-pending')`)
	d.OK(!strings.Contains(pending, "secret") && !strings.Contains(pending, "example.org"), "what is kept holds no message or address: "+pending)

	d.ReloadPage()
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy-preview")
	d.WaitFor("the code is in the preview", `document.getElementById('privacy-preview').textContent.includes('page_error')`)
	preview := d.Text("#privacy-preview")
	d.OK(strings.Contains(preview, `"crash"`) && !strings.Contains(preview, "secret") && !strings.Contains(preview, "feature") && !strings.Contains(preview, "browser"), "the preview is the crash code alone: "+preview)
	d.OK(d.Str(`localStorage.getItem('showcase:crash-pending') || ''`) == "", "the kept codes are cleared once counted")

	d.Click("#privacy-crash input") // off: what was counted goes
	d.WaitFor("off", `localStorage.getItem('showcase:crash') !== 'on'`)
	d.WaitFor("the preview empties", `!document.getElementById('privacy-preview').textContent.includes('page_error')`)
	// (the page errors in this test are the ones it threw on purpose)
}
