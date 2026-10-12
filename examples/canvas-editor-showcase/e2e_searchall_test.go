//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// "Search all documents" (Find tab) finds a word in every saved document, opens a hit, and replaces across the
// other documents with a preview, a choice and an undo; the open document is left to the editor's own
// Replace all.
func TestSearchAndReplaceAcrossDocuments(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Fill(".gj-saver-name", "alpha")
	d.Click(".gj-saver-save")
	d.WaitSelector(`.gj-doclist-item:has(.gj-doc-name:text-is("alpha"))`)
	d.Fill(".gj-saver-name", "beta")
	d.Click(".gj-saver-save-new")
	d.WaitFor("two documents", "document.querySelectorAll('.gj-doclist-item').length === 2")
	open := d.Attr(".gj-doclist-current", "data-id")
	if open == "" {
		t.Fatal("no open document")
	}

	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("the hits", "document.querySelectorAll('#sa-hits .sa-hit').length === 2")
	d.OK(d.Count("#sa-hits .sa-hit") == 2, "both documents hold the word")
	d.OK(strings.Contains(strings.ToLower(d.Text("#sa-hits .sa-hit")), "canvas"), "a hit shows the words around the match: "+d.Text("#sa-hits .sa-hit"))
	d.Fill("#sa-query", "zzzzqx")
	d.Click("#sa-search")
	d.WaitSelector("#sa-none")

	// Replace in the other document only.
	d.Fill("#sa-query", "canvas")
	d.Fill("#sa-repl", "leaf")
	d.Click("#sa-preview")
	d.WaitSelector("#sa-changes")
	d.OK(d.Count("#sa-changes .sa-change") == 1, "the open document is left out of the preview")
	d.OK(strings.Contains(d.Text("#sa-changes .sa-after"), "leaf"), "the preview shows the new text: "+d.Text("#sa-changes .sa-after"))
	d.Click("#sa-apply")
	d.WaitSelector("#sa-undo")
	d.Fill("#sa-query", "leaf")
	d.Click("#sa-search")
	d.WaitFor("the replaced document", "document.querySelectorAll('#sa-hits .sa-hit').length === 1")
	d.OK(d.Attr("#sa-hits .sa-hit", "data-id") != open, "only the other document changed")

	// Undo puts the old text back.
	d.Click("#sa-undo")
	d.WaitFor("the undo", "!document.querySelector('#sa-undo')")
	d.Fill("#sa-query", "leaf")
	d.Click("#sa-search")
	d.WaitSelector("#sa-none")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("both again", "document.querySelectorAll('#sa-hits .sa-hit').length === 2")
	d.NoErrors()
}

// The encrypted browser store is searched on this device after it is unlocked: the hit comes from the
// decrypted text, and nothing but the page ever sees it.
func TestSearchAllDocumentsInTheEncryptedStore(t *testing.T) {
	site := e2etest.NewSite(t)
	site.UseLocalhost()
	d := site.Open(t, "")
	d.VirtualAuthenticator()
	turnOn(t, d, "")
	saveConfidential(d, "Secret plans")
	d.Click(`button[data-tab="find"]`)
	d.WaitSelector("#search-all")
	d.Fill("#sa-query", "canvas")
	d.Click("#sa-search")
	d.WaitFor("the hit", "document.querySelectorAll('#sa-hits .sa-hit').length === 1")
	d.OK(strings.Contains(strings.ToLower(d.Text("#sa-hits .sa-hit")), "canvas"), "the hit comes from the decrypted text: "+d.Text("#sa-hits .sa-hit"))
	d.NoErrors()
}
