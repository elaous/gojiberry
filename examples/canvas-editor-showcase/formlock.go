//go:build js && wasm && (experimental || exp_forms)

package main

import (
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/formseal"
	"github.com/elaous/gojiberry/xango"
)

const (
	formKeyID   = "form-lock:local"
	formKeyText = "showcase:formlock-key"
)

var formKey struct {
	signer  crypto.Signer
	plain   bool
	loaded  bool
	secret  string
	restore string
	open    bool // the restore field is open
}

func loadFormKey(renderUI func()) {
	if formKey.loaded {
		return
	}
	formKey.loaded = true
	if raw := lsGet(formKeyText); raw != "" {
		if k, err := dirclient.ImportOwnerKey(raw); err == nil {
			formKey.signer, formKey.plain = k, true
			return
		}
	}
	go func() {
		if _, w, ok, err := webOwnerLoad(formKeyID); err == nil && ok {
			formKey.signer = w
			renderUI()
		}
	}()
}

func (u *ui) lockSigner() crypto.Signer {
	if u.enc != nil && u.enc.dir.owner.key != nil {
		return u.enc.dir.owner.key
	}
	return formKey.signer
}

func keepFormKey(k dirclient.OwnerKey, made bool) {
	signer, plain := signerFor(k)
	if w, ok := signer.(*webKey); ok && !plain && webOwnerSave(formKeyID, "", w) != nil {
		signer, plain = k, true
	}
	if plain {
		lsSet(formKeyText, dirclient.ExportOwnerKey(k))
	} else {
		lsSet(formKeyText, "")
		if made {
			formKey.secret = dirclient.ExportOwnerKey(k)
		}
		clear(k)
	}
	formKey.signer, formKey.plain = signer, plain
}

func (u *ui) createFormKey(renderUI func()) {
	go func() {
		// the browser's key comes from promises: not waitable on the event thread
		k, err := dirclient.GenerateOwnerKey()
		if err != nil {
			u.forms.msg = err.Error()
			renderUI()
			return
		}
		keepFormKey(k, true)
		u.forms.msg = T("forms.keyMade", "A key was made. Locks you make now are sealed with it. Keep the backup: without the key a lock cannot be taken off here.")
		u.announce(u.forms.msg)
		renderUI()
	}()
}

func sealProps(ff *canvas.FormFieldObject) formseal.Props {
	return formseal.Props{Kind: string(ff.Kind), Name: ff.Name, Label: ff.Label, Placeholder: ff.Placeholder, Pattern: ff.Pattern, Required: ff.Required, Options: ff.Options}
}

func (u *ui) lockField(fl *canvas.TextFlow, ff *canvas.FormFieldObject, redraw, renderUI func()) {
	signer := u.lockSigner()
	if signer == nil {
		u.forms.msg = T("forms.needKey", "Locking needs a key: it proves the lock is yours, and only the key takes it off. Make one with \"Make a key\" below.")
		u.announce(u.forms.msg)
		renderUI()
		return
	}
	go func() { // signing may wait for the browser
		sl, err := formseal.Make(signer, sealProps(ff))
		if err != nil {
			u.forms.msg = err.Error()
			renderUI()
			return
		}
		ff.Locked, ff.LockKey, ff.LockSig = true, sl.Key, sl.Sig
		if u.forms.target == ff && u.forms.edit {
			u.forms.edit = false
		}
		fl.Touch()
		redraw()
		renderUI()
	}()
}

func (u *ui) unlockField(fl *canvas.TextFlow, ff *canvas.FormFieldObject, redraw, renderUI func()) {
	if ff.LockKey != "" && !formseal.Holds(u.lockSigner(), formseal.Seal{Key: ff.LockKey, Sig: ff.LockSig}) {
		u.forms.msg = fmt.Sprintf(T("forms.wrongKey", "This field was locked with another key (%s). Only that key takes the lock off."), formseal.Fingerprint(ff.LockKey))
		u.announce(u.forms.msg)
		renderUI()
		return
	}
	ff.Locked, ff.LockKey, ff.LockSig = false, "", ""
	fl.Touch()
	redraw()
	renderUI()
}

func signerFingerprint(s crypto.Signer) string {
	pub, ok := s.Public().(ed25519.PublicKey)
	if !ok {
		return ""
	}
	return formseal.Fingerprint(base64.StdEncoding.EncodeToString(pub))
}

func sealNote(ff *canvas.FormFieldObject) (text string, broken bool) {
	if ff.LockKey == "" {
		return "", false
	}
	fp := formseal.Fingerprint(ff.LockKey)
	if !formseal.Verify(formseal.Seal{Key: ff.LockKey, Sig: ff.LockSig}, sealProps(ff)) {
		return fmt.Sprintf(T("forms.sealBroken", "The seal does not match: this field was changed after it was locked with key %s."), fp), true
	}
	return fmt.Sprintf(T("forms.sealed", "Locked with key %s; the field is as it was locked."), fp), false
}

func (u *ui) formKeyPanel(renderUI func()) []xango.Node {
	var nodes []xango.Node
	if s := u.lockSigner(); s != nil {
		who := T("forms.keyBrowser", "this browser's key")
		if u.enc != nil && u.enc.dir.owner.key != nil {
			who = T("forms.keyOwner", "your owner key")
		}
		fp := signerFingerprint(s)
		nodes = append(nodes, para("forms-key", fmt.Sprintf(T("forms.keyIs", "Locks are made with %s (%s)."), who, fp), false))
		if formKey.secret != "" && s == formKey.signer {
			nodes = append(nodes,
				para("forms-key-backup-about", T("forms.keyBackup", "Backup of the key. It is shown once; keep it where only you can read it."), false),
				xango.El("textarea", []xango.Attr{xango.A("id", "forms-key-backup"), xango.A("readonly", "readonly"), xango.A("rows", "3"), xango.A("spellcheck", "false"), xango.A("aria-label", T("forms.keyBackupLabel", "Backup of the key")), xango.A("style", "width:100%;box-sizing:border-box;font-family:monospace;font-size:12px")}, xango.Text(formKey.secret)),
				btn("forms-key-hide", T("forms.keyHide", "I have saved it"), func() { formKey.secret = ""; renderUI() }, false))
		}
		return nodes
	}
	nodes = append(nodes,
		para("forms-nokey", T("forms.noKey", "Locking a field needs a key: it proves the lock is yours, and only the key takes it off."), false),
		btn("forms-create-key", T("forms.makeKey", "Make a key"), func() { u.createFormKey(renderUI) }, false),
		btn("forms-restore-key", T("forms.restoreKey", "Restore a key from its backup"), func() { formKey.open = !formKey.open; renderUI() }, false))
	if formKey.open {
		nodes = append(nodes,
			lineInput("forms-restore-text", T("forms.restoreText", "The backup text"), formKey.restore, "", func(v string) { formKey.restore = v }),
			btn("forms-restore-go", T("forms.restoreGo", "Restore"), func() {
				k, err := dirclient.ImportOwnerKey(strings.TrimSpace(formKey.restore))
				if err != nil {
					u.forms.msg = T("forms.restoreBad", "That is not a backup of a key.")
					renderUI()
					return
				}
				go func() {
					keepFormKey(k, false)
					formKey.restore, formKey.open = "", false
					u.forms.msg = T("forms.restored", "The key was restored.")
					renderUI()
				}()
			}, false))
	}
	return nodes
}
