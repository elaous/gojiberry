//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestFoldersAndBreadcrumb drives folders in the Save tab: a folder is made and
// opened, the breadcrumb shows where you are and takes you back, a document saved
// there is in it, another can be moved in, and a folder with something in it
// cannot be deleted.
func TestFoldersAndBreadcrumb(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	confirm := func() { d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`) }
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "top doc")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved document", "document.querySelectorAll('.gj-doclist-item').length === 1")

	d.Fill(".gj-doclist-newfolder-name", "Work")
	d.WaitFor("the create button", `!document.querySelector('.gj-doclist-newfolder').disabled`)
	d.Click(".gj-doclist-newfolder")
	d.WaitFor("the folder", "document.querySelectorAll('.gj-doclist-folder').length === 1")
	d.OK(d.Count(".gj-doclist-item") == 1 && d.Count(".gj-doclist-folder") == 1, "the top level shows its document and the new folder")
	d.OK(d.Text(".gj-doclist-folder .gj-doc-name") == "📁 Work", "the folder is listed")

	d.Click(".gj-doclist-folder .gj-doclist-folder-open")
	d.WaitFor("the folder to open", `document.querySelector('.gj-breadcrumb-current') && document.querySelector('.gj-breadcrumb-current').textContent === 'Work'`)
	d.OK(d.Count(".gj-doclist-item") == 0, "the folder is empty: the top-level document is not shown in it")
	d.OK(d.Text(".gj-breadcrumb-link") == "Documents", "the breadcrumb leads back to the top")

	// A document saved here belongs here.
	d.Fill(".gj-saver-name", "inside")
	d.Click(".gj-saver-save-new")
	d.WaitFor("the document in the folder", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.OK(d.Text(".gj-doclist-item .gj-doc-name") == "inside", "a document saved in a folder is in it")
	d.Fill(".gj-doclist-newfolder-name", "2026")
	d.WaitFor("the create button", `!document.querySelector('.gj-doclist-newfolder').disabled`)
	d.Click(".gj-doclist-newfolder")
	d.WaitFor("the subfolder", "document.querySelectorAll('.gj-doclist-folder').length === 1")

	// Back to the top, and the other document moves in from there.
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	d.OK(d.Count(".gj-doclist-item") == 1 && d.Text(".gj-doclist-item .gj-doc-name") == "top doc", "the top level has its own document")
	d.Select(".gj-doclist-item .gj-doclist-move", "folder:"+d.Attr(".gj-doclist-folder", "data-id"))
	d.WaitFor("the move", "document.querySelectorAll('.gj-doclist-item').length === 0")
	d.OK(d.Count(".gj-doclist-item") == 0, "a document moved into a folder leaves the top level")

	// A folder with something in it cannot be deleted; the error shows.
	d.Click(".gj-doclist-folder .gj-doclist-folder-delete")
	confirm()
	d.WaitSelector(".gj-doc-error")
	d.OK(d.Count(".gj-doclist-folder") == 1, "a folder with documents in it stays: "+d.Text(".gj-doc-error"))

	// Both documents are in Work now; opening one from the top level shows its folder.
	d.Click(".gj-doclist-folder .gj-doclist-folder-open")
	d.WaitFor("the folder", `document.querySelector('.gj-breadcrumb-current')`)
	d.OK(d.Count(".gj-doclist-item") == 2, "both documents are in Work now")
	d.Click(".gj-breadcrumb-link")
	d.WaitFor("the top level", "!document.querySelector('.gj-breadcrumb-link')")
	d.Click(".gj-doclist-folder .gj-doclist-folder-open")
	d.WaitFor("the folder", `document.querySelector('.gj-breadcrumb-current')`)
	d.Click(`.gj-doclist-item:has(.gj-doc-name:text-is("top doc")) .gj-doclist-open`)
	d.WaitFor("the document to open", "document.querySelector('.gj-doclist-current')")
	d.OK(d.Text(".gj-breadcrumb-current") == "Work" && d.Text(".gj-doclist-current .gj-doc-name") == "top doc", "an open document is marked in its folder")
	d.NoErrors()
}

// TestLinkAndDownload: a document's Link is its /d/{id} address, a folder's is
// /f/{id}; a document downloads in a chosen format and a folder as a ZIP.
func TestLinkAndDownload(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "plan")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved document", "document.querySelectorAll('.gj-doclist-item').length === 1")
	id := d.Attr(".gj-doclist-item", "data-id")
	link := d.Attr(".gj-doclist-item .gj-doclist-link", "data-url")
	d.OK(strings.HasSuffix(link, "/d/"+id), "a document's link is its /d/{id} address: "+link)

	name, data := d.Download(func() { d.Select(".gj-doclist-item .gj-doclist-download", "format:markdown") })
	d.OK(strings.HasSuffix(name, ".md") && len(data) > 0, "a document downloads in the chosen format: "+name)
	d.WaitFor("the status line", "(document.querySelector('.gj-saver-status') || {}).textContent === 'plan downloaded'")

	// The formats are under headings, and opendocs' own file carries the history.
	groups := d.Strs(`Array.from(document.querySelectorAll('.gj-doclist-item .gj-doclist-download optgroup')).map((g) => g.label)`)
	d.OK(strings.Join(groups, ",") == "Documents,Web and text,Data,opendocs", "the formats are grouped: "+strings.Join(groups, ","))
	d.Click(".gj-versions-save")
	d.WaitFor("a version", "document.querySelector('.gj-versions-item')")
	oname, odata := d.Download(func() { d.Select(".gj-doclist-item .gj-doclist-download", "format:open") })
	d.OK(strings.HasSuffix(oname, ".open") && len(odata) > 0, "the .open download: "+oname)
	d.OK(strings.Contains(string(odata), "plan"), "it holds the document")

	d.Fill(".gj-doclist-newfolder-name", "Work")
	d.WaitFor("the create button", `!document.querySelector('.gj-doclist-newfolder').disabled`)
	d.Click(".gj-doclist-newfolder")
	d.WaitFor("the folder", "document.querySelectorAll('.gj-doclist-folder').length === 1")
	fid := d.Attr(".gj-doclist-folder", "data-id")
	d.OK(strings.HasSuffix(d.Attr(".gj-doclist-folder .gj-doclist-link", "data-url"), "/f/"+fid), "a folder's link is its /f/{id} address")
	zname, zdata := d.Download(func() { d.Click(".gj-doclist-folder-download") })
	d.OK(zname == "Work.zip" && len(zdata) >= 4 && string(zdata[:2]) == "PK", "a folder downloads as a ZIP: "+zname)
}
