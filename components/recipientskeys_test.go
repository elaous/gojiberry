package components

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/xango"
)

func clickAll(n xango.Node) {
	for _, h := range findHandlers(n, "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
}

func TestMyPasskeyIsHiddenUnsupportedOrReady(t *testing.T) {
	var calls int
	p := RecipientsProps{Pending: &Pending{Inline: true}}
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-my", "Add my passkey")

	p.AddMyPasskey = func(context.Context) error { calls++; return nil }
	html := render(t, Recipients(p))
	mustContain(t, html, "gj-recip-unsupported", "does not support the PRF extension")
	mustNotContain(t, html, "gj-recip-add-mine")

	p.PasskeySupported = true
	n := Recipients(p)
	mustContain(t, render(t, n), "Add my passkey as a key", "gj-recip-add-mine", "nothing is typed")
	mustNotContain(t, render(t, n), "gj-recip-need-second", "gj-recip-recovery")
	clickAll(n)
	if calls != 1 {
		t.Errorf("AddMyPasskey called %d times", calls)
	}
	// After it worked the host shows the fingerprint that was added.
	p.MyPasskeyAdded = &RecipientRow{Type: "passkey-prf", Fingerprint: "SHA256:mine"}
	mustContain(t, render(t, Recipients(p)), "Your passkey was added", "SHA256:mine")
	// A document that is not confidential never asks for a second key.
	p.Owner = []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}}
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-need-second")
}

func TestMyPasskeyNeedsASecondKeyOnAConfidentialDocument(t *testing.T) {
	var created int
	var shown string
	p := RecipientsProps{
		AddMyPasskey: func(context.Context) error { t.Error("must not add"); return nil }, PasskeySupported: true, Confidential: true,
		Owner:            []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}},
		SecondKeyMessage: "encrypt: a passkey cannot be the only key: add a recovery key or an SSH key too",
		CreateRecoveryKey: func(context.Context) (string, error) {
			created++
			return "AGE-SECRET-KEY-1TEST", nil
		},
		OnRecoveryKey: func(s string) { shown = s },
		Pending:       &Pending{Inline: true},
	}
	n := Recipients(p)
	html := render(t, n)
	mustContain(t, html, "a passkey cannot be the only key", `role="alert"`, "Create a recovery key", "gj-recip-add-mine")
	if !strings.Contains(html, `class="gj-recip-add-mine" disabled="disabled"`) {
		t.Errorf("the passkey button is off: %s", html)
	}
	clickAll(n) // the disabled button has no handler: only Create a recovery key acts
	if created != 1 || shown != "AGE-SECRET-KEY-1TEST" {
		t.Fatalf("created %d, shown %q", created, shown)
	}
	// The secret never appears until the host passes it back.
	mustNotContain(t, html, "AGE-SECRET-KEY")

	// A key that is not a passkey among the owner's keys lifts the rule.
	p.Owner = append(p.Owner, RecipientRow{Type: "age-x25519", Fingerprint: "SHA256:rec"})
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-need-second")
	// So do no owner keys at all? No: nothing is not a recovery key.
	p.Owner = nil
	mustContain(t, render(t, Recipients(p)), "gj-recip-need-second")
	// An empty message falls back to the default wording.
	p.SecondKeyMessage = ""
	mustContain(t, render(t, Recipients(p)), "A passkey cannot be the only key")
}

func TestRecoveryKeyIsShownOnceWithAKeepOfflineWarning(t *testing.T) {
	var copied, downloaded string
	var done int
	p := RecipientsProps{
		AddMyPasskey: func(context.Context) error { return nil }, PasskeySupported: true, Confidential: true,
		Owner:             []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}},
		CreateRecoveryKey: func(context.Context) (string, error) { return "x", nil },
		RecoverySecret:    "AGE-SECRET-KEY-1ABC",
		Copy:              func(s string) { copied = s },
		Download:          func(name, s string) { downloaded = name + ":" + s },
		OnRecoveryDone:    func() { done++ },
		Pending:           &Pending{Inline: true},
	}
	n := Recipients(p)
	html := render(t, n)
	mustContain(t, html, "AGE-SECRET-KEY-1ABC", "shown only now", "Keep it offline", "We cannot recover it", "readonly", "Copy", "Download", "I have saved it")
	mustNotContain(t, html, "Create a recovery key") // one is being shown
	clickAll(n)
	if copied != "AGE-SECRET-KEY-1ABC" || downloaded != "recovery-key.txt:AGE-SECRET-KEY-1ABC\n" || done != 1 {
		t.Errorf("copied %q downloaded %q done %d", copied, downloaded, done)
	}
	// Once the host drops it, the secret is gone from the page.
	p.RecoverySecret = ""
	mustNotContain(t, render(t, Recipients(p)), "AGE-SECRET-KEY", "gj-recip-secret")
	// Without Copy and Download those buttons are left out.
	p.RecoverySecret, p.Copy, p.Download = "AGE-SECRET-KEY-1ABC", nil, nil
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-copy", "gj-recip-download")
}

