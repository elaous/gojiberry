package encrypt

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

type ownerEnv struct {
	t     *testing.T
	d     *AdapterDirectory
	store docstore.Adapter
	clk   *testClock
}

func newOwnerEnv(t *testing.T) *ownerEnv {
	t.Helper()
	d, store, clk := newTestDirectory(t)
	return &ownerEnv{t: t, d: d, store: store, clk: clk}
}

func ownerKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func (e *ownerEnv) pos() string {
	e.t.Helper()
	h, err := e.d.Head(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return FormatLogPosition(h.Seq, h.Hash)
}

func (e *ownerEnv) sign(priv ed25519.PrivateKey, op, fp string) *docstore.DirectoryOwnerSig {
	if op == OpPublish { // a publication is signed over its label too (version 2); these keys have none
		s := SignOwnerPublish(priv, "ana@x.io", fp, "", e.clk.Now(), e.pos())
		return &s
	}
	s := SignOwnerOp(priv, op, "ana@x.io", fp, e.clk.Now(), e.pos())
	return &s
}

func (e *ownerEnv) enroll(mode string, priv, old ed25519.PrivateKey) (docstore.DirectoryEnrollment, error) {
	e.t.Helper()
	ch, err := e.d.EnrollChallenge(context.Background(), ana)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.d.Enroll(context.Background(), ana, e.enrollRequest(ch, mode, priv, old))
}

func (e *ownerEnv) enrollRequest(ch docstore.DirectoryEnrollChallenge, mode string, priv, old ed25519.PrivateKey) docstore.DirectoryEnroll {
	pub := priv.Public().(ed25519.PublicKey)
	fp := OwnerFingerprint(pub)
	at := time.Unix(ch.Time, 0)
	req := docstore.DirectoryEnroll{Mode: mode, OwnerPub: FormatOwnerPub(pub), Token: ch.Token, Time: ch.Time, Prev: ch.Prev}
	switch mode {
	case OpEnroll:
		req.Sig = SignOwnerOp(priv, OpEnroll, "ana@x.io", fp, at, ch.Prev).Sig
	case OpRotate:
		req.Sig = SignOwnerOp(old, OpRotate, "ana@x.io", fp, at, ch.Prev).Sig
		req.Proof = SignOwnerOp(priv, "accept", "ana@x.io", fp, at, ch.Prev).Sig
	case OpReset:
		req.Proof = SignOwnerOp(priv, "accept", "ana@x.io", fp, at, ch.Prev).Sig
	}
	return req
}

func (e *ownerEnv) mustEnroll(priv ed25519.PrivateKey) {
	e.t.Helper()
	if _, err := e.enroll(OpEnroll, priv, nil); err != nil {
		e.t.Fatal(err)
	}
}

func (e *ownerEnv) publish(priv ed25519.PrivateKey, k Recipient) (PublishedKey, error) {
	e.t.Helper()
	opt := PublishOptions{}
	if priv != nil {
		opt.Owner = e.sign(priv, OpPublish, k.Fingerprint)
	}
	return e.d.PublishWith(context.Background(), ana, k, opt)
}

func (e *ownerEnv) lookup() map[string]PublishedKey {
	e.t.Helper()
	got, err := e.d.LookupWith(context.Background(), "ana@x.io", LookupOptions{IncludeRevoked: true})
	if err != nil {
		e.t.Fatal(err)
	}
	m := map[string]PublishedKey{}
	for _, k := range got {
		m[k.Fingerprint] = k
	}
	return m
}

func (e *ownerEnv) audit(opt AuditOptions) AddressAudit {
	e.t.Helper()
	l, err := e.d.ReadLog(context.Background(), 0, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := VerifyLog(l.Entries, "", 0, logKeys(e.t, e.d)); err != nil {
		e.t.Fatal(err)
	}
	return AuditAddress("ana@x.io", e.d.LogAddressesFor("ana@x.io"), l.Entries, opt)
}

func TestOwnerEnrolledAddressNeedsTheOwnersSignatureAndLookupMarksIt(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	pub, priv := ownerKey(t)
	view, err := e.enroll(OpEnroll, priv, nil)
	if err != nil || !view.Enrolled || len(view.Keys) != 1 || view.Keys[0].Pub != FormatOwnerPub(pub) || view.Keys[0].Seq != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	if got, _ := e.d.Enrollment(ctx, "ana@x.io"); !got.Enrolled || got.Keys[0].Pub != view.Keys[0].Pub {
		t.Errorf("%+v", got)
	}
	if got, _ := e.d.Enrollment(ctx, "bo@x.io"); got.Enrolled {
		t.Errorf("another address: %+v", got)
	}

	age1, _ := ageKey(t)
	ssh, _, _ := edSSHKey(t)
	_ = ssh
	// The session alone no longer publishes.
	if _, err := e.publish(nil, age1); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("no signature: %v", err)
	}
	_, other := ownerKey(t)
	if _, err := e.publish(other, age1); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("another key's signature: %v", err)
	}
	wrong := e.sign(priv, OpRevoke, age1.Fingerprint)
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: wrong}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("a revoke signature used to publish: %v", err)
	}
	k2 := newRecovery(t)
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: e.sign(priv, OpPublish, k2.Fingerprint)}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("a signature for another key: %v", err)
	}
	forAddr := SignOwnerPublish(priv, "bo@x.io", age1.Fingerprint, "", e.clk.Now(), e.pos())
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: &forAddr}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("a signature for another address: %v", err)
	}
	stale := SignOwnerPublish(priv, "ana@x.io", age1.Fingerprint, "", e.clk.Now().Add(-11*time.Minute), e.pos())
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: &stale}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("stale: %v", err)
	}
	future := SignOwnerPublish(priv, "ana@x.io", age1.Fingerprint, "", e.clk.Now().Add(11*time.Minute), e.pos())
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: &future}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("future: %v", err)
	}
	nowhere := SignOwnerPublish(priv, "ana@x.io", age1.Fingerprint, "", e.clk.Now(), FormatLogPosition(99, strings.Repeat("a", 64)))
	if _, err := e.d.PublishWith(ctx, ana, age1, PublishOptions{Owner: &nowhere}); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("a position that is not in the log: %v", err)
	}
	if got := e.lookup(); len(got) != 0 {
		t.Fatalf("refused publishes left records: %+v", got)
	}

	k, err := e.publish(priv, age1)
	if err != nil || !k.Verified || !k.OwnerSigned || !k.OwnerEnrolled || k.Owner == nil || k.Attested {
		t.Fatalf("%+v %v", k, err)
	}
	row := e.lookup()[age1.Fingerprint]
	if !row.OwnerSigned || row.Owner.Pub != FormatOwnerPub(pub) {
		t.Fatalf("%+v", row)
	}
	dk := toDirectoryKey(row)
	if !VerifyOwnerEvidence("ana@x.io", dk) {
		t.Error("the evidence in the row verifies without the server")
	}
	if VerifyOwnerEvidence("bo@x.io", dk) {
		t.Error("the evidence is for ana's address only")
	}
	dk.Fingerprint = "SHA256:other"
	if VerifyOwnerEvidence("ana@x.io", dk) {
		t.Error("and for this fingerprint only")
	}

	// Revoking needs the signature too.
	if _, err := e.d.Revoke(ctx, ana, age1.Fingerprint, nil); !errors.Is(err, ErrOwnerRequired) {
		t.Errorf("revoke without: %v", err)
	}
	if _, err := e.d.Revoke(ctx, ana, age1.Fingerprint, e.sign(other, OpRevoke, age1.Fingerprint)); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("revoke with another key: %v", err)
	}
	if _, err := e.d.Revoke(ctx, ana, age1.Fingerprint, e.sign(priv, OpRevoke, age1.Fingerprint)); err != nil {
		t.Fatal(err)
	}
	if row := e.lookup()[age1.Fingerprint]; !row.Revoked || !row.OwnerSigned {
		t.Errorf("the tombstone is owner-signed: %+v", row)
	}

	a := e.audit(AuditOptions{})
	if !a.Enrolled || len(a.Violations) != 0 || len(a.Entries) != 3 || len(a.OwnerKeys) != 1 {
		t.Errorf("%+v", a)
	}
}

