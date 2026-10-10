//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The owner key of the person's address in the key directory. Once an address has one, the
// directory only takes entries signed by it, so a stolen session token alone cannot publish or
// revoke keys for the address. The key is made here, enrolled with the server, and kept in this
// browser (localStorage, for the directory it was enrolled with). Whoever can read this browser's
// storage can sign as the owner, which is why the panel offers a backup and a way to forget it: it is
// kept and treated like a password. (Wrapping it with the passkey is a later step: it costs a prompt
// for every signature.)

const ownerKeyPrefix = "showcase-enc:owner:" // + the directory address

// ownerRecord is what is kept for one directory: the address the key is enrolled for and the key.
type ownerRecord struct {
	Email string `json:"email"`
	Key   string `json:"key"`
}

// ownerUI is the state of the owner key panel.
type ownerUI struct {
	key   dirclient.OwnerKey // held in this browser, nil if none
	email string             // the address the held key was enrolled for
	// state is what the server last said about the address, shown as text.
	state    string
	msg      string
	isErr    bool
	backup   bool   // the backup is shown
	restore  string // the draft of a backup being restored
	restoreO bool   // the restore field is open
}

// loadOwner reads the owner key kept for the current directory and tells the client it is pinned.
func (d *dirUI) loadOwner() {
	d.owner = ownerUI{}
	if d.url == "" {
		return
	}
	raw := lsGet(ownerKeyPrefix + d.url)
	if raw == "" {
		return
	}
	var rec ownerRecord
	if json.Unmarshal([]byte(raw), &rec) != nil || rec.Email == "" {
		return
	}
	k, err := dirclient.ImportOwnerKey(rec.Key)
	if err != nil {
		return
	}
	d.owner.key, d.owner.email = k, rec.Email
	if d.dir != nil {
		d.dir.Pin(rec.Email, k)
	}
}

// keepOwner keeps k for the current directory and address.
func (d *dirUI) keepOwner(email string, k dirclient.OwnerKey) {
	b, _ := json.Marshal(ownerRecord{Email: email, Key: dirclient.ExportOwnerKey(k)})
	lsSet(ownerKeyPrefix+d.url, string(b))
	d.owner.key, d.owner.email = k, email
	if d.dir != nil {
		d.dir.Pin(email, k)
	}
}

// forgetOwner drops the key held in this browser. The address keeps its owner key on the server.
func (d *dirUI) forgetOwner() {
	lsSet(ownerKeyPrefix+d.url, "")
	d.owner = ownerUI{}
}

func (d *dirUI) say(msg string, isErr bool) { d.owner.msg, d.owner.isErr = msg, isErr }

// checkOwner asks the server who the account is and whether its address has an owner key.
func (d *dirUI) checkOwner(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	en, err := d.dir.Enrollment(ctx, me)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.owner.state = d.ownerState(me, en)
	d.say("", false)
	return nil
}

// ownerState says in words what the address has and whether this browser holds it.
func (d *dirUI) ownerState(me string, en dirclient.Enrollment) string {
	held := d.owner.key != nil && d.owner.email == me && slices.Contains(en.Fingerprints, dirclient.OwnerFingerprint(d.owner.key))
	switch {
	case !en.Enrolled:
		return T("dir.owner.none", "%s has no owner key: anyone who signs in as this address can publish keys for it.", me)
	case held:
		return T("dir.owner.held", "%s has an owner key, and this browser holds it (%s).", me, dirclient.OwnerFingerprint(d.owner.key))
	default:
		return T("dir.owner.notHeld", "%s has an owner key (%s), and this browser does not hold it: restore it from its backup to publish.", me, firstOr(en.Fingerprints))
	}
}