func TestMyPasskeyShowsAnError(t *testing.T) {
	p := RecipientsProps{PasskeySupported: true, Pending: &Pending{Inline: true},
		AddMyPasskey: func(context.Context) error { return errors.New("the passkey prompt was cancelled") }}
	clickAll(Recipients(p))
	mustContain(t, render(t, Recipients(p)), `role="alert"`, "the passkey prompt was cancelled")
}

func TestVerifiedMarkAndDirectoryPicker(t *testing.T) {
	var looked string
	var results []DirRow
	var picked DirRow
	p := RecipientsProps{
		Owner:   []RecipientRow{{Label: "ana", Type: "ssh-ed25519", Fingerprint: "SHA256:owner"}},
		Shared:  []RecipientRow{{Label: "bo@x.io", Type: "age-x25519", Fingerprint: "SHA256:bo", Verified: true}, {Label: "cy", Type: "age-x25519", Fingerprint: "SHA256:cy"}},
		Pending: &Pending{Inline: true},
		Add:     func(_ context.Context, k, c string) error { return nil },
		Directory: func(_ context.Context, email string) ([]DirRow, error) {
			looked = email
			return []DirRow{{Label: email, Type: "age-x25519", Fingerprint: "SHA256:v", Key: "age1v", Verified: true},
				{Label: email, Type: "ssh-ed25519", Fingerprint: "SHA256:l", Key: "ssh-ed25519 AAAA", Verified: false}}, nil
		},
		OnDirResults: func(_ string, rows []DirRow) { results = rows },
		OnPick:       func(r DirRow) { picked = r },
	}
	html := render(t, Recipients(p))
	// The list marks verified keys; label-only keys carry no mark there (already confirmed).
	if strings.Count(html, "verified e-mail") != 1 {
		t.Errorf("one verified mark in the list: %s", html)
	}
	mustContain(t, html, "gj-recip-dir-email", "Find keys")
	// Find is off until an address is typed.
	mustContain(t, html, `class="gj-recip-dir-find" disabled="disabled"`)
	p.DirEmail = " bo@x.io "
	clickAll(Recipients(p))
	if looked != "bo@x.io" || len(results) != 2 {
		t.Fatalf("looked %q results %v", looked, results)
	}
	p.DirResults, p.DirSearched = results, true
	n := Recipients(p)
	html = render(t, n)
	mustContain(t, html, "gj-recip-dir-results", "verified e-mail", "label only: confirm the fingerprint", "Use this key")
	var picks []xango.EventHandler
	for _, h := range findHandlers(n, "click") {
		picks = append(picks, h)
	}
	picks[len(picks)-1](xango.NewEvent("click", nil, false, nil, nil)) // second row's Use this key
	if picked.Fingerprint != "SHA256:l" {
		t.Errorf("picked %+v", picked)
	}
	// Nothing found.
	p.DirResults = nil
	mustContain(t, render(t, Recipients(p)), "No keys are published for that address", "label only")
	// Without Directory there is no picker.
	p.Directory = nil
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-dir")
}

func TestPickWithoutOnPickFillsTheDraft(t *testing.T) {
	var key, conf string
	p := RecipientsProps{Pending: &Pending{Inline: true}, Add: func(context.Context, string, string) error { return nil },
		Directory:  func(context.Context, string) ([]DirRow, error) { return nil, nil },
		DirResults: []DirRow{{Type: "age-x25519", Fingerprint: "SHA256:v", Key: "age1v", Verified: true}},
		OnDraft:    func(k, c string) { key, conf = k, c }}
	n := Recipients(p)
	hs := findHandlers(n, "click")
	hs[len(hs)-1](xango.NewEvent("click", nil, false, nil, nil)) // the pick button (Find and Add are off)
	if key != "age1v" || conf != "" {
		t.Errorf("draft %q %q", key, conf)
	}
}

