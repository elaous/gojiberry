//go:build js && wasm

package main

import (
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
)

// printRecoverySheet opens the printable page of a recovery key in a new window. The page is built
// here, from the secret, and is a blob in this browser: nothing is sent anywhere.
func printRecoverySheet(label, secret string) {
	fp, err := encstore.RecoveryFingerprint(secret)
	if err != nil {
		return
	}
	page := encstore.RecoverySheet(encstore.SheetText{
		Title:    T("enc.sheetTitle", "Recovery key"),
		Intro:    T("enc.sheetIntro", "This key opens the encrypted documents saved in the browser it was made for, if every other key is lost."),
		KeyLabel: T("enc.sheetKey", "Recovery key"),
		Finger:   T("enc.sheetFinger", "Fingerprint (to compare with the store's key list)"),
		Keep:     T("enc.sheetKeep", "Keep this sheet offline, somewhere only you can reach. Anyone who reads it can open your documents."),
		Lost:     T("enc.sheetLost", "If you lose this and every other key, nobody can open the documents, including us. There is no way back."),
		Print:    T("enc.sheetPrint", "Print"),
		QRAlt:    T("enc.sheetQR", "QR code of the recovery key. The same key is written above; scanning it moves the key to another device."),
		Date:     time.Now().Format("2006-01-02"),
	}, label, secret, fp)
	arr := js.Global().Get("Uint8Array").New(len(page))
	js.CopyBytesToJS(arr, page)
	blob := js.Global().Get("Blob").New([]any{arr}, map[string]any{"type": "text/html"})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	js.Global().Call("open", url, "_blank")
	// the new window needs the URL until it has loaded
	time.AfterFunc(time.Minute, func() { js.Global().Get("URL").Call("revokeObjectURL", url) })
}
