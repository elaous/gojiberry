//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

const (
	ownerKeyPrefix = "showcase-enc:owner:"   // + the directory address
	watchStateKey  = "showcase-enc:watch:"   // the log position and what lookups saw
	logKeysKey     = "showcase-enc:logkeys:" // the pinned log keys
	alertsKey      = "showcase-enc:alerts:"  // what the watch found and was not dismissed
	pinsKey        = "showcase-enc:pins:"    // owner keys the person confirmed for other addresses
)

type ownerRecord struct {
	Email string `json:"email"`
	Key   string `json:"key"`
}

type ownerAlert struct {
	Kind string `json:"kind,omitempty"`
	Seq  int64  `json:"seq,omitempty"`
	Text string `json:"text"`
}

func (a *ownerAlert) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		*a = ownerAlert{}
		return json.Unmarshal(b, &a.Text)
	}
	type plain ownerAlert
	return json.Unmarshal(b, (*plain)(a))
}

func alertFor(a dirclient.Alert) ownerAlert {
	text := a.Text
	switch a.Kind {
	case dirclient.AlertReset:
		text = T("dir.alert.reset", "The owner key of your address was reset (log entry %d). The key you had before no longer signs for it. If you did not do this, somebody used a signed-in session of your account: check which keys are published and enroll a new owner key.", a.Seq)
	case dirclient.AlertResetRequest:
		text = T("dir.alert.resetRequest", "Someone asked to reset the owner key of your address (log entry %d). It takes effect after the waiting period. If it was not you, cancel the reset with your owner key before then.", a.Seq)
	}
	return ownerAlert{Kind: string(a.Kind), Seq: a.Seq, Text: text}
}

type ownerUI struct {
	key        dirclient.OwnerSigner
	plain      bool
	email      string // the address the held key was enrolled for
	secret     string
	state      string
	unenrolled bool // the server said the address has no owner key
	msg        string
	isErr      bool
	backup     bool   // the backup is shown
	restore    string // the draft of a backup being restored
	restoreO   bool   // the restore field is open

	pending     dirclient.OwnerSigner
	pendingText bool // pending is kept as text
	pendingSec  string
	resetSince  time.Time
	resetDue    time.Time

	logKeys   map[string]string
	candidate map[string]string
	expected  string
	alerts    []ownerAlert // what watching found, until dismissed
	watchMsg  string
}

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

