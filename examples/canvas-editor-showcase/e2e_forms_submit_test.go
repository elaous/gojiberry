//go:build !(js && wasm) && (experimental || (exp_forms && exp_forms_submit))

package main

import (
	"strings"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// submitSteps: naming an address and saving the page as a web form that sends the answers there (exp_forms_submit).
func submitSteps(d *e2etest.Tab) {
	d.Fill("#forms-submit-to", "http://example.org")
	d.Click("#forms-webform")
	d.WaitFor("a wrong address is refused", `document.getElementById('forms-message').textContent.includes('https://')`)
	d.Fill("#forms-submit-to", "https://example.org/in")
	_, page := d.Download(func() { d.Click("#forms-webform") })
	d.OK(strings.Contains(string(page), `<form action="https://example.org/in" method="post">`) && strings.Contains(string(page), `<button type="submit">Send</button>`), "the web form: "+string(page))
}
