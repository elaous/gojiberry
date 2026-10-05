//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/internal/e2etest"
)

func wordBytes(t *testing.T, text string) []byte {
	t.Helper()
	data, err := export.DOCX{}.Export(export.FromDocument(docfile.Document{Title: text, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestMigrateFromGoogleDocsAndWordFiles migrates from a fake Google Drive (folders
// kept, a bad token refused) and from a Word file picked on the computer; the
// documents land in the Save tab's list.
func TestMigrateFromGoogleDocsAndWordFiles(t *testing.T) {
	doc := wordBytes(t, "migrated text")
	drive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "authorization")
		if r.Method == http.MethodOptions {
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/drive/v3/files" && strings.HasPrefix(r.URL.Query().Get("q"), "'root'"):
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{
				{"id": "f1", "name": "Work", "mimeType": "application/vnd.google-apps.folder"},
				{"id": "d1", "name": "Plan", "mimeType": "application/vnd.google-apps.document"},
			}})
		case r.URL.Path == "/drive/v3/files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"id": "d2", "name": "Budget", "mimeType": "application/vnd.google-apps.document"}}})
		case strings.HasSuffix(r.URL.Path, "/export"):
			_, _ = w.Write(doc)
		default:
			http.NotFound(w, r)
		}
	}))
	defer drive.Close()

	d := e2etest.NewSite(t).Open(t, "?driveapi="+drive.URL)
	d.Click(`button[data-tab="save"]`)
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Click(`button[data-tab="migrate"]`)
	d.Select(".gj-migrate-source", "google")
	d.Fill(".gj-migrate-token", "wrong")
	d.Click(".gj-migrate-connect")
	d.WaitFor("the refusal", `(document.querySelector('.gj-doc-error') || {textContent: ''}).textContent.includes('401')`)
	d.Fill(".gj-migrate-token", "secret")
	d.Click(".gj-migrate-connect")
	d.WaitFor("the listing", "document.querySelectorAll('.gj-migrate-item').length === 2")
	d.OK(d.Count(".gj-migrate-folder") == 1, "the folder is listed as a folder")
	d.Click(".gj-migrate-all")
	d.Click(".gj-migrate-start")
	d.WaitFor("the report", `(document.querySelector('.gj-migrate-report') || {textContent: ''}).textContent === '2 documents migrated'`)

	d.Click(`button[data-tab="save"]`)
	d.WaitFor("the folder and the document", "document.querySelectorAll('.gj-doclist-item').length === 1 && document.querySelectorAll('.gj-doclist-folder').length === 1")
	d.OK(d.Text(".gj-doclist-item .gj-doc-name") == "Plan", "Plan came in at the top")
	d.Click(".gj-doclist-folder .gj-doclist-folder-open")
	d.WaitFor("Work to open", `(document.querySelector('.gj-breadcrumb-current') || {textContent: ''}).textContent === 'Work'`)
	d.WaitFor("Budget", "document.querySelectorAll('.gj-doclist-item').length === 1")
	d.OK(d.Text(".gj-doclist-item .gj-doc-name") == "Budget", "Budget came in under Work, where it was")

	// A Word file from this computer.
	d.Click(`button[data-tab="migrate"]`)
	d.ChooseFile(func() { d.Click(".gj-migrate-pick") }, "Letter.docx", wordBytes(t, "dear reader"))
	d.WaitFor("the report", `(document.querySelector('.gj-migrate-report') || {textContent: ''}).textContent === '1 documents migrated'`)
	d.Click(`button[data-tab="save"]`)
	d.WaitFor("Letter in Work", `Array.from(document.querySelectorAll('.gj-doclist-item .gj-doc-name')).some((e) => e.textContent === 'Letter')`)
}