func (d *dirUI) lookAtLogKeys(ctx context.Context) error {
	keys, err := d.dir.ServerLogKeys(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	if len(keys) == 0 {
		d.say(T("dir.log.noKeys", "The server did not state any log keys."), true)
		return nil
	}
	d.owner.candidate, d.owner.expected = keys, ""
	d.say("", false)
	return nil
}

func (d *dirUI) pinLogKeys() {
	keys := d.owner.candidate
	if want := dirclient.NormalizeFingerprint(d.owner.expected); want != "" {
		match := false
		for _, k := range keys {
			match = match || dirclient.NormalizeFingerprint(dirclient.LogKeyFingerprint(k)) == want
		}
		if !match {
			d.say(T("dir.log.mismatch", "None of the keys this server states has that fingerprint. Nothing was pinned: this may not be the server you meant."), true)
			return
		}
	}
	if err := d.dir.SetLogKeys(keys); err != nil {
		d.say(err.Error(), true)
		return
	}
	b, _ := json.Marshal(keys)
	lsSet(logKeysKey+d.url, string(b))
	d.owner.logKeys, d.owner.candidate, d.owner.expected = keys, nil, ""
	d.say(T("dir.log.pinned", "The log keys are pinned: later looks at the log are checked against them."), false)
}

func (d *dirUI) forgetLogKeys() {
	_ = d.dir.SetLogKeys(nil)
	lsSet(logKeysKey+d.url, "")
	d.owner.logKeys = nil
}

func (d *dirUI) watchMine(ctx context.Context) error {
	me, err := d.dir.Me(ctx)
	if err != nil {
		d.say(dirErr(err).Error(), true)
		return nil
	}
	var owner dirclient.OwnerSigner
	if d.owner.email == me {
		owner = d.owner.key
	}
	rep, err := d.dir.Watch(ctx, me, owner)
	switch {
	case errors.Is(err, dirclient.ErrNoLogKeys):
		d.say(T("dir.log.needKeys", "Pin the server's log keys first."), true)
		return nil
	case errors.Is(err, dirclient.ErrLogRewritten):
		d.owner.alerts = append(d.owner.alerts, ownerAlert{Text: T("dir.log.rewritten", "The directory's log was cut back or rewritten since the last look. Do not trust this directory until you know why.")})
		d.keepAlerts()
		d.say("", false)
		return nil
	case err != nil:
		d.say(dirErr(err).Error(), true)
		return nil
	}
	for _, a := range rep.Alerts {
		d.owner.alerts = append(d.owner.alerts, alertFor(a))
	}
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

func (d *dirUI) loadOwner() {
	d.owner.key, d.owner.plain, d.owner.email, d.owner.secret = nil, false, "", ""
	d.owner.state, d.owner.msg, d.owner.isErr, d.owner.backup = "", "", false, false
	d.owner.restore, d.owner.restoreO = "", false
	d.loadPending()
	url := d.url
	if url == "" {
		return
	}
	if raw := lsGet(ownerKeyPrefix + url); raw != "" {
		var rec ownerRecord
		if json.Unmarshal([]byte(raw), &rec) == nil && rec.Email != "" {
			if k, err := dirclient.ImportOwnerKey(rec.Key); err == nil {
				d.owner.key, d.owner.email, d.owner.plain = k, rec.Email, true
				if d.dir != nil {
					d.dir.Pin(rec.Email, k)
				}
			}
		}
	}
	if d.render == nil {
		return
	}
	go func() {
		current := func() bool { return d.url == url }
		if email, w, ok, err := webOwnerLoad(url); err == nil && ok && current() {
			d.owner.key, d.owner.email, d.owner.plain = w, email, false
			lsSet(ownerKeyPrefix+url, "")
			if d.dir != nil {
				d.dir.Pin(email, w)
			}
		} else if d.owner.plain && current() {
			if k, ok := d.owner.key.(dirclient.OwnerKey); ok {
				if w, err := webOwnerImport(k); err == nil && webOwnerSave(url, d.owner.email, w) == nil {
					d.owner.key, d.owner.plain = w, false
					lsSet(ownerKeyPrefix+url, "")
					clear(k)
					d.dir.Pin(d.owner.email, w)
				}
			}
		}
		if d.render != nil {
			d.render()
		}
	}()
}

func signerFor(k dirclient.OwnerKey) (signer dirclient.OwnerSigner, plain bool) {
	if w, err := webOwnerImport(k); err == nil {
		return w, false
	}
	return k, true
}

func (d *dirUI) keepOwner(email string, signer dirclient.OwnerSigner, plain bool, k dirclient.OwnerKey, made bool) {
	if !plain {
		if w, ok := signer.(*webKey); ok && webOwnerSave(d.url, email, w) != nil {
			signer, plain = k, true
		}
	}
	if plain {
		b, _ := json.Marshal(ownerRecord{Email: email, Key: dirclient.ExportOwnerKey(k)})
		lsSet(ownerKeyPrefix+d.url, string(b))
	} else {
		lsSet(ownerKeyPrefix+d.url, "")
		if made {
			d.owner.secret = dirclient.ExportOwnerKey(k)
		}
		clear(k)
	}
	d.owner.key, d.owner.email, d.owner.plain = signer, email, plain
	if d.dir != nil {
		d.dir.Pin(email, signer)
	}
}

func (d *dirUI) backupText() string {
	if k, ok := d.owner.key.(dirclient.OwnerKey); ok && d.owner.plain {
		return dirclient.ExportOwnerKey(k)
	}
	return d.owner.secret
}

func (d *dirUI) forgetOwner() {
	_ = webOwnerDelete(d.url)
	lsSet(ownerKeyPrefix+d.url, "")
	d.owner = ownerUI{}
}

func (d *dirUI) say(msg string, isErr bool) { d.owner.msg, d.owner.isErr = msg, isErr }

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
	d.owner.unenrolled = !en.Enrolled
	d.owner.resetSince, d.owner.resetDue = en.ResetSince, en.ResetDue
	if !en.ResetPending {
		d.dropPending()
	}
	d.say("", false)
	return nil
}

func (d *dirUI) ownerState(me string, en dirclient.Enrollment) string {
	held := d.owner.key != nil && d.owner.email == me && en.Current == dirclient.OwnerFingerprint(d.owner.key)
	switch {
	case en.ResetPending:
		return T("dir.owner.resetPending", "%s has an owner key reset waiting since %s: it can be completed from %s. Until then the old key still publishes, and its holder can cancel the reset.", me, en.ResetSince.UTC().Format("2006-01-02 15:04 UTC"), en.ResetDue.UTC().Format("2006-01-02 15:04 UTC"))
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
	signer, plain := signerFor(k)
	if err := d.dir.Enroll(ctx, me, signer); err != nil {
		clear(k)
		if errors.Is(err, dirclient.ErrAlreadyEnrolled) {
			d.say(T("dir.owner.already", "%s already has an owner key. If it is yours, restore it from its backup. If it is not, somebody enrolled before you: the directory's log shows when.", me), true)
			return nil
		}
		d.say(dirErr(err).Error(), true)
		return nil
	}
	d.keepOwner(me, signer, plain, k, true)
	d.owner.backup = true
	d.say(T("dir.owner.enrolled", "Owner key enrolled for %s. Back it up now: if this browser's storage is cleared, only the backup can publish for this address again.", me), false)
	return d.checkOwner(ctx)
}

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
	if en.Current != dirclient.OwnerFingerprint(k) {
		clear(k)
		d.say(T("dir.owner.notOwner", "That key is not an owner key of %s.", me), true)
		return nil
	}
	signer, plain := signerFor(k)
	d.keepOwner(me, signer, plain, k, false)
	d.owner.restore, d.owner.restoreO = "", false
	d.say(T("dir.owner.restored", "Owner key restored for %s.", me), false)
	return d.checkOwner(ctx)
}

func (d *dirUI) ownerPanel(e *encUI) []xango.Node {
	if d.dir == nil {
		return nil
	}
	busy := e.pending.Busy()
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("style", "margin:0.8em 0 0.2em;font-size:13px")}, xango.Text(T("dir.owner.title", "Owner key for your address"))),
		para("dir-owner-explain", T("dir.owner.explain", "An owner key makes the directory take entries for your address only when this key signed them, so a stolen session cannot publish or revoke for you."), false),
	}
	switch {
	case d.owner.key != nil && !d.owner.plain:
		nodes = append(nodes, para("dir-owner-storage", T("dir.owner.storageWeb", "This browser keeps the owner key so that it cannot be copied out of it. Its backup is shown once, when the key is made."), false))
	case d.owner.key != nil:
		nodes = append(nodes, para("dir-owner-storage", T("dir.owner.storagePlain", "This browser cannot keep the owner key out of reach, so it is kept as text in this site's storage, where any script of the page could read it. Treat the page like a password manager: only use it on a device and a site you trust."), true))
	}
	if d.owner.state != "" {
		nodes = append(nodes, para("dir-owner-state", d.owner.state, false))
	}
	if d.owner.unenrolled {
		nodes = append(nodes, para("dir-owner-nudge", T("dir.owner.nudge", "Until you make an owner key, only the directory server's word ties the keys published for this address to you: a server that holds its own key could number entries for it as it likes, and anyone who signs in as the address can publish. Make an owner key now."), true))
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
			btn("dir-owner-backup", T("dir.owner.backup", "Back up the owner key"), func() { d.owner.backup = !d.owner.backup; e.render() }, d.backupText() == ""),
			btn("dir-owner-forget", T("dir.owner.forget", "Forget it in this browser"), func() {
				if d.ask == nil {
					return
				}
				d.ask(T("dir.owner.forget", "Forget it in this browser"),
					T("dir.owner.confirmForget", "Forget the owner key in this browser? The address keeps its owner key on the server and will refuse entries without it. You can restore the key only from its backup."),
					func() { e.run(func(context.Context) error { d.forgetOwner(); return nil }) })
				e.render()
			}, busy))
	}
	more, notes := d.replacePanel(e, busy)
	row = append(row, more...)
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")}, row...))
	nodes = append(nodes, notes...)
	if d.owner.key != nil && !d.owner.plain && d.owner.secret == "" {
		nodes = append(nodes, para("dir-owner-nobackup", T("dir.owner.noBackup", "The backup of this owner key was shown when it was made and is not kept. If you did not save it, make a new owner key; the address needs its reset to replace this one."), false))
	}
	if secret := d.backupText(); d.owner.key != nil && d.owner.backup && secret != "" {
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "dir-owner-backup-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
			para("dir-owner-backup-warning", T("dir.owner.backupWarning", "This is the owner key. Whoever has it can publish and revoke keys for your address. Keep it offline, like a password."), true),
			textarea("dir-owner-secret", T("dir.owner.secret", "Owner key (secret)"), secret, "", "2", func(string) {}),
			xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
				btn("dir-owner-download", T("enc.download", "Download"), func() { transfer.Download("owner-key.txt", "text/plain", []byte(secret+"\n")) }, false),
				btn("dir-owner-hide", T("dir.owner.hide", "Hide it"), func() {
					if d.owner.plain || d.ask == nil {
						d.owner.backup = false
						e.render()
						return
					}
					d.ask(T("dir.owner.hide", "Hide it"),
						T("dir.owner.confirmHide", "Hide the backup? It will not be shown again: this browser keeps the owner key where it cannot be copied out. Have you saved it?"),
						func() { d.owner.backup, d.owner.secret = false, ""; e.render() })
					e.render()
				}, false))))
	}
	if d.owner.key == nil && d.owner.restoreO {
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "dir-owner-restore-box"), xango.A("style", "margin:8px 0")},
			textarea("dir-owner-restore", T("dir.owner.restoreLabel", "Owner key backup"), d.owner.restore, T("dir.owner.restorePlaceholder", "Paste the backup text"), "2", func(v string) { d.owner.restore = v }),
			xango.El("div", []xango.Attr{xango.A("style", "margin-top:6px")},
				btn("dir-owner-restore-go", T("dir.owner.restore", "Restore"), func() { e.run(d.restoreOwner) }, busy))))
	}
	return append(nodes, d.watchPanel(e)...)
}

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

