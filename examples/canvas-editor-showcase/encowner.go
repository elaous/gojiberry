//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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

const (
	ownerKeyPrefix = "showcase-enc:owner:"   // + the directory address
	watchStateKey  = "showcase-enc:watch:"   // the log position and what lookups saw
	logKeysKey     = "showcase-enc:logkeys:" // the pinned log keys
	alertsKey      = "showcase-enc:alerts:"  // what the watch found and was not dismissed
	pinsKey        = "showcase-enc:pins:"    // owner keys the person confirmed for other addresses
)

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

	logKeys  map[string]string // the pinned log keys (key ID to base64), none until pinned
	alerts   []string          // what watching found, until dismissed
	watchMsg string
}

// loadWatch restores what is kept for the current directory: the log position, the pinned log keys
// and the alerts not yet dismissed. A position that cannot be read is dropped, and said.
func (d *dirUI) loadWatch() {
	d.owner.logKeys, d.owner.alerts, d.owner.watchMsg = nil, nil, ""
	if d.dir == nil {
		return
	}
	if err := d.dir.LoadState(lsGet(watchStateKey + d.url)); err != nil {
		lsSet(watchStateKey+d.url, "")
		d.owner.watchMsg = err.Error()
	}
	if raw := lsGet(pinsKey + d.url); raw != "" {
		var pins map[string]string
		if json.Unmarshal([]byte(raw), &pins) == nil {
			d.dir.LoadPins(pins)
		}
	}
	if raw := lsGet(logKeysKey + d.url); raw != "" {
		var keys map[string]string
		if json.Unmarshal([]byte(raw), &keys) == nil && d.dir.SetLogKeys(keys) == nil {
			d.owner.logKeys = keys
		}
	}
	if raw := lsGet(alertsKey + d.url); raw != "" {
		_ = json.Unmarshal([]byte(raw), &d.owner.alerts)
	}
}

func (d *dirUI) keepAlerts() {
	if len(d.owner.alerts) == 0 {
		lsSet(alertsKey+d.url, "")
		return
	}
	b, _ := json.Marshal(d.owner.alerts)
	lsSet(alertsKey+d.url, string(b))
}

