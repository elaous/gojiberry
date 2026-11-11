//go:build js && wasm && (experimental || (exp_forms && exp_forms_submit))

package main

import (
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

func submitNodes(u *ui, fl *canvas.TextFlow, renderUI func()) []xango.Node {
	return []xango.Node{
		xango.El("label", nil, xango.Text(T("forms.submitTo", "Address for the answers of a web form (https or mailto)")),
			lineInput("forms-submit-to", T("forms.submitTo", "Address for the answers of a web form (https or mailto)"), u.forms.submitTo, "https://", func(v string) { u.forms.submitTo = strings.TrimSpace(v) })),
		btn("forms-webform", T("forms.webForm", "Download as a web form"), func() { u.downloadWebForm(fl, renderUI) }, fl == nil || len(fl.FormFields()) == 0),
	}
}

func (u *ui) downloadWebForm(fl *canvas.TextFlow, renderUI func()) {
	if fl == nil {
		return
	}
	data, err := export.HTML{SubmitTo: u.forms.submitTo, SubmitLabel: T("forms.send", "Send")}.Export(export.FromDocument(docfile.FromFlow(u.title, fl)))
	if err != nil {
		u.forms.msg = T("forms.webFormBad", "The address must start with https:// or mailto: and have no spaces.")
	} else {
		transfer.Download("form.html", "text/html", data)
		u.forms.msg = T("forms.webFormSaved", "The web form was saved.")
	}
	u.announce(u.forms.msg)
	renderUI()
}