func firstOr(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// enrollOwner makes an owner key and enrolls it for the signed-in address.
func (d *dirUI) enrollOwner(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	k, err := dirclient.GenerateOwnerKey()
	if err != nil {
		return err
	}
	if err := d.dir.Enroll(ctx, me, k); err != nil {
		if errors.Is(err, dirclient.ErrAlreadyEnrolled) {
			d.say(T("dir.owner.already", "%s already has an owner key. If it is yours, restore it from its backup. If it is not, somebody enrolled before you: the directory's log shows when.", me), true)
			return nil
		}
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.keepOwner(me, k)
	d.owner.backup = true // the first thing to do with a new key is back it up
	d.say(T("dir.owner.enrolled", "Owner key enrolled for %s. Back it up now: if this browser's storage is cleared, only the backup can publish for this address again.", me), false)
	return d.checkOwner(ctx)
}

// restoreOwner takes a backup, checks it is an owner key of the signed-in address, and keeps it.
func (d *dirUI) restoreOwner(ctx context.Context) error {
	k, err := dirclient.ImportOwnerKey(d.owner.restore)
	if err != nil {
		d.say(T("dir.owner.badBackup", "That is not an owner key backup."), true)
		return nil
	}
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	en, err := d.dir.Enrollment(ctx, me)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	if !slices.Contains(en.Fingerprints, dirclient.OwnerFingerprint(k)) {
		d.say(T("dir.owner.notOwner", "That key is not an owner key of %s.", me), true)
		return nil
	}
	d.keepOwner(me, k)
	d.owner.restore, d.owner.restoreO = "", false
	d.say(T("dir.owner.restored", "Owner key restored for %s.", me), false)
	return d.checkOwner(ctx)
}

// ownerPanel is the owner key section of the directory settings.
func (d *dirUI) ownerPanel(e *encUI) []xango.Node {
	if d.dir == nil {
		return nil
	}
	busy := e.pending.Busy()
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("style", "margin:0.8em 0 0.2em;font-size:13px")}, xango.Text(T("dir.owner.title", "Owner key for your address"))),
		para("dir-owner-explain", T("dir.owner.explain", "An owner key makes the directory take entries for your address only when this key signed them, so a stolen session cannot publish or revoke for you."), false),
	}
	if d.owner.state != "" {
		nodes = append(nodes, para("dir-owner-state", d.owner.state, false))
	}
	if d.owner.msg != "" {
		nodes = append(nodes, para("dir-owner-msg", d.owner.msg, d.owner.isErr))
	}
	row := []xango.Node{btn("dir-owner-check", T("dir.owner.check", "Check my address"), func() { e.run(d.checkOwner) }, busy)}
	if d.owner.key == nil {
		row = append(row,
			btn("dir-owner-enroll", T("dir.owner.enroll", "Make and enroll an owner key"), func() { e.run(d.enrollOwner) }, busy),
			btn("dir-owner-restore-open", T("dir.owner.restoreOpen", "Restore from a backup…"), func() { d.owner.restoreO = !d.owner.restoreO; e.render() }, false))
	} else {
		row = append(row,
			btn("dir-owner-backup", T("dir.owner.backup", "Back up the owner key"), func() { d.owner.backup = !d.owner.backup; e.render() }, false),
			btn("dir-owner-forget", T("dir.owner.forget", "Forget it in this browser"), func() {
				if d.ask == nil {
					return
				}
				d.ask(T("dir.owner.forget", "Forget it in this browser"),
					T("dir.owner.confirmForget", "Forget the owner key in this browser? The address keeps its owner key on the server and will refuse entries without it. You can restore the key only from its backup."),
					func() { d.forgetOwner(); e.render() })
				e.render()
			}, busy))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")}, row...))
	if d.owner.key != nil && d.owner.backup {
		secret := dirclient.ExportOwnerKey(d.owner.key)
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "dir-owner-backup-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
			para("dir-owner-backup-warning", T("dir.owner.backupWarning", "This is the owner key. Whoever has it can publish and revoke keys for your address. Keep it offline, like a password."), true),
			textarea("dir-owner-secret", T("dir.owner.secret", "Owner key (secret)"), secret, "", "2", func(string) {}),
			xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
				btn("dir-owner-download", T("enc.download", "Download"), func() { transfer.Download("owner-key.txt", "text/plain", []byte(secret+"\n")) }, false),
				btn("dir-owner-hide", T("dir.owner.hide", "Hide it"), func() { d.owner.backup = false; e.render() }, false))))
	}
	if d.owner.key == nil && d.owner.restoreO {
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "dir-owner-restore-box"), xango.A("style", "margin:8px 0")},
			textarea("dir-owner-restore", T("dir.owner.restoreLabel", "Owner key backup"), d.owner.restore, T("dir.owner.restorePlaceholder", "Paste the backup text"), "2", func(v string) { d.owner.restore = v }),
			xango.El("div", []xango.Attr{xango.A("style", "margin-top:6px")},
				btn("dir-owner-restore-go", T("dir.owner.restore", "Restore"), func() { e.run(d.restoreOwner) }, busy))))
	}
	return nodes
}