func (d *dirUI) watchPanel(e *encUI) []xango.Node {
	busy := e.pending.Busy()
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("style", "margin:0.8em 0 0.2em;font-size:13px")}, xango.Text(T("dir.log.title", "Who changed my entry"))),
	}
	var row []xango.Node
	if len(d.owner.logKeys) == 0 {
		nodes = append(nodes, para("dir-log-state", T("dir.log.none", "The directory's log keys are not pinned, so its log cannot be checked."), false))
		if d.owner.candidate == nil {
			row = append(row, btn("dir-log-pin", T("dir.log.pin", "Look at the server's log keys…"), func() { e.run(d.lookAtLogKeys) }, busy))
		} else {
			kids := make([]string, 0, len(d.owner.candidate))
			for kid := range d.owner.candidate {
				kids = append(kids, kid)
			}
			slices.Sort(kids)
			var lines []xango.Node
			for _, kid := range kids {
				lines = append(lines, xango.El("li", nil, xango.Text(kid+": "+dirclient.LogKeyFingerprint(d.owner.candidate[kid]))))
			}
			nodes = append(nodes,
				para("dir-log-compare", T("dir.log.compare", "This server states these log keys. Pinning them makes later looks at its log check against them; that shows the server stays consistent with itself, not that it is the server you meant. If you have a fingerprint from another source (the operator's page, a call), type it below and the keys are pinned only if one of them matches."), false),
				xango.El("ul", []xango.Attr{xango.A("id", "dir-log-candidates"), xango.A("style", "font-family:monospace;font-size:12px")}, lines...),
				xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("id", "dir-log-expected"), xango.A("value", d.owner.expected), xango.A("autocomplete", "off"),
					xango.A("aria-label", T("dir.log.expected", "Fingerprint to hold the keys to (optional)")), xango.A("placeholder", T("dir.log.expected", "Fingerprint to hold the keys to (optional)")),
					xango.A("style", "width:100%;box-sizing:border-box;font-size:12px;margin:2px 0"),
					xango.On("input", func(ev xango.Event) { d.owner.expected = xango.EventValue[string](ev) })}))
			row = append(row,
				btn("dir-log-pin-confirm", T("dir.log.pinThese", "Pin these keys"), func() { d.pinLogKeys(); e.render() }, busy),
				btn("dir-log-pin-cancel", T("dir.log.pinCancel", "Cancel"), func() { d.owner.candidate, d.owner.expected = nil, ""; e.render() }, busy))
		}
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
			attrs := []xango.Attr{}
			if a.Kind != "" {
				attrs = append(attrs, xango.A("data-kind", a.Kind))
			}
			items = append(items, xango.El("li", attrs, xango.Text(a.Text)))
		}
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "dir-log-alerts"), xango.A("role", "alert"), xango.A("class", "gj-doc-error"), xango.A("style", "font-size:13px")}, items...))
	}
	return nodes
}