func TestAServerWithTheDirectoryKeyCannotPublishForAnEnrolledAddress(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	good := newRecovery(t)
	if _, err := e.publish(priv, good); err != nil {
		t.Fatal(err)
	}
	rogue := newRecovery(t)
	now := e.clk.Now()
	fake := func(owner *docstore.DirectoryOwnerSig) {
		in := logInput{Op: OpPublish, Email: "ana@x.io", Fingerprint: rogue.Fingerprint}
		rec := directoryRecord{Key: rogue.Text, Publisher: ana.ID, Provider: "google", Email: "ana@x.io", At: now.Unix()}
		if owner != nil {
			in.OwnerSig, in.OpTime, in.OwnerPrev = owner.Sig, owner.Time, owner.Prev
			rec.OwnerSig, rec.OwnerTime, rec.OwnerPrev = owner.Sig, owner.Time, owner.Prev
		}
		seq, err := e.d.appendLog(ctx, in, now)
		if err != nil {
			t.Fatal(err)
		}
		rec.Seq = seq
		id := e.d.directoryID("ana@x.io", rogue.Fingerprint)
		data, _ := json.Marshal(e.d.sign(rec, id))
		if err := e.store.Save(ctx, id, data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.d.PublishWith(ctx, ana, rogue, PublishOptions{}); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("PublishWith: %v", err)
	}
	fake(nil)
	if k := e.lookup()[rogue.Fingerprint]; k.Verified || k.OwnerSigned || !k.OwnerEnrolled {
		t.Errorf("a forged record on an enrolled address is verified: %+v", k)
	}
	if k := e.lookup()[good.Fingerprint]; !k.OwnerSigned || !k.Verified {
		t.Errorf("the owner's own key stays: %+v", k)
	}
	_, serverOwn := ownerKey(t)
	rogue = newRecovery(t)
	fake(e.sign(serverOwn, OpPublish, rogue.Fingerprint))
	if k := e.lookup()[rogue.Fingerprint]; k.Verified || k.OwnerSigned {
		t.Errorf("a record signed by a key that is not the owner's: %+v", k)
	}
	a := e.audit(AuditOptions{})
	if len(a.Violations) != 2 || a.Violations[0].Seq != 3 || a.Violations[1].Seq != 4 {
		t.Fatalf("violations: %+v", a.Violations)
	}
	if !strings.Contains(a.Violations[0].Why, "no owner signature") || !strings.Contains(a.Violations[1].Why, "does not verify") {
		t.Errorf("%+v", a.Violations)
	}
	sPub, sPriv := ownerKey(t)
	pos := e.pos()
	sig := SignOwnerOp(sPriv, OpEnroll, "ana@x.io", OwnerFingerprint(sPub), now, pos)
	if _, err := e.d.appendLog(ctx, logInput{Op: OpEnroll, Email: "ana@x.io", Fingerprint: OwnerFingerprint(sPub), OwnerPub: FormatOwnerPub(sPub), OwnerSig: sig.Sig, OpTime: sig.Time, OwnerPrev: pos}, now); err != nil {
		t.Fatal(err)
	}
	a = e.audit(AuditOptions{})
	if len(a.Violations) != 3 || !strings.Contains(a.Violations[2].Why, "already has an owner key") || len(a.OwnerKeys) != 1 {
		t.Errorf("%+v", a)
	}
	if _, err := e.enroll(OpEnroll, sPriv, nil); !errors.Is(err, ErrEnrolled) {
		t.Errorf("enroll over an owner key: %v", err)
	}
}

func TestPublicationsBeforeEnrollmentStayVerifiedButAreNotOwnerSigned(t *testing.T) {
	e := newOwnerEnv(t)
	old := newRecovery(t)
	if _, err := e.publish(nil, old); err != nil {
		t.Fatal(err)
	}
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	k := e.lookup()[old.Fingerprint]
	if !k.Verified || k.OwnerSigned || !k.OwnerEnrolled {
		t.Errorf("older than the enrollment: Verified, not owner-signed, and the row says the address is enrolled: %+v", k)
	}
	if _, err := e.publish(priv, old); err != nil {
		t.Fatal(err)
	}
	if k := e.lookup()[old.Fingerprint]; !k.OwnerSigned {
		t.Errorf("published again with the owner's signature: %+v", k)
	}
}

func TestOwnerSignaturesCannotBeReplayedLater(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	k := newRecovery(t)
	sig := e.sign(priv, OpPublish, k.Fingerprint)
	if _, err := e.d.PublishWith(ctx, ana, k, PublishOptions{Owner: sig}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Revoke(ctx, ana, k.Fingerprint, e.sign(priv, OpRevoke, k.Fingerprint)); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(DefaultRevokeWindow + time.Minute)
	if _, err := e.d.PublishWith(ctx, ana, k, PublishOptions{Owner: sig}); !errors.Is(err, ErrBadOwnerSig) {
		t.Fatalf("a replayed signature: %v", err)
	}
	if got := e.lookup(); len(got) != 0 && got[k.Fingerprint].Verified {
		t.Errorf("%+v", got)
	}
}

func TestOwnerKeyRotationIsSignedByTheOldKeyAndKeepsEarlierPublicationsOwnerSigned(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	pub1, priv1 := ownerKey(t)
	pub2, priv2 := ownerKey(t)
	if _, err := e.enroll(OpRotate, priv2, priv1); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("rotate with no owner key: %v", err)
	}
	e.mustEnroll(priv1)
	k1 := newRecovery(t)
	if _, err := e.publish(priv1, k1); err != nil {
		t.Fatal(err)
	}
	_, stranger := ownerKey(t)
	if _, err := e.enroll(OpRotate, priv2, stranger); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("rotation not signed by the current key: %v", err)
	}
	ch, _ := e.d.EnrollChallenge(ctx, ana)
	noAccept := e.enrollRequest(ch, OpRotate, priv2, priv1)
	noAccept.Proof = ""
	if _, err := e.d.Enroll(ctx, ana, noAccept); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("rotation the new key did not accept: %v", err)
	}
	if _, err := e.enroll(OpRotate, priv1, priv1); !errors.Is(err, ErrEnrolled) {
		t.Errorf("rotation to the same key: %v", err)
	}
	view, err := e.enroll(OpRotate, priv2, priv1)
	if err != nil || len(view.Keys) != 2 || view.Keys[0].Until != view.Keys[1].Seq || view.Keys[1].Pub != FormatOwnerPub(pub2) || view.Keys[0].Pub != FormatOwnerPub(pub1) {
		t.Fatalf("%+v %v", view, err)
	}
	k2 := newRecovery(t)
	if _, err := e.publish(priv1, k2); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("the retired owner key signs: %v", err)
	}
	if _, err := e.publish(priv2, k2); err != nil {
		t.Fatal(err)
	}
	rows := e.lookup()
	if !rows[k1.Fingerprint].OwnerSigned || !rows[k2.Fingerprint].OwnerSigned || rows[k1.Fingerprint].Owner.Pub != FormatOwnerPub(pub1) || rows[k2.Fingerprint].Owner.Pub != FormatOwnerPub(pub2) {
		t.Errorf("%+v", rows)
	}
	a := e.audit(AuditOptions{})
	if len(a.Violations) != 0 || len(a.OwnerKeys) != 2 || a.OwnerKeys[0].Until != 3 {
		t.Errorf("%+v", a)
	}
}

