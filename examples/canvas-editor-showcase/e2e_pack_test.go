//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func TestPackDownloadAndImport(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-doclist-newfolder-name", "Work")
	d.WaitFor("the create button", `!document.querySelector('.gj-doclist-newfolder').disabled`)
	d.Click(".gj-doclist-newfolder")
	d.WaitFor("the folder", "document.querySelectorAll('.gj-doclist-folder').length === 1")
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside the folder", "document.querySelector('.gj-breadcrumb-current')")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitFor("the document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")

	name, pack := d.Download(func() { d.Click(".gj-doclist-folder-pack") })
	d.OK(name == "Work.opendoc" && strings.HasPrefix(string(pack), "OPENDOC\x00"), "a pack of the folder: "+name)

	d.Click(".gj-doclist-folder-open")
	d.WaitFor("inside", "document.querySelector('.gj-doclist-item')")
	d.Click(".gj-doclist-trash")
	d.WaitFor("trashed", "document.querySelectorAll('.gj-doclist-item').length === 0")
	d.Click(".gj-trash-delete")
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`)
	d.WaitFor("the trash empty", "document.querySelectorAll('.gj-trash-item').length === 0")
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	d.Click(".gj-doclist-folder-delete")
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`)
	d.WaitFor("no folder", "document.querySelectorAll('.gj-doclist-folder').length === 0")

	d.ChooseFile(func() { d.Click(".gj-doclist-import-pack") }, "Work.opendoc", pack)
	d.WaitFor("the status line", "((document.querySelector('.gj-saver-status') || {}).textContent || '').includes('1 documents and 1 folders imported')")
	d.WaitFor("the folder is back", "document.querySelectorAll('.gj-doclist-folder').length === 1")
	d.Click(".gj-doclist-folder-open")
	d.WaitFor("the document is back", `Array.from(document.querySelectorAll('.gj-doclist-item .gj-doc-name')).some((e) => e.textContent === 'plan')`)
	d.NoErrors()
}
