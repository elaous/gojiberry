//go:build !(js && wasm) && !experimental && exp_forms && !exp_forms_submit

package main

import "github.com/elaous/gojiberry/internal/e2etest"

func submitSteps(d *e2etest.Tab) {
	d.OK(!d.Has("#forms-submit-to") && !d.Has("#forms-webform"), "no address box and no web form without exp_forms_submit")
}