func TestOwnerKeyResetWaitsAndTheOwnerCanCancelIt(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	pub1, priv1 := ownerKey(t)
	pub2, priv2 := ownerKey(t)
	if _, err := e.d.RequestReset(ctx, ana); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("reset with no owner key: %v", err)
	}
	e.mustEnroll(priv1)
	if _, err := e.enroll(OpReset, priv2, nil); !errors.Is(err, ErrResetWait) {
		t.Errorf("a reset that was not requested: %v", err)
	}
	req, err := e.d.RequestReset(ctx, ana)
	if err != nil || req.ResetSince != e.clk.Now().Unix() || req.ResetDue != e.clk.Now().Add(DefaultResetWait).Unix() || !req.Enrolled {
		t.Fatalf("%+v %v", req, err)
	}
	if again, err := e.d.RequestReset(ctx, ana); err != nil || again.ResetSince != req.ResetSince {
		t.Errorf("asking again changes nothing: %+v %v", again, err)
	}
	if n := len(logOps(t, e.d)); n != 2 {
		t.Errorf("asking again is not logged twice: %d entries", n)
	}
	k := newRecovery(t)
	if _, err := e.publish(priv1, k); err != nil {
		t.Fatal(err)
	}
	if !e.lookup()[k.Fingerprint].OwnerResetPending {
		t.Error("the row shows the pending reset")
	}
	e.clk.Add(DefaultResetWait - time.Minute)
	if _, err := e.enroll(OpReset, priv2, nil); !errors.Is(err, ErrResetWait) {
		t.Errorf("a reset before the wait is over: %v", err)
	}
	if _, err := e.d.CancelReset(ctx, ana, nil); !errors.Is(err, ErrOwnerRequired) {
		t.Errorf("cancel by the session alone: %v", err)
	}
	if _, err := e.d.CancelReset(ctx, ana, e.sign(priv2, OpResetCancel, "")); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("cancel by another key: %v", err)
	}
	cancelled, err := e.d.CancelReset(ctx, ana, e.sign(priv1, OpResetCancel, ""))
	if err != nil || cancelled.ResetSince != 0 {
		t.Fatalf("%+v %v", cancelled, err)
	}
	if _, err := e.d.CancelReset(ctx, ana, e.sign(priv1, OpResetCancel, "")); !errors.Is(err, ErrResetWait) {
		t.Errorf("cancel with nothing pending: %v", err)
	}
	e.clk.Add(2 * time.Minute)
	if _, err := e.enroll(OpReset, priv2, nil); !errors.Is(err, ErrResetWait) {
		t.Errorf("a cancelled reset does not complete: %v", err)
	}

	if _, err := e.d.RequestReset(ctx, ana); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(DefaultResetWait + time.Second)
	view, err := e.enroll(OpReset, priv2, nil)
	if err != nil || len(view.Keys) != 2 || view.Keys[1].Pub != FormatOwnerPub(pub2) || view.Keys[0].Pub != FormatOwnerPub(pub1) || view.ResetSince != 0 {
		t.Fatalf("%+v %v", view, err)
	}
	if _, err := e.publish(priv1, newRecovery(t)); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("the old key after a reset: %v", err)
	}
	if _, err := e.publish(priv2, newRecovery(t)); err != nil {
		t.Errorf("the new key: %v", err)
	}
	a := e.audit(AuditOptions{})
	if len(a.Violations) != 0 || len(a.OwnerKeys) != 2 || a.ResetSince != 0 {
		t.Errorf("%+v", a)
	}
	e2 := newOwnerEnv(t)
	e2.d.ResetWait = time.Hour
	e2.mustEnroll(priv1)
	if _, err := e2.d.RequestReset(ctx, ana); err != nil {
		t.Fatal(err)
	}
	e2.clk.Add(2 * time.Hour)
	if _, err := e2.enroll(OpReset, priv2, nil); err != nil {
		t.Fatal(err)
	}
	if a := e2.audit(AuditOptions{}); len(a.Violations) != 1 || !strings.Contains(a.Violations[0].Why, "waiting period") {
		t.Errorf("a short wait is a violation: %+v", a.Violations)
	}
	if a := e2.audit(AuditOptions{ResetWait: time.Hour}); len(a.Violations) != 0 {
		t.Errorf("under the auditor's own figure: %+v", a.Violations)
	}
}

