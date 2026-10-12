//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
	"slices"
	"strings"
	"time"
)

// encPeopleOrNone is encPeople, or nothing while encryption is off.
func (u *ui) encPeopleOrNone() xango.Node {
	if !u.enc.store.Enabled() {
		return xango.Frag()
	}
	return u.encPeople()
}

// ownerList says a list of owner keys in words, with the fingerprint of each: what a journal would
// make the store's keys, shown so the person can tell their own keys from someone else's.
func ownerList(list []encstore.Owner) string {
	var parts []string
	for _, o := range list {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", o.Label, o.Kind, o.Fingerprint()))
	}
	return strings.Join(parts, "; ")
}

// --- the People tab ---

func recipientRows(rs []encrypt.Recipient) []components.RecipientRow {
	var out []components.RecipientRow
	for _, r := range rs {
		out = append(out, components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint})
	}
	return out
}

func parseRecipient(text string) (encrypt.Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return encrypt.ParseAgeRecipient(text)
	}
	return encrypt.ParseSSHRecipient(text)
}

// ownerRows are the store's keys with whether each can be removed and why not.
func (e *encUI) ownerRows() []components.RecipientRow {
	rows := recipientRows(e.store.Recipients())
	used := lastUsedHere()
	for i := range rows {
		if t, ok := used[rows[i].Fingerprint]; ok {
			rows[i].Detail = T("enc.lastUsedHere", "Last used in this browser: %s", t.Format("2006-01-02"))
		} else {
			rows[i].Detail = T("enc.neverUsedHere", "Not used in this browser")
		}
		err := e.store.CanRemoveOwner(rows[i].Fingerprint)
		rows[i].CanRemove = err == nil
		if err != nil {
			rows[i].RemoveBlocked = tErr(err).Error()
		}
	}
	return rows
}

// refreshShared reads the current document's list of keys (and whether it is confidential) in the
// background when something it depends on changed.
func (u *ui) refreshShared() {
	e := u.enc
	id := u.saver.ID()
	key := fmt.Sprintf("%s|%v|%d", id, e.store.Unlocked(), len(e.store.Owners()))
	if key == e.sharedFor {
		return
	}
	e.sharedFor = key
	go func() {
		ctx := context.Background()
		conf, rows := false, []components.RecipientRow(nil)
		if id != "" {
			if en, err := u.saver.Saver.Get(ctx, "browser", id); err == nil {
				conf = en.Protection == saver.ProtectionE2EE
			}
			if sh, err := e.store.Sharing(); err == nil && conf {
				own := map[string]bool{}
				for _, o := range e.store.Recipients() {
					own[o.Fingerprint] = true
				}
				if list, err := sh.List(ctx, id); err == nil {
					for _, s := range list {
						if !own[s.Fingerprint] {
							row := components.RecipientRow{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint}
							e.dir.decorate(&row, true)
							rows = append(rows, row)
						}
					}
				}
			}
		}
		e.docConfidential, e.shared = conf, rows
		e.render()
	}()
}

