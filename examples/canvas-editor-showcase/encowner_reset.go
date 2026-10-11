//go:build js && wasm

package main

import (
	"context"
	"encoding/json"

	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// Replacing the owner key. The holder of the key rotates it: the old key signs the new one in, and
// from then on only the new one publishes. A person who lost the key asks for a reset instead: the
// directory waits (so that the real holder, if any, can cancel) and then takes a key made here. The
// key made for a reset is held apart from the main one until the reset is completed.

// pendingID is where the key made for a reset is kept: beside the main one, under another name.
func (d *dirUI) pendingID() string { return d.url + "#reset" }

// keepPending keeps the key made for a reset until it is completed or dropped.
func (d *dirUI) keepPending(email string, signer dirclient.OwnerSigner, plain bool, k dirclient.OwnerKey) {
	if !plain {
		if w, ok := signer.(*webKey); ok && webOwnerSave(d.pendingID(), email, w) != nil {
			signer, plain = k, true
		}
	}
	d.owner.pendingSec = dirclient.ExportOwnerKey(k)
	if plain {
		b, _ := json.Marshal(ownerRecord{Email: email, Key: d.owner.pendingSec})
		lsSet(ownerKeyPrefix+d.pendingID(), string(b))
	} else {
		clear(k)
	}
	d.owner.pending, d.owner.pendingText = signer, plain
}

// loadPending reads the text copy of a pending key (the browser-kept one is read in the background).
func (d *dirUI) loadPending() {
	d.owner.pending, d.owner.pendingText, d.owner.pendingSec = nil, false, ""
	if raw := lsGet(ownerKeyPrefix + d.pendingID()); raw != "" {
		var rec ownerRecord
		if json.Unmarshal([]byte(raw), &rec) == nil {
			if k, err := dirclient.ImportOwnerKey(rec.Key); err == nil {
				d.owner.pending, d.owner.pendingText, d.owner.pendingSec = k, true, rec.Key
			}
		}
	}
}

// dropPending forgets the key made for a reset.
func (d *dirUI) dropPending() {
	if d.owner.pending == nil && d.owner.pendingSec == "" {
		return
	}
	_ = webOwnerDelete(d.pendingID())
	lsSet(ownerKeyPrefix+d.pendingID(), "")
	d.owner.pending, d.owner.pendingText, d.owner.pendingSec = nil, false, ""
}

// rotateOwner replaces the held owner key with a new one, signed in by the old.
func (d *dirUI) rotateOwner(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	k, err := dirclient.GenerateOwnerKey()
	if err != nil {
		return err
	}
	signer, plain := signerFor(k)
	if err := d.dir.Rotate(ctx, me, d.owner.key, signer); err != nil {
		clear(k)
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.keepOwner(me, signer, plain, k, true)
	d.owner.backup = true
	return d.finish(ctx, T("dir.owner.rotated", "Owner key replaced for %s. The old key no longer publishes. Back the new one up now.", me))
}

// requestReset asks the directory to replace the owner key of the address by one made here, after its wait.
func (d *dirUI) requestReset(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	en, err := d.dir.RequestReset(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	k, err := dirclient.GenerateOwnerKey()
	if err != nil {
		return err
	}
	signer, plain := signerFor(k)
	d.keepPending(me, signer, plain, k)
	d.owner.resetSince, d.owner.resetDue = en.ResetSince, en.ResetDue
	return d.finish(ctx, T("dir.owner.resetAsked", "Reset requested for %s. Back up the new key now; once the wait is over, complete the reset here.", me))
}

// completeReset makes the key made at the request the owner key, once the directory's wait is over.
func (d *dirUI) completeReset(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	if err := d.dir.CompleteReset(ctx, me, d.owner.pending); err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	signer, plain, text := d.owner.pending, d.owner.pendingText, d.owner.pendingSec
	if w, ok := signer.(*webKey); ok && !plain {
		if webOwnerSave(d.url, me, w) != nil {
			// IndexedDB refused: the backup text is then the key
			if k, err := dirclient.ImportOwnerKey(text); err == nil {
				signer, plain = k, true
			}
		}
	}
	if k, ok := signer.(dirclient.OwnerKey); ok && plain {
		b, _ := json.Marshal(ownerRecord{Email: me, Key: dirclient.ExportOwnerKey(k)})
		lsSet(ownerKeyPrefix+d.url, string(b))
	} else {
		lsSet(ownerKeyPrefix+d.url, "")
	}
	d.owner.key, d.owner.email, d.owner.plain, d.owner.secret = signer, me, plain, text
	if plain {
		d.owner.secret = ""
	}
	d.dir.Pin(me, signer)
	_ = webOwnerDelete(d.pendingID())
	lsSet(ownerKeyPrefix+d.pendingID(), "")
	d.owner.pending, d.owner.pendingText, d.owner.pendingSec = nil, false, ""
	return d.finish(ctx, T("dir.owner.resetDone", "Owner key reset for %s: this browser holds the new key.", me))
}

// cancelReset stops a waiting reset, with the owner key this browser holds.
func (d *dirUI) cancelReset(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	if err := d.dir.CancelReset(ctx, me, d.owner.key); err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.dropPending()
	return d.finish(ctx, T("dir.owner.resetCancelled", "Reset cancelled for %s.", me))
}

// confirmStep asks before a step that cannot be taken back, then runs it.
func (d *dirUI) confirmStep(e *encUI, title, text string, run func(context.Context) error) {
	if d.ask == nil {
		return
	}
	d.ask(title, text, func() { e.run(run) })
	e.render()
}

// replacePanel is the buttons and notes for replacing the owner key; row gets the buttons.
func (d *dirUI) replacePanel(e *encUI, busy bool) (row, notes []xango.Node) {
	switch {
	case d.owner.pending != nil:
		row = append(row,
			btn("dir-owner-reset-complete", T("dir.owner.resetComplete", "Complete the reset"), func() { e.run(d.completeReset) }, busy),
			btn("dir-owner-reset-giveup", T("dir.owner.resetGiveUp", "Give up the reset here"), func() {
				d.confirmStep(e, T("dir.owner.resetGiveUp", "Give up the reset here"),
					T("dir.owner.confirmGiveUp", "Drop the new key made for the reset? The request stays on the server until it is cancelled or completed; completing it needs this key."),
					func(context.Context) error { d.dropPending(); return nil })
			}, busy))
		notes = append(notes, xango.El("div", []xango.Attr{xango.A("id", "dir-owner-pending-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
			para("dir-owner-pending-warning", T("dir.owner.pendingWarning", "This is the new owner key for the reset. Save it now; it is the only copy once the reset is done."), true),
			textarea("dir-owner-pending-secret", T("dir.owner.pendingSecret", "New owner key (secret)"), d.owner.pendingSec, "", "2", func(string) {}),
			xango.El("div", []xango.Attr{xango.A("style", "margin-top:6px")},
				btn("dir-owner-pending-download", T("enc.download", "Download"), func() { transfer.Download("owner-key.txt", "text/plain", []byte(d.owner.pendingSec+"\n")) }, false))))
	case d.owner.key == nil:
		row = append(row, btn("dir-owner-reset-request", T("dir.owner.resetRequest", "I lost my owner key…"), func() {
			d.confirmStep(e, T("dir.owner.resetRequest", "I lost my owner key…"),
				T("dir.owner.confirmReset", "Ask the directory to replace the owner key of your address? It waits 24 hours before it accepts a new one, and the holder of the old key can cancel the reset in that time. Until then nothing changes."),
				d.requestReset)
		}, busy))
	}
	if d.owner.key != nil {
		row = append(row, btn("dir-owner-rotate", T("dir.owner.rotate", "Replace the owner key…"), func() {
			d.confirmStep(e, T("dir.owner.rotate", "Replace the owner key…"),
				T("dir.owner.confirmRotate", "Make a new owner key and replace the current one? The old key and its backup stop working; the new key's backup is shown to save."),
				d.rotateOwner)
		}, busy))
		if !d.owner.resetSince.IsZero() {
			row = append(row, btn("dir-owner-reset-cancel", T("dir.owner.resetCancel", "Cancel the reset"), func() { e.run(d.cancelReset) }, busy))
		}
	}
	return row, notes
}

// finish re-reads the address's state and then says what was done, unless reading it failed.
func (d *dirUI) finish(ctx context.Context, done string) error {
	err := d.checkOwner(ctx)
	if !d.owner.isErr {
		d.say(done, false)
	}
	return err
}
