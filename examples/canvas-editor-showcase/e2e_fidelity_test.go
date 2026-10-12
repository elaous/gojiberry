//go:build !(js && wasm)

package main

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// The Files tab says, before anything is exported, what the chosen format leaves out of this document, and the
// list changes with the format. It is a status a screen reader announces, the list has a name, and it holds no
// accessibility violation.
func TestFidelityPanelListsWhatAFormatLeavesOut(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	importMarkdown(d, "Water is H~2~O and x^2^ is a square.\n\nEnd words.", "End words")
	d.OK(d.Has("#gj-fidelity-format") && d.Has("#gj-fidelity-summary"), "the panel is in the tab")
	d.OK(d.Attr("#gj-fidelity-summary", "role") == "status", "the summary is a live status")

	d.Select("#gj-fidelity-format", "pdf")
	d.WaitFor("the PDF's losses", `document.querySelectorAll('.gj-fidelity-item').length > 0`)
	d.OK(d.Has(`.gj-fidelity-item[data-feature="superscript"][data-treatment="dropped"]`), "superscript is left out of a PDF: "+d.Text("#gj-fidelity-list"))
	d.OK(d.Has(`.gj-fidelity-item[data-feature="subscript"][data-treatment="dropped"]`), "subscript is left out of a PDF")
	d.OK(strings.Contains(d.Text("#gj-fidelity-summary"), "PDF") && strings.Contains(d.Text("#gj-fidelity-summary"), "left out or simplified"), d.Text("#gj-fidelity-summary"))
	d.OK(d.Attr("#gj-fidelity-list", "aria-label") != "", "the list has a name")

	d.Select("#gj-fidelity-format", "text")
	d.WaitFor("plain text's losses", `!!document.querySelector('.gj-fidelity-item[data-feature="superscript"] .gj-fidelity-note')`)
	d.OK(strings.Contains(d.Text(`.gj-fidelity-item[data-feature="superscript"]`), "Only the plain text is kept."), d.Text("#gj-fidelity-list"))

	d.Select("#gj-fidelity-format", "docx")
	d.WaitFor("Word keeps both", `!document.querySelector('.gj-fidelity-item[data-feature="superscript"]')`)
	d.OK(!d.Has(".gj-fidelity-item"), "nothing else is lost in Word: "+d.Text(".gj-fidelity"))
	d.OK(strings.Contains(d.Text("#gj-fidelity-summary"), "nothing in this document is lost"), d.Text("#gj-fidelity-summary"))

	for _, v := range d.Axe(".gj-fidelity", axeDisabled...) {
		t.Errorf("the fidelity panel: %s", v)
	}

	// Exporting as the PDF says what was left out, next to the file's name.
	d.Select("#gj-fidelity-format", "pdf")
	name, _ := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="pdf"]`) })
	d.OK(strings.HasSuffix(name, ".pdf"), name)
	d.WaitFor("the message", `document.getElementById('transfer-message').textContent.includes('left out or simplified')`)
	d.NoErrors()
}

// A Word file with a comment and a header says, after it is imported, that neither was brought over.
func TestFidelityPanelSaysWhatAnImportLeftOut(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	const ns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	for name, body := range map[string]string{
		"word/document.xml": `<w:document ` + ns + `><w:body><w:p><w:r><w:t>Body words</w:t></w:r></w:p></w:body></w:document>`,
		"word/comments.xml": `<w:comments ` + ns + `><w:comment w:id="0"><w:p><w:r><w:t>Check</w:t></w:r></w:p></w:comment></w:comments>`,
		"word/header1.xml":  `<w:hdr ` + ns + `><w:p><w:r><w:t>Company</w:t></w:r></w:p></w:hdr>`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()

	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.Click(`button[data-tab="transfer"]`)
	d.OK(!d.Has("#gj-fidelity-import"), "no import yet, no import report")
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="docx"]`) }, "memo.docx", buf.Bytes())
	d.Wait("s.text.includes('Body words')")
	d.Click(`button[data-tab="transfer"]`)
	d.WaitSelector("#gj-fidelity-import")
	d.OK(strings.Contains(d.Text("#gj-fidelity-import"), "memo.docx") && strings.Contains(d.Text("#gj-fidelity-import"), "2 kinds of content"), d.Text("#gj-fidelity-import"))
	d.OK(d.Has(`#gj-fidelity-import-list .gj-fidelity-item[data-feature="comment"]`) && d.Has(`#gj-fidelity-import-list .gj-fidelity-item[data-feature="header-footer"]`), d.Text(".gj-fidelity"))
	for _, v := range d.Axe(".gj-fidelity", axeDisabled...) {
		t.Errorf("the fidelity panel after an import: %s", v)
	}
	d.NoErrors()
}