func TestEnrollChallengeIsSingleUseBoundToTheUserAndCurrentKeyOnly(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	oldSigner, rotated := rotation(t)
	dOld := &AdapterDirectory{Store: store, Signer: oldSigner, Now: clk.Now}
	dNew := &AdapterDirectory{Store: store, Signer: rotated, Now: clk.Now}
	e := &ownerEnv{t: t, d: dOld, store: store, clk: clk}
	_, priv := ownerKey(t)
	ch, err := dOld.EnrollChallenge(ctx, ana)
	if err != nil {
		t.Fatal(err)
	}
	req := e.enrollRequest(ch, OpEnroll, priv, nil)
	if _, err := dOld.Enroll(ctx, bo, req); !errors.Is(err, ErrBadProof) {
		t.Errorf("another user's token: %v", err)
	}
	bad := req
	bad.Time++
	if _, err := dOld.Enroll(ctx, ana, bad); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("a time that is not the challenge's: %v", err)
	}
	if _, err := dNew.Enroll(ctx, ana, req); !errors.Is(err, ErrBadProof) {
		t.Errorf("a challenge of a retired key: %v", err)
	}
	clk.Add(ChallengeTTL + time.Second)
	if _, err := dOld.Enroll(ctx, ana, req); !errors.Is(err, ErrBadProof) {
		t.Errorf("expired: %v", err)
	}
	ch, _ = dOld.EnrollChallenge(ctx, ana)
	req = e.enrollRequest(ch, OpEnroll, priv, nil)
	if _, err := dOld.Enroll(ctx, ana, req); err != nil {
		t.Fatal(err)
	}
	if _, err := dOld.Enroll(ctx, ana, req); !errors.Is(err, ErrEnrolled) && !errors.Is(err, ErrBadProof) {
		t.Errorf("replayed enrollment: %v", err)
	}
	pub2, priv2 := ownerKey(t)
	_ = pub2
	ch2, _ := dOld.EnrollChallenge(ctx, ana)
	rot := e.enrollRequest(ch2, OpRotate, priv2, priv)
	if _, err := dOld.Enroll(ctx, ana, rot); err != nil {
		t.Fatal(err)
	}
	if _, err := dOld.Enroll(ctx, ana, rot); err == nil {
		t.Error("a replayed rotation was accepted")
	}
	if view, _ := dOld.Enrollment(ctx, "ana@x.io"); len(view.Keys) != 2 {
		t.Errorf("%+v", view)
	}
	// A mode that is not one.
	ch3, _ := dOld.EnrollChallenge(ctx, ana)
	weird := e.enrollRequest(ch3, OpEnroll, priv2, nil)
	weird.Mode = "delete"
	if _, err := dOld.Enroll(ctx, ana, weird); !errors.Is(err, ErrBadOwnerSig) {
		t.Errorf("mode: %v", err)
	}
	if _, err := dOld.EnrollChallenge(ctx, auth.User{ID: "passkey:z", Email: "ana@x.io", VerifiedEmail: true}); !errors.Is(err, ErrNotPublishable) {
		t.Errorf("passkey: %v", err)
	}
}