// encPeople is the keys control of the People tab: the store's owner keys, with "Add my passkey",
// "Create a recovery key", removing and replacing keys (the same host code as
// examples/people-keys-demo, over the encrypted store), and, for a confidential document, who it is
// shared with.
func (u *ui) encPeople() xango.Node {
	e := u.enc
	u.refreshShared()
	unlocked := e.store.Unlocked() && !e.store.Interrupted()
	docID := u.saver.ID()
	props := components.RecipientsProps{
		Owner: e.ownerRows(), Shared: e.shared,
		Pending: e.pending, Ask: u.saver.Asker.Ask,
		Confidential: true, SecondKeyMessage: T("enc.err.needSecond", "A passkey cannot be the only key: add a recovery key or an SSH key too"),
		PasskeySupported: client.Supported(),
		Copy:             func(text string) { u.saver.Copy(text) },
		Download:         func(name, text string) { transfer.Download(name, "text/plain", []byte(text)) },
	}
	var note string
	switch {
	case e.store.Interrupted():
		note = T("enc.peopleInterrupted", "A change to the keys did not finish (Save tab): finish it or undo it before changing keys here.")
	case !unlocked:
		note = T("enc.peopleLocked", "Unlock the store (Save tab) to add keys or share documents.")
	default:
		props.AddMyPasskey, props.MyPasskeyAdded = e.wrapDo(e.addMyPasskey), e.myAdded
		props.RemoveOwner = func(ctx context.Context, fp string) error {
			return e.wrapDo(func(ctx context.Context) error { return e.removeOwner(ctx, u, fp) })(ctx)
		}
		props.ReplaceOwner = func(ctx context.Context, fp string) error {
			return e.wrapDo(func(ctx context.Context) error { return e.replacePasskey(ctx, u, fp) })(ctx)
		}
		if !e.store.HasKind(encstore.KindRecovery) || e.peopleKey != nil {
			props.CreateRecoveryKey = func(ctx context.Context) (string, error) { return e.prepareRecoveryKey() }
			props.OnRecoveryKey = func(string) { e.render() }
			props.CommitRecoveryKey = e.wrapDo(e.commitRecoveryKey)
			props.CancelRecoveryKey = func() { e.peopleKey = nil; e.render() }
			props.OnRecoveryAcknowledge = func(on bool) {
				if e.peopleKey != nil {
					e.peopleKey.acked = on
				}
				e.render()
			}
			if e.peopleKey != nil {
				props.RecoverySecret, props.RecoveryAcknowledged = e.peopleKey.secret, e.peopleKey.acked
			}
		}
		props.OnRecoveryDone = nil
		switch {
		case docID == "":
			note = T("enc.peopleNoDoc", "Save the document to share it with other keys.")
		case !e.docConfidential:
			note = T("enc.peopleNotConfidential", "This document is encrypted to your keys. Make it confidential (Save tab) to share it with other keys.")
		default:
			props.Draft, props.Confirm = e.draft, e.confirm
			if strings.TrimSpace(e.draft) != "" {
				if r, err := parseRecipient(e.draft); err == nil {
					props.Parsed = &components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint}
					e.dir.decorate(props.Parsed, false)
				} else {
					props.ParseError = err.Error()
				}
			}
			props.OnDraft = func(k, c string) { e.draft, e.confirm = k, c; e.render() }
			props.Add = func(ctx context.Context, key, confirm string) error {
				return e.wrapDo(func(ctx context.Context) error {
					r, err := parseRecipient(key)
					if err != nil {
						return err
					}
					sh, err := e.store.Sharing()
					if err != nil {
						return err
					}
					if err := sh.Add(ctx, docID, r, confirm); err != nil {
						return err
					}
					e.dir.verifiedAdded[r.Fingerprint] = e.dir.verified[r.Fingerprint]
					e.dir.marksAdded[r.Fingerprint] = e.dir.marks[r.Fingerprint]
					e.draft, e.confirm, e.sharedFor = "", "", ""
					return nil
				})(ctx)
			}
			props.Remove = func(ctx context.Context, fp string) (int, error) {
				sh, err := e.store.Sharing()
				if err != nil {
					return 0, tErr(err)
				}
				n, err := sh.Remove(ctx, docID, fp)
				e.sharedFor = ""
				return n, tErr(err)
			}
			props.OnRemoved = func(_ string, n int) { e.removedNotice = components.RemovedNotice(n) }
			props.Notice = e.removedNotice
		}
		e.dir.wire(&props, e)
	}
	nodes := []xango.Node{components.Recipients(props)}
	nodes = append(nodes, e.rekeyProgress()...)
	nodes = append(nodes, e.dir.settingsPanel(e)...)
	if note != "" {
		nodes = append([]xango.Node{para("enc-people-note", note, false)}, nodes...)
	}
	if e.notice != "" && u.tabIsPeople() {
		nodes = append([]xango.Node{xango.El("p", []xango.Attr{xango.A("id", "enc-people-message"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(e.notice))}, nodes...)
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-people"), xango.A("style", "margin-top:1.2em")}, nodes...)
}

func (u *ui) tabIsPeople() bool { return u.tab == "people" }

func (e *encUI) addMyPasskey(ctx context.Context) error {
	if e.store.HasKind(encstore.KindPasskey) {
		return errors.New(T("enc.havePasskey", "Your passkey is already one of this store's keys."))
	}
	k, err := e.makePasskey(ctx)
	if err != nil {
		return err
	}
	if err := e.store.AddOwner(ctx, k); err != nil {
		return err // the passkey stays pending: pressing the button again does not make another
	}
	e.pendingCred = ""
	row := components.RecipientRow{Label: k.Label, Type: k.Recipient.Type, Fingerprint: k.Recipient.Fingerprint}
	e.myAdded, e.sharedFor = &row, ""
	return nil
}

// replacePasskey swaps the passkey for a new one in one transaction. The slot that remembers which
// passkey to ask is put back if the swap fails, so the next unlock still asks the one that works.
func (e *encUI) replacePasskey(ctx context.Context, u *ui, fp string) error {
	prev := client.LocalCredential(encSlot)
	id, err := client.CreateLocal(ctx, encSlot, T("enc.passkeyRP", "Gojiberry showcase"), T("enc.passkeyUser", "Showcase owner"))
	if err != nil {
		client.SetLocalCredential(encSlot, prev)
		return e.passkeyFailed(err)
	}
	restore := func() { client.SetLocalCredential(encSlot, prev) }
	_, secret, err := client.DeriveKey(ctx, id, encrypt.PRFSalt)
	if err != nil {
		restore()
		return e.passkeyFailed(err)
	}
	k, err := encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
	clear(secret)
	if err != nil {
		restore()
		return err
	}
	if err := e.store.ReplaceOwner(ctx, fp, k); err != nil {
		restore()
		return err
	}
	e.sharedFor, e.myAdded = "", nil
	e.removedNotice = T("enc.replaced", "The passkey was replaced: the new one opens the store from now on. The old one is still in your authenticator: delete it in your browser's or authenticator's passkey settings.")
	return u.saver.Refresh(ctx)
}

func (e *encUI) removeOwner(ctx context.Context, u *ui, fp string) error {
	if err := e.store.RemoveOwner(ctx, fp); err != nil {
		return err
	}
	e.sharedFor, e.myAdded = "", nil
	e.removedNotice = T("enc.keyRemoved", "The key was removed and everything saved was encrypted again without it. Whoever held it keeps what they already read or copied.")
	if !e.store.HasKind(encstore.KindPasskey) {
		client.ForgetLocal(encSlot)
	}
	return u.saver.Refresh(ctx)
}

// prepareRecoveryKey makes a recovery key and returns its secret to be shown. Nothing is added to
// the store: commitRecoveryKey does that once the person confirmed they saved the secret.
func (e *encUI) prepareRecoveryKey() (string, error) {
	k, secret, err := encstore.NewRecoveryKey(T("enc.recoveryLabel", "Recovery key"))
	if err != nil {
		return "", err
	}
	e.peopleKey = &prepared{key: k, secret: secret}
	return secret, nil
}

func (e *encUI) commitRecoveryKey(ctx context.Context) error {
	p := e.peopleKey
	if p == nil {
		return nil
	}
	k := p.key
	k.SecretSaved, k.Forget = p.acked, true // another key is unlocked; this one is for the day it is lost
	if err := e.store.AddOwner(ctx, k); err != nil {
		return err // the secret stays on screen: it can be tried again
	}
	e.peopleKey = nil
	e.sharedFor = ""
	return nil
}

// trustedListsKey is where the digests of the key lists the person confirmed (or that a passkey or a
// recovery key opened) are kept in this browser: a known-hosts file for stores.
const trustedListsKey = "showcase-enc:trusted-lists"

// keysUsedKey is where the time each key last unlocked the store in this browser is kept: a map from a
// key's fingerprint to an RFC 3339 time. It says what this browser saw, not what happened elsewhere.
const keysUsedKey = "showcase-enc:keys-used"

func lastUsedHere() map[string]time.Time {
	out := map[string]time.Time{}
	var raw map[string]string
	if json.Unmarshal([]byte(lsGet(keysUsedKey)), &raw) != nil {
		return out
	}
	for fp, v := range raw {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			out[fp] = t
		}
	}
	return out
}

// hookStore connects the store's hooks to the page: a key that unlocks is noted as used here, and a
// change that re-encrypts the store reports its progress.
func (e *encUI) hookStore() {
	e.store.ConfirmFirstUse = true
	var known []string
	if json.Unmarshal([]byte(lsGet(trustedListsKey)), &known) == nil {
		for _, d := range known {
			e.store.TrustList(d)
		}
	}
	e.store.OnTrust = func(d string) {
		var k []string
		_ = json.Unmarshal([]byte(lsGet(trustedListsKey)), &k)
		if !slices.Contains(k, d) {
			b, _ := json.Marshal(append(k, d))
			lsSet(trustedListsKey, string(b))
		}
	}
	e.store.OnUnlock = func(o encstore.Owner) {
		fp := o.Fingerprint()
		if fp == "" {
			return
		}
		raw := map[string]string{}
		_ = json.Unmarshal([]byte(lsGet(keysUsedKey)), &raw)
		raw[fp] = time.Now().UTC().Format(time.RFC3339)
		b, _ := json.Marshal(raw)
		lsSet(keysUsedKey, string(b))
	}
	e.store.Progress = func(done, total int) {
		e.rekeyDone, e.rekeyTotal = done, total
		if done >= total {
			e.rekeyDone, e.rekeyTotal = 0, 0
		}
		e.render()
	}
}

// rekeyProgress is the status line while the store is being encrypted again: "Encrypting again: 3 of 12
// documents".
func (e *encUI) rekeyProgress() []xango.Node {
	if e.rekeyTotal == 0 {
		return nil
	}
	return []xango.Node{xango.El("p", []xango.Attr{xango.A("id", "enc-progress"), xango.A("role", "status")},
		xango.Text(T("enc.progress", "Encrypting again: %d of %d documents", e.rekeyDone, e.rekeyTotal)))}
}

// forgetStoreMemory drops what this browser remembers about a store that is gone: the key lists the
// person confirmed, when each key was used and when the recovery key was checked.
func forgetStoreMemory() {
	for _, k := range []string{trustedListsKey, keysUsedKey, recoveryCheckedKey, recoverySnoozeKey} {
		lsSet(k, "")
	}
}
