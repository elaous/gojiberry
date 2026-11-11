package encrypt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func TestOwnerSignatureCoversTheLabel(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	k := newRecovery(t)
	k.Label = "laptop"
	signedForOther := SignOwnerPublish(priv, "ana@x.io", k.Fingerprint, "phone", e.clk.Now(), e.pos())
	if _, err := e.d.PublishWith(ctx, ana, k, PublishOptions{Owner: &signedForOther}); !errors.Is(err, ErrBadOwnerSig) {
		t.Fatalf("a signature over another label: %v", err)
	}
	good := SignOwnerPublish(priv, "ana@x.io", k.Fingerprint, "laptop", e.clk.Now(), e.pos())
	if _, err := e.d.PublishWith(ctx, ana, k, PublishOptions{Owner: &good}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.d.LookupWith(ctx, "ana@x.io", LookupOptions{})
	if err != nil || len(rows) != 1 || !rows[0].OwnerSigned || rows[0].Label != "laptop" {
		t.Fatalf("%+v %v", rows, err)
	}
	if a := e.audit(AuditOptions{}); len(a.Violations) != 0 {
		t.Errorf("%+v", a.Violations)
	}
	name := ""
	metas, _ := docstore.ListPrefix(ctx, e.store, directoryPrefix)
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, popPrefix) && !strings.HasPrefix(m.ID, docstore.DirectoryEnrollPrefix) {
			name = m.ID
		}
	}
	doc, err := e.store.Load(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.Data), `"label":"laptop"`) {
		t.Fatalf("%s", doc.Data)
	}
	if err := e.store.Save(ctx, name, []byte(strings.Replace(string(doc.Data), `"label":"laptop"`, `"label":"evil"`, 1))); err != nil {
		t.Fatal(err)
	}
	rows, _ = e.d.LookupWith(ctx, "ana@x.io", LookupOptions{})
	for _, r := range rows {
		if r.OwnerSigned {
			t.Errorf("a record with a swapped label is still owner-signed: %+v", r)
		}
	}
}

