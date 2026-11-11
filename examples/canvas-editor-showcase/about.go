//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/xango"
)

const (
	projectRepo     = "https://github.com/elaous/gojiberry"
	projectLicence  = projectRepo + "/blob/main/LICENSE"
	projectNotice   = projectRepo + "/blob/main/NOTICE"
	projectCommerce = projectRepo + "/blob/main/COMMERCIAL.md"
)

func aboutLink(id, href, text string) xango.Node {
	return xango.El("li", nil, xango.El("a", []xango.Attr{xango.A("id", id), xango.A("href", href), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer")}, xango.Text(text)))
}

func aboutPanel() xango.Node {
	return xango.Frag(
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("about.title", "About Gojiberry"))),
		para("about-what", T("about.what", "Gojiberry is a document editor that runs in your browser. Your documents stay on this device unless you choose to share them."), false),
		para("about-licence", T("about.licence", "It is free software under the GNU Affero General Public License, version 3. If you run a changed version for other people over a network, you must offer them its source. A commercial licence is available for use the AGPL does not fit."), false),
		para("about-credit", T("about.credit", "Made with the Gojiberry project, github.com/elaous/gojiberry. Keep this credit in copies and in changed versions."), false),
		xango.El("ul", []xango.Attr{xango.A("id", "about-links"), xango.A("style", "font-size:13px")},
			aboutLink("about-source", projectRepo, T("about.source", "Source code")),
			aboutLink("about-licence-text", projectLicence, T("about.licenceText", "Licence (AGPL-3.0)")),
			aboutLink("about-notice", projectNotice, T("about.notice", "Notice and credit")),
			aboutLink("about-commercial", projectCommerce, T("about.commercial", "Commercial licence")),
		),
	)
}