func TestDamagedEnrollmentFailsClosed(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	id := e.d.enrollNames("ana@x.io")[0]
	if err := e.store.Save(ctx, id, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	k := newRecovery(t)
	if _, err := e.publish(nil, k); !errors.Is(err, errEnrollmentCorrupt) {
		t.Errorf("publish with no owner on a damaged enrollment: %v", err)
	}
	if _, err := e.d.LookupWith(ctx, "ana@x.io", LookupOptions{}); !errors.Is(err, errEnrollmentCorrupt) {
		t.Errorf("lookup: %v", err)
	}
	rec := enrollRecord{V: 1, Kid: e.d.Signer.KeyID(), Email: "ana@x.io", Sig: base64.StdEncoding.EncodeToString(make([]byte, 64))}
	data, _ := json.Marshal(rec)
	_ = e.store.Save(ctx, id, data)
	if _, err := e.d.Enrollment(ctx, "ana@x.io"); !errors.Is(err, errEnrollmentCorrupt) {
		t.Errorf("unsigned: %v", err)
	}
	_ = e.store.Delete(ctx, id)
	if a := e.audit(AuditOptions{}); !a.Enrolled {
		t.Error("the log still shows the enrollment")
	}
}

func TestResignMovesEnrollmentsAndKeepsOwnerSignedRecords(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	oldSigner, rotated := rotation(t)
	d1 := &AdapterDirectory{Store: store, Signer: oldSigner, Now: clk.Now}
	e := &ownerEnv{t: t, d: d1, store: store, clk: clk}
	pub, priv := ownerKey(t)
	e.mustEnroll(priv)
	k := newRecovery(t)
	if _, err := e.publish(priv, k); err != nil {
		t.Fatal(err)
	}
	d2 := &AdapterDirectory{Store: store, Signer: rotated, Now: clk.Now}
	e.d = d2
	if row := e.lookup()[k.Fingerprint]; !row.OwnerSigned {
		t.Fatalf("before Resign: %+v", row)
	}
	rep, err := d2.Startup(ctx)
	if err != nil || rep.Resigned != 1 || rep.Enrollments != 1 || rep.Unknown != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if rows := e.lookup(); !rows[k.Fingerprint].OwnerSigned || rows[k.Fingerprint].Kid != rotated.KeyID() || rows[k.Fingerprint].Owner.Pub != FormatOwnerPub(pub) {
		t.Errorf("after Resign: %+v", rows)
	}
	if _, err := store.Load(ctx, d1.enrollNames("ana@x.io")[0]); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the old enrollment record is still there: %v", err)
	}
	d3 := &AdapterDirectory{Store: store, Signer: mustSigner(t, []byte(strings.Repeat("\x02", 32))), Now: clk.Now}
	e.d = d3
	if rows := e.lookup(); !rows[k.Fingerprint].OwnerSigned {
		t.Errorf("without the retired key: %+v", rows)
	}
	if _, err := e.publish(priv, newRecovery(t)); err != nil {
		t.Errorf("publish after the rotation: %v", err)
	}
	if view, _ := d3.Enrollment(ctx, "ana@x.io"); !view.Enrolled {
		t.Error("still enrolled")
	}
}

