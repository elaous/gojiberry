package encstore

import (
	"html"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore/encrypt"
)

func RecoveryFingerprint(secret string) (string, error) {
	ident, err := encrypt.ParseAgeIdentity(strings.TrimSpace(secret))
	if err != nil {
		return "", err
	}
	xid, ok := ident.(*age.X25519Identity)
	if !ok {
		return "", ErrNotAKey
	}
	return Owner{Kind: KindRecovery, Type: "age-x25519", Public: xid.Recipient().String()}.Fingerprint(), nil
}

type SheetText struct {
	Title, Intro      string
	KeyLabel, Finger  string
	Keep, Lost, Print string
	Date              string
	QRAlt             string
}

func RecoverySheet(t SheetText, label, secret, fingerprint string) []byte {
	var b strings.Builder
	e := html.EscapeString
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>")
	b.WriteString(e(t.Title))
	b.WriteString("</title><style>")
	b.WriteString("body{font:18px/1.5 system-ui,sans-serif;color:#000;background:#fff;max-width:40em;margin:2em auto;padding:0 1em}")
	b.WriteString("h1{font-size:1.5em}code{display:block;font:bold 1.15em/1.6 ui-monospace,monospace;word-break:break-all;border:2px solid #000;padding:.6em;margin:.5em 0}")
	b.WriteString(".warn{border-left:6px solid #000;padding-left:.8em}button{font:inherit;padding:.4em 1em}@media print{button{display:none}}")
	b.WriteString("</style></head><body><h1>")
	b.WriteString(e(t.Title))
	b.WriteString("</h1><p>")
	b.WriteString(e(t.Intro))
	b.WriteString("</p><p><strong>")
	b.WriteString(e(t.KeyLabel))
	if label != "" {
		b.WriteString(" (" + e(label) + ")")
	}
	b.WriteString("</strong></p><code>")
	b.WriteString(e(secret))
	b.WriteString("</code>")
	if t.QRAlt != "" {
		if svg, err := RecoveryQR(secret, t.QRAlt); err == nil {
			b.WriteString("<p>" + svg + "</p>")
		}
	}
	b.WriteString("<p>")
	b.WriteString(e(t.Finger))
	b.WriteString(": <strong>")
	b.WriteString(e(fingerprint))
	b.WriteString("</strong></p><p class=\"warn\">")
	b.WriteString(e(t.Keep))
	b.WriteString(" ")
	b.WriteString(e(t.Lost))
	b.WriteString("</p>")
	if t.Date != "" {
		b.WriteString("<p>" + e(t.Date) + "</p>")
	}
	b.WriteString("<p><button id=\"print\">")
	b.WriteString(e(t.Print))
	b.WriteString("</button></p><script>document.getElementById('print').onclick=function(){window.print()};window.onload=function(){window.print()}</script></body></html>")
	return []byte(b.String())
}

const ReminderEvery = 90 * 24 * time.Hour

// SnoozeFor is how long "remind me later" is quiet.
const SnoozeFor = 7 * 24 * time.Hour

func CheckDue(now, lastChecked, snoozedUntil time.Time, every time.Duration) bool {
	if now.Before(snoozedUntil) {
		return false
	}
	return lastChecked.IsZero() || now.Sub(lastChecked) >= every
}