// pinLogKeys takes the keys the server states for its log and keeps them as the ones to check
// against. This is trust on first use: it proves the server is consistent with itself from now on,
// not that it is the server meant, which is why the person is asked first.
func (d *dirUI) pinLogKeys(ctx context.Context) error {
	keys, err := d.dir.ServerLogKeys(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	if len(keys) == 0 {
		d.say(T("dir.log.noKeys", "The server did not state any log keys."), true)
		return nil
	}
	if err := d.dir.SetLogKeys(keys); err != nil {
		d.say(err.Error(), true)
		return nil
	}
	b, _ := json.Marshal(keys)
	lsSet(logKeysKey+d.url, string(b))
	d.owner.logKeys = keys
	d.say(T("dir.log.pinned", "The log keys are pinned: later looks at the log are checked against them."), false)
	return nil
}

func (d *dirUI) forgetLogKeys() {
	_ = d.dir.SetLogKeys(nil)
	lsSet(logKeysKey+d.url, "")
	d.owner.logKeys = nil
}

// watchMine reads the log since the last look and says what somebody else did to my address.
func (d *dirUI) watchMine(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	var owner dirclient.OwnerKey
	if d.owner.email == me {
		owner = d.owner.key
	}
	rep, err := d.dir.Watch(ctx, me, owner)
	switch {
	case errors.Is(err, dirclient.ErrNoLogKeys):
		d.say(T("dir.log.needKeys", "Pin the server's log keys first."), true)
		return nil
	case errors.Is(err, dirclient.ErrLogRewritten):
		d.owner.alerts = append(d.owner.alerts, T("dir.log.rewritten", "The directory's log was cut back or rewritten since the last look. Do not trust this directory until you know why."))
		d.keepAlerts()
		d.say("", false)
		return nil
	case err != nil:
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.owner.alerts = append(d.owner.alerts, rep.Alerts...)
	d.keepAlerts()
	msg := T("dir.log.checked", "The log was checked up to entry %d: %d new for your address, %d of them not yours.", rep.HeadSeq, rep.New, len(rep.Alerts))
	if rep.AddressUnconfirmed {
		msg += " " + T("dir.log.unconfirmed", "No entry of yours confirms the log name of your address yet: publish a key, then watch again.")
	}
	if rep.Filtered {
		msg += " " + T("dir.log.filtered", "The server showed only the entries about your address; enroll an owner key to read the whole log.")
	}
	d.owner.watchMsg = msg
	d.say("", false)
	return nil
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
	return append(nodes, d.watchPanel(e)...)
}

// confirmPanel lists the owner keys behind the last lookup that nothing held confirms. A row signed by
// an owner key the server alone vouches for is not shown as owner-signed; the person can compare the
// key's fingerprint with its owner another way (a call, a message) and confirm it here, which keeps
// the pin for this directory and shows the strongest mark from then on.
func (d *dirUI) confirmPanel(e *encUI) []xango.Node {
	type pending struct {
		pub   string
		state dirclient.OwnerState
	}
	var todo []pending
	for _, k := range d.keys {
		if k.Owner.NeedsConfirming() && k.OwnerPub != "" && !slices.ContainsFunc(todo, func(p pending) bool { return p.pub == k.OwnerPub }) {
			todo = append(todo, pending{k.OwnerPub, k.Owner})
		}
	}
	if len(todo) == 0 {
		return nil
	}
	nodes := []xango.Node{xango.El("h4", []xango.Attr{xango.A("style", "margin:0.8em 0 0.2em;font-size:13px")}, xango.Text(T("dir.pin.title", "Owner keys to confirm")))}
	for i, p := range todo {
		fp := dirclient.OwnerPubFingerprint(p.pub)
		var why string
		switch p.state {
		case dirclient.OwnerChanged:
			why = T("dir.pin.changed", "A different owner key than before signed these entries. Check with %s that they changed it.", d.keysEmail)
		case dirclient.OwnerFirstSeen:
			why = T("dir.pin.firstSeen", "The same owner key has signed these entries each time you looked, but nobody has confirmed it is %s's.", d.keysEmail)
		default:
			why = T("dir.pin.unpinned", "The directory says %s's entries are signed by this owner key, and you have no other way yet to know it is theirs.", d.keysEmail)
		}
		pub := p.pub
		nodes = append(nodes,
			para(fmt.Sprintf("dir-pin-%d", i), why+" "+T("dir.pin.fingerprint", "Owner key: %s", fp), false),
			btn(fmt.Sprintf("dir-pin-confirm-%d", i), T("dir.pin.confirm", "I compared it with them: confirm this owner key"), func() {
				if d.ask == nil {
					return
				}
				d.ask(T("dir.pin.confirm", "Confirm this owner key"),
					T("dir.pin.confirmAsk", "Confirm that %s is the owner key of %s? Only do this after you compared the whole fingerprint with them another way, not from this page.", fp, d.keysEmail),
					func() { e.run(func(ctx context.Context) error { return d.confirmOwnerKey(ctx, d.keysEmail, pub) }) })
				e.render()
			}, e.pending.Busy()))
	}
	return nodes
}

// confirmOwnerKey pins pub for email, keeps the pins and looks the address up again so the rows are
// marked by what is now confirmed.
func (d *dirUI) confirmOwnerKey(ctx context.Context, email, pub string) error {
	d.dir.PinOwner(email, pub)
	b, _ := json.Marshal(d.dir.Pins())
	lsSet(pinsKey+d.url, string(b))
	keys, err := d.dir.Lookup(ctx, email)
	if err != nil {
		return dirErr(err)
	}
	d.keys, d.keysEmail = keys, email
	d.rows = d.dirRows(keys)
	d.verified, d.marks = map[string]bool{}, map[string]dirMarks{}
	for _, r := range d.rows {
		if r.Verified {
			d.verified[r.Fingerprint] = true
		}
		d.marks[r.Fingerprint] = dirMarks{Attested: r.Attested, OwnerSigned: r.OwnerSigned, OwnerEnrolled: r.OwnerEnrolled}
	}
	return nil
}

// watchPanel pins the server's log keys and watches the address.
func (d *dirUI) watchPanel(e *encUI) []xango.Node {
	busy := e.pending.Busy()
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("style", "margin:0.8em 0 0.2em;font-size:13px")}, xango.Text(T("dir.log.title", "Who changed my entry"))),
	}
	var row []xango.Node
	if len(d.owner.logKeys) == 0 {
		nodes = append(nodes, para("dir-log-state", T("dir.log.none", "The directory's log keys are not pinned, so its log cannot be checked."), false))
		row = append(row, btn("dir-log-pin", T("dir.log.pin", "Pin the server's log keys…"), func() {
			if d.ask == nil {
				return
			}
			d.ask(T("dir.log.pin", "Pin the server's log keys"),
				T("dir.log.confirmPin", "Take the log keys this server states and check its log against them from now on? That shows the server stays consistent with itself, not that it is the server you meant. If you can, compare the key IDs with ones you got from another source."),
				func() { e.run(d.pinLogKeys) })
			e.render()
		}, busy))
	} else {
		kids := make([]string, 0, len(d.owner.logKeys))
		for kid := range d.owner.logKeys {
			kids = append(kids, kid)
		}
		slices.Sort(kids)
		nodes = append(nodes, para("dir-log-state", T("dir.log.pinnedState", "Pinned log keys: %s.", strings.Join(kids, ", ")), false))
		row = append(row,
			btn("dir-log-watch", T("dir.log.watch", "Watch my address"), func() { e.run(d.watchMine) }, busy),
			btn("dir-log-unpin", T("dir.log.unpin", "Forget the pinned keys"), func() { d.forgetLogKeys(); e.render() }, busy))
	}
	if len(d.owner.alerts) > 0 {
		row = append(row, btn("dir-log-clear", T("dir.log.clear", "Dismiss the alerts"), func() { d.owner.alerts = nil; d.keepAlerts(); e.render() }, false))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")}, row...))
	if d.owner.watchMsg != "" {
		nodes = append(nodes, para("dir-log-msg", d.owner.watchMsg, false))
	}
	if len(d.owner.alerts) > 0 {
		var items []xango.Node
		for _, a := range d.owner.alerts {
			items = append(items, xango.El("li", nil, xango.Text(a)))
		}
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "dir-log-alerts"), xango.A("role", "alert"), xango.A("class", "gj-doc-error"), xango.A("style", "font-size:13px")}, items...))
	}
	return nodes
}