func TestAuditAddressContinuesFromAnEarlierRun(t *testing.T) {
	e := newOwnerEnv(t)
	_, priv1 := ownerKey(t)
	_, priv2 := ownerKey(t)
	e.mustEnroll(priv1)
	if _, err := e.publish(priv1, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	l1, _ := e.d.ReadLog(context.Background(), 0, 100)
	addrs := e.d.LogAddressesFor("ana@x.io")
	first := AuditAddress("ana@x.io", addrs, l1.Entries, AuditOptions{})
	if _, err := e.enroll(OpRotate, priv2, priv1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.publish(priv2, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	l2, _ := e.d.ReadLog(context.Background(), int64(len(l1.Entries)), 100)
	second := AuditAddress("ana@x.io", addrs, l2.Entries, AuditOptions{Start: &first})
	if len(second.Violations) != 0 || len(second.OwnerKeys) != 2 || len(second.Entries) != 2 {
		t.Errorf("%+v", second)
	}
	alone := AuditAddress("ana@x.io", addrs, l2.Entries, AuditOptions{})
	if len(alone.Violations) == 0 {
		t.Error("a run that starts mid-history has no enrollment to check against: the rotation is flagged")
	}
	// Entries of other addresses are skipped.
	if other := AuditAddress("bo@x.io", e.d.LogAddressesFor("bo@x.io"), l1.Entries, AuditOptions{}); len(other.Entries) != 0 || other.Enrolled {
		t.Errorf("%+v", other)
	}
}

func TestARetiredOwnerKeyCannotSignAfterItsRotation(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv1 := ownerKey(t)
	_, priv2 := ownerKey(t)
	e.mustEnroll(priv1)
	if _, err := e.enroll(OpRotate, priv2, priv1); err != nil {
		t.Fatal(err)
	}
	rogue := newRecovery(t)
	now := e.clk.Now()
	sig := e.sign(priv1, OpPublish, rogue.Fingerprint)
	seq, err := e.d.appendLog(ctx, logInput{Op: OpPublish, Email: "ana@x.io", Fingerprint: rogue.Fingerprint, OwnerSig: sig.Sig, OpTime: sig.Time, OwnerPrev: sig.Prev, OwnerV: sig.V, LabelHash: LabelHash("")}, now)
	if err != nil {
		t.Fatal(err)
	}
	id := e.d.directoryID("ana@x.io", rogue.Fingerprint)
	rec := e.d.sign(directoryRecord{Key: rogue.Text, Publisher: ana.ID, Provider: "google", Email: "ana@x.io", At: now.Unix(), Seq: seq,
		OwnerSig: sig.Sig, OwnerTime: sig.Time, OwnerPrev: sig.Prev}, id)
	data, _ := json.Marshal(rec)
	if err := e.store.Save(ctx, id, data); err != nil {
		t.Fatal(err)
	}
	if k := e.lookup()[rogue.Fingerprint]; k.OwnerSigned || k.Verified {
		t.Errorf("a record signed by the retired owner key after the rotation: %+v", k)
	}
	if a := e.audit(AuditOptions{}); len(a.Violations) != 1 || a.Violations[0].Seq != seq {
		t.Errorf("%+v", a.Violations)
	}
}

func TestAuditNotesASmallClockSkewAndFlagsALargeOne(t *testing.T) {
	e := newOwnerEnv(t)
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	at := e.clk.Now()

	got := e.audit(AuditOptions{Now: at.Add(-3 * time.Minute)})
	if len(got.Violations) != 0 || len(got.Notes) != 1 || got.Notes[0].Ahead < 2*time.Minute || got.Notes[0].Ahead > 4*time.Minute {
		t.Fatalf("small skew: %+v", got)
	}
	if got := e.audit(AuditOptions{Now: at.Add(-30 * time.Second)}); len(got.Notes) != 0 || len(got.Violations) != 0 {
		t.Errorf("within a minute: %+v", got)
	}
	got = e.audit(AuditOptions{Now: at.Add(-15 * time.Minute)})
	if len(got.Violations) != 1 || !strings.Contains(got.Violations[0].Why, "ahead of the auditor") {
		t.Errorf("large skew: %+v", got)
	}
}

func TestAuditFlagsADateBeyondWhatADurationHolds(t *testing.T) {
	e := newOwnerEnv(t)
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	got := e.audit(AuditOptions{Now: time.Unix(e.clk.Now().Unix()-(1<<62), 0)})
	if len(got.Violations) != 1 || !strings.Contains(got.Violations[0].Why, "in the future") {
		t.Fatalf("%+v", got)
	}
}

func TestRequireEnrollmentRefusesPublicationsForAnUnenrolledAddress(t *testing.T) {
	e := newOwnerEnv(t)
	e.d.RequireEnrollment = true
	age1, _ := ageKey(t)
	if _, err := e.publish(nil, age1); !errors.Is(err, ErrEnrollmentRequired) {
		t.Fatalf("an unenrolled address published: %v", err)
	}
	if k, _ := e.d.Lookup(context.Background(), "ana@x.io"); len(k) != 0 {
		t.Errorf("a refused publication left keys: %+v", k)
	}
	_, priv := ownerKey(t)
	if view, err := e.enroll(OpEnroll, priv, nil); err != nil || !view.Enrolled {
		t.Fatalf("enrolling must stay open: %+v %v", view, err)
	}
	if _, err := e.publish(nil, age1); !errors.Is(err, ErrOwnerRequired) {
		t.Errorf("enrolled but unsigned: %v, want the owner's signature asked for", err)
	}
	if _, err := e.publish(priv, age1); err != nil {
		t.Errorf("enrolled and signed: %v", err)
	}
	bo := auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true}
	if err := e.d.Publish(context.Background(), bo, age1); !errors.Is(err, ErrEnrollmentRequired) {
		t.Errorf("another unenrolled address: %v", err)
	}
	e.d.RequireEnrollment = false
	if err := e.d.Publish(context.Background(), bo, age1); err != nil {
		t.Errorf("without the option an unenrolled address publishes as before: %v", err)
	}
}