func TestLogTimesDoNotGoBack(t *testing.T) {
	ctx := context.Background()
	d, _, clk := newTestDirectory(t)
	if err := d.Publish(ctx, ana, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	clk.Add(-time.Hour) // a clock that went back
	if err := d.Publish(ctx, ana, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	ents := logOps(t, d)
	if len(ents) != 2 || ents[1].At < ents[0].At {
		t.Fatalf("times went back: %+v", ents)
	}
	if _, err := VerifyLog(ents, "", 0, logKeys(t, d)); err != nil {
		t.Fatal(err)
	}
	bad := append([]LogEntry(nil), ents...)
	e2 := bad[1]
	e2.At = ents[0].At - int64(time.Hour/time.Second)
	e2.Hash = logHash(e2)
	e2.Sig = signEntryForTest(d, e2)
	bad[1] = e2
	if _, err := VerifyLog(bad, "", 0, logKeys(t, d)); !errors.Is(err, ErrLogBroken) || !strings.Contains(err.Error(), "must not go back") {
		t.Errorf("a back-dated chain verified: %v", err)
	}
	audit := AuditAddress("ana@x.io", d.LogAddressesFor("ana@x.io"), bad, AuditOptions{})
	if len(audit.Violations) == 0 || !strings.Contains(audit.Violations[0].Why, "went back") {
		t.Errorf("%+v", audit.Violations)
	}
	ok := append([]LogEntry(nil), ents...)
	e3 := ok[1]
	e3.At = ents[0].At - 60
	e3.Hash = logHash(e3)
	e3.Sig = signEntryForTest(d, e3)
	ok[1] = e3
	if _, err := VerifyLog(ok, "", 0, logKeys(t, d)); err != nil {
		t.Errorf("a small skew: %v", err)
	}
}

func signEntryForTest(d *AdapterDirectory, e LogEntry) string {
	return SignLogEntry(d.Signer, e).Sig
}

func TestCrashedEnrollmentIsReconciledFromTheLog(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	for _, id := range e.d.enrollNames("ana@x.io") {
		_ = e.store.Delete(ctx, id)
	}
	if v, _ := e.d.Enrollment(ctx, "ana@x.io"); v.Enrolled {
		t.Fatal("the record is gone")
	}
	if _, err := e.d.RequestReset(ctx, ana); !errors.Is(err, docstore.ErrDirectoryBusy) {
		t.Fatalf("a young dangling entry: %v", err)
	}
	e.clk.Add(reconcileGrace + time.Second)
	if n, err := e.d.ReconcileEnrollments(ctx); err != nil || n != 0 {
		t.Fatalf("no record to start from: %d %v", n, err) // start-up only walks records
	}
	view, err := e.d.RequestReset(ctx, ana)
	if err != nil || !view.Enrolled || view.ResetSince == 0 {
		t.Fatalf("the record is completed from the log, then the operation runs: %+v %v", view, err)
	}
	if a := e.audit(AuditOptions{}); len(a.Violations) != 0 || !a.Enrolled {
		t.Errorf("%+v", a)
	}
	e.clk.Add(time.Hour)
	if _, err := e.d.CancelReset(ctx, ana, e.sign(priv, OpResetCancel, "")); err != nil {
		t.Fatal(err)
	}
	enr, digest, _ := e.d.readEnrollment(ctx, "ana@x.io")
	behind := *enr
	behind.Seq = 1
	behind.ResetSince = 12345
	if err := e.d.writeEnrollment(ctx, "ana@x.io", behind, digest); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(reconcileGrace + time.Second)
	if n, err := e.d.ReconcileEnrollments(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if got, _, _ := e.d.readEnrollment(ctx, "ana@x.io"); got == nil || got.ResetSince != 0 || got.Seq == 1 {
		t.Errorf("%+v", got)
	}
}

func TestOperatorResetIsLoggedAndVisible(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	if _, err := e.d.OperatorResetEnrollment(ctx, "no-such-pseudonym"); !errors.Is(err, docstore.ErrDirectoryNotFound) {
		t.Errorf("an unknown address: %v", err)
	}
	pseud := e.d.LogAddressFor("ana@x.io")
	view, err := e.d.OperatorResetEnrollment(ctx, pseud)
	if err != nil || view.Enrolled {
		t.Fatalf("%+v %v", view, err)
	}
	if v, _ := e.d.Enrollment(ctx, "ana@x.io"); v.Enrolled {
		t.Errorf("%+v", v)
	}
	a := e.audit(AuditOptions{})
	if len(a.Violations) != 0 || a.Enrolled || len(a.OperatorResets) != 1 || a.OperatorResets[0].Op != OpOperatorReset || a.OperatorResets[0].OwnerSig != "" {
		t.Fatalf("%+v", a)
	}
	_, priv2 := ownerKey(t)
	e.mustEnroll(priv2)
	if a := e.audit(AuditOptions{}); len(a.Violations) != 0 || !a.Enrolled || len(a.OperatorResets) != 1 {
		t.Errorf("%+v", a)
	}
}

func TestSweepDoesNotDeleteATombstoneReplacedSinceItWasRead(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	d := &AdapterDirectory{Store: hs, Signer: testSigner(t, 1), Now: clk.Now}
	k := newRecovery(t)
	if err := d.Publish(ctx, ana, k); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Revoke(ctx, ana, k.Fingerprint, nil); err != nil {
		t.Fatal(err)
	}
	clk.Add(d.window() + time.Hour)
	metas, _ := docstore.ListPrefix(ctx, hs, directoryPrefix)
	var tomb string
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, popPrefix) {
			tomb = m.ID
		}
	}
	replaced := false
	hs.mu.Lock()
	hs.afterLoad = func(id string) {
		if id == tomb && !replaced {
			replaced = true
			_ = hs.MemoryAdapter.Save(ctx, id, []byte(`{"republished":true}`))
		}
	}
	hs.mu.Unlock()
	if n, err := d.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := hs.MemoryAdapter.Load(ctx, tomb); err != nil {
		t.Errorf("the replaced record was deleted: %v", err)
	}
	hs.mu.Lock()
	hs.afterLoad = nil
	hs.mu.Unlock()
}

func TestDefaultSkipIsCaseInsensitive(t *testing.T) {
	for _, id := range []string{"Directory/abc", "DIRECTORY/abc", "directory/abc", "Catalog:x", "ACL:x", "Fonts/a.woff", strings.ToUpper(docstore.DirectoryLogPrefix) + "1", strings.ToUpper(docstore.DirectoryEnrollPrefix) + "x"} {
		if !DefaultSkip(id) {
			t.Errorf("%q is encrypted", id)
		}
	}
	if DefaultSkip("document-1") {
		t.Error("a document is skipped")
	}
}

func TestWritersSeesOtherProcessesSharingTheStore(t *testing.T) {
	ctx := context.Background()
	store, clk := newHookStore(), newTestClock()
	d1 := &AdapterDirectory{Store: store, Signer: testSigner(t, 1), Now: clk.Now}
	d2 := &AdapterDirectory{Store: store, Signer: testSigner(t, 1), Now: clk.Now}
	if n, err := d1.Writers(ctx); err != nil || n != 0 {
		t.Fatalf("alone: %d %v", n, err)
	}
	if n, err := d2.Writers(ctx); err != nil || n != 1 {
		t.Fatalf("another process registered: %d %v", n, err)
	}
	if n, _ := d1.Writers(ctx); n != 1 {
		t.Errorf("%d", n)
	}
	clk.Add(writerFresh + time.Second) // d2 stopped beating
	if n, _ := d1.Writers(ctx); n != 0 {
		t.Errorf("a stale registration still counts: %d", n)
	}
}