func TestVerifiedKeySkipsTypingOnlyWhenTheHostTrustsIt(t *testing.T) {
	var added []string
	p := RecipientsProps{
		Pending: &Pending{Inline: true}, Draft: "age1v",
		Parsed: &RecipientRow{Label: "bo@x.io", Type: "age-x25519", Fingerprint: "SHA256:v", Verified: true},
		Add:    func(_ context.Context, k, c string) error { added = append(added, k+"|"+c); return nil },
	}
	// Default: verified or not, the fingerprint is typed.
	html := render(t, Recipients(p))
	mustContain(t, html, "verified e-mail", "gj-recip-confirm", "Check this fingerprint")
	if !disabledAdd(t, p) {
		t.Error("typing is still required without TrustVerified")
	}
	// TrustVerified: the fingerprint is shown, nothing is typed, and Add confirms with it.
	p.TrustVerified = true
	html = render(t, Recipients(p))
	mustContain(t, html, "SHA256:v", "verified e-mail")
	mustNotContain(t, html, "gj-recip-confirm", "Check this fingerprint")
	if disabledAdd(t, p) {
		t.Error("a trusted verified key can be added")
	}
	clickAll(Recipients(p))
	if len(added) != 1 || added[0] != "age1v|SHA256:v" {
		t.Errorf("added %v", added)
	}
	// A label-only key still needs the typed fingerprint, trusted or not.
	p.Parsed = &RecipientRow{Label: "cy", Type: "age-x25519", Fingerprint: "SHA256:l"}
	html = render(t, Recipients(p))
	mustContain(t, html, "label only: confirm the fingerprint", "gj-recip-confirm")
	if !disabledAdd(t, p) {
		t.Error("label-only keys need the typed fingerprint")
	}
}

func TestRecoveryKeyButtonNeedsAHostThatCanShowTheSecret(t *testing.T) {
	p := RecipientsProps{
		AddMyPasskey: func(context.Context) error { return nil }, PasskeySupported: true, Confidential: true,
		Owner: []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}},
		CreateRecoveryKey: func(context.Context) (string, error) {
			t.Error("must not create a key nobody can see")
			return "x", nil
		},
		Pending: &Pending{Inline: true},
	}
	n := Recipients(p)
	mustNotContain(t, render(t, n), "Create a recovery key", "gj-recip-recovery\"")
	clickAll(n)
	p.OnRecoveryKey = func(string) {}
	p.CreateRecoveryKey = func(context.Context) (string, error) { return "x", nil }
	mustContain(t, render(t, Recipients(p)), "Create a recovery key")
}

// A recovery key whose secret is shown before the store commits it: the key is not added until the
// person says they saved the secret and confirms; cancelling adds nothing.
func TestRecoveryKeyIsCommittedOnlyAfterTheSecretIsAcknowledged(t *testing.T) {
	var committed, cancelled, acked int
	p := RecipientsProps{
		AddMyPasskey: func(context.Context) error { return nil }, PasskeySupported: true, Confidential: true,
		Owner:                 []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}},
		CreateRecoveryKey:     func(context.Context) (string, error) { return "x", nil },
		RecoverySecret:        "AGE-SECRET-KEY-1ABC",
		CommitRecoveryKey:     func(context.Context) error { committed++; return nil },
		CancelRecoveryKey:     func() { cancelled++ },
		OnRecoveryAcknowledge: func(bool) { acked++ },
		OnRecoveryDone:        func() { t.Error("the older done button is shown with a commit step") },
		Pending:               &Pending{Inline: true},
	}
	html := render(t, Recipients(p))
	mustContain(t, html, "AGE-SECRET-KEY-1ABC", "I have saved this recovery key somewhere safe", "Add this recovery key", "Cancel: do not add it")
	mustNotContain(t, html, "gj-recip-recovery-done")
	if !strings.Contains(html, `class="gj-recip-recovery-commit" disabled="disabled"`) {
		t.Fatalf("the commit button is on before the acknowledgement: %s", html)
	}
	n := Recipients(p)
	for _, h := range findHandlers(n, "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if committed != 0 || cancelled != 1 {
		t.Fatalf("committed %d cancelled %d: only Cancel may act before the acknowledgement", committed, cancelled)
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.NewEvent("change", nil, true, nil, nil))
	}
	if acked != 1 {
		t.Fatalf("acknowledgement not reported: %d", acked)
	}
	p.RecoveryAcknowledged = true
	n = Recipients(p)
	if strings.Contains(render(t, n), `class="gj-recip-recovery-commit" disabled`) {
		t.Fatal("the commit button stays off after the acknowledgement")
	}
	for _, h := range findHandlers(n, "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if committed != 1 {
		t.Fatalf("committed %d", committed)
	}
}

