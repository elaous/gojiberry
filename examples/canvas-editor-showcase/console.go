//go:build js && wasm

package main

import (
	"mime"
	"path"

	"github.com/elaous/gojiberry/console"
	"github.com/elaous/gojiberry/transfer"
)

// newConsole makes the Console tab: the command line of docsh (ls, mv, tag, import, export, pack, check ...) over
// the documents the Save tab shows. Files for import are added with the tab's button, and what export and pack make
// is downloaded. The writers of the other file formats are the file worker's (u.transfer).
func newConsole(u *ui, renderUI func()) *console.Session {
	target, _ := u.saver.Where()
	sh := console.New(u.saver.Saver, target, nil, nil, nil)
	sh.Registry = u.transfer
	s := console.NewSession(sh, func(name string, data []byte) { transfer.Download(name, fileType(name), data) })
	s.OnChange = func() { renderUI() }
	s.Before = func() { sh.Target, _ = u.saver.Where() }
	s.OnDone = func(error) { u.saver.Pending.Run(u.saver.Refresh) }
	s.PickFiles = func(add func(name string, data []byte)) {
		transfer.PickFiles("", func(files []transfer.PickedFile, err error) {
			if err != nil {
				u.message = err.Error()
				return
			}
			for _, f := range files {
				add(f.Name, f.Data)
			}
		})
	}
	return s
}

// fileType is the media type of a file the console made, from its extension.
func fileType(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	if path.Ext(name) == ".opendoc" {
		return "application/vnd.opendocs.pack"
	}
	return "application/octet-stream"
}
