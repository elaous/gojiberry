//go:build js && wasm

package main

import (
	"context"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/migrate"
	"github.com/elaous/gojiberry/transfer"
)

// newMigrate is the Migrate tab's session: Google Docs and Microsoft Word on
// OneDrive, into the folder the Save tab is showing. The access token is typed
// into the tab (a real app gets it from its OAuth sign-in); ?driveapi= and
// ?graphapi= point the clients at another address (a proxy, or a test server).
func newMigrate(u *ui, renderUI func()) *migrate.Session {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	param := func(k string) string {
		if q.Call("has", k).Bool() {
			return strings.TrimSpace(q.Call("get", k).String())
		}
		return ""
	}
	drive, graph := param("driveapi"), param("graphapi")
	s := migrate.NewSession([]migrate.SourceChoice{
		{Name: "google", Label: "Google Docs", New: func(tok string) migrate.Source { return &migrate.GoogleDocs{Token: tok, BaseURL: drive} }},
		{Name: "microsoft", Label: "Microsoft Word (OneDrive)", New: func(tok string) migrate.Source { return &migrate.OneDrive{Token: tok, BaseURL: graph} }},
	}, func() migrate.Sink {
		target, folder := u.saver.Where()
		return &migrate.SaverSink{Saver: u.saver.Saver, Target: target, Folder: folder}
	})
	s.OnChange = func() { renderUI() }
	s.OnDone = func(migrate.Report) { u.saver.Pending.Run(u.saver.Refresh) }
	return s
}

// pickWordFiles opens the chooser for Word files on this computer and migrates them.
func pickWordFiles(u *ui) {
	transfer.PickFiles(".docx,application/vnd.openxmlformats-officedocument.wordprocessingml.document", func(files []transfer.PickedFile, err error) {
		if err != nil {
			u.message = err.Error()
			return
		}
		var in migrate.Files
		for _, f := range files {
			in = append(in, migrate.File{Name: f.Name, Data: f.Data})
		}
		u.migrate.Pending.Run(func(ctx context.Context) error { return u.migrate.ImportFiles(ctx, in) })
	})
}