func TestOwnerKeysCanBeRemovedOrReplacedAfterAsking(t *testing.T) {
	var removed, replaced, asked []string
	p := RecipientsProps{
		Owner: []RecipientRow{
			{Label: "My passkey", Type: "passkey-prf", Fingerprint: "SHA256:p1", CanRemove: false, RemoveBlocked: "Keep a recovery key or an SSH key."},
			{Label: "Recovery key", Type: "age-x25519", Fingerprint: "SHA256:r1", CanRemove: true},
		},
		RemoveOwner:  func(_ context.Context, fp string) error { removed = append(removed, fp); return nil },
		ReplaceOwner: func(_ context.Context, fp string) error { replaced = append(replaced, fp); return nil },
		Ask:          func(m string, do func()) { asked = append(asked, m); do() },
		Pending:      &Pending{Inline: true},
	}
	n := Recipients(p)
	html := render(t, n)
	mustContain(t, html, "Remove this key", "Replace this passkey", "Keep a recovery key or an SSH key.", `aria-label="Remove the key Recovery key"`, `aria-label="Replace the passkey My passkey"`)
	if c := strings.Count(html, "gj-recip-replace"); c != 1 {
		t.Errorf("replace is for passkeys only: %d buttons", c)
	}
	clickAll(n)
	if !slices.Equal(removed, []string{"SHA256:r1"}) || !slices.Equal(replaced, []string{"SHA256:p1"}) {
		t.Fatalf("removed %v replaced %v (a blocked key must not be removable)", removed, replaced)
	}
	all := strings.Join(asked, "\n")
	mustContain(t, all, "encrypted again without it", "keeps what they already read", "encrypted again for it", "stays in your authenticator")
	// Without the operations no control is drawn.
	p.RemoveOwner, p.ReplaceOwner = nil, nil
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-remove-owner", "gj-recip-replace")
}

func TestPublishMyKeysIsOfferedWithoutADocument(t *testing.T) {
	published := 0
	p := RecipientsProps{
		Publish: func(context.Context) error { published++; return nil }, PublishNotice: "Your keys were published.",
		Pending: &Pending{Inline: true},
	}
	n := Recipients(p)
	mustContain(t, render(t, n), "Publish my keys", "Your keys were published.", `role="status"`)
	clickAll(n)
	if published != 1 {
		t.Fatalf("published %d", published)
	}
	p.Publish = nil
	mustNotContain(t, render(t, Recipients(p)), "gj-recip-publish\"")
}

func TestRecoveryKeyIsOfferedBesidesAnSSHKeyToo(t *testing.T) {
	var shown string
	p := RecipientsProps{
		AddMyPasskey: func(context.Context) error { return nil }, PasskeySupported: true, Confidential: true,
		Owner:             []RecipientRow{{Type: "passkey-prf", Fingerprint: "SHA256:p1"}, {Type: "ssh-ed25519", Fingerprint: "SHA256:s1"}},
		CreateRecoveryKey: func(context.Context) (string, error) { return "AGE-SECRET-KEY-1ZZZ", nil },
		OnRecoveryKey:     func(s string) { shown = s },
		Pending:           &Pending{Inline: true},
	}
	n := Recipients(p)
	mustContain(t, render(t, n), "Create a recovery key", "gj-recip-add-mine")
	for _, h := range findHandlers(n, "click") {
		h(xango.NewEvent("click", nil, false, nil, nil))
	}
	if shown != "AGE-SECRET-KEY-1ZZZ" {
		t.Fatalf("%q", shown)
	}
}
