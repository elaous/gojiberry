package encrypt

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func unmappedOf(t *testing.T, ents ...LogEntry) []string {
	t.Helper()
	d, _, _ := newTestDirectory(t)
	a := strings.Repeat("e", 64)
	for i := range ents {
		ents[i].Address = a
	}
	d.idxMu.Lock()
	d.idx.byAddr = map[string][]LogEntry{a: ents}
	d.idxMu.Unlock()
	var out []string
	for _, u := range d.findUnmapped(map[string]bool{}) {
		out = append(out, fmt.Sprintf("%d-%d", u.FirstSeq, u.LastSeq))
	}
	return out
}

func TestFindUnmappedVoidAndReset(t *testing.T) {
	cases := []struct {
		name string
		ents []LogEntry
		want []string
	}{
		{"enroll voided", []LogEntry{{Seq: 5, Op: OpEnroll}, {Seq: 6, Op: OpVoid, Ref: 5}}, nil},
		{"enroll voided then enrolled again", []LogEntry{{Seq: 5, Op: OpEnroll}, {Seq: 6, Op: OpVoid, Ref: 5}, {Seq: 7, Op: OpEnroll}}, []string{"7-7"}},
		{"only a void", []LogEntry{{Seq: 6, Op: OpVoid, Ref: 5}}, nil},
		{"enroll, operator reset, enroll", []LogEntry{{Seq: 1, Op: OpEnroll}, {Seq: 2, Op: OpOperatorReset}, {Seq: 3, Op: OpEnroll}}, []string{"3-3"}},
		{"enroll, operator reset", []LogEntry{{Seq: 1, Op: OpEnroll}, {Seq: 2, Op: OpOperatorReset}}, nil},
		{"one voided, one standing", []LogEntry{{Seq: 1, Op: OpEnroll}, {Seq: 2, Op: OpResetRequest}, {Seq: 3, Op: OpVoid, Ref: 1}}, []string{"2-2"}},
	}
	for _, c := range cases {
		got := unmappedOf(t, c.ents...)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestUnmappedIgnoresAVoidedFirstEnrollment(t *testing.T) {
	ctx := context.Background()
	e := newOwnerEnv(t)
	crashAfterEnroll(t, e, false)
	if u, _ := e.d.UnmappedAddresses(ctx); u.Count != 1 {
		t.Fatalf("listed before the void: %+v", u)
	}
	e.d.voidLog(ctx, "ana@x.io", 1, "", e.clk.Now())
	if u, err := e.d.UnmappedAddresses(ctx); err != nil || u.Count != 0 {
		t.Errorf("voided: %+v %v", u, err)
	}
}

func TestUnmappedMendedByEnrollAndCancelReset(t *testing.T) {
	ctx := context.Background()
	crash := func() (*ownerEnv, ed25519.PrivateKey) {
		e := newOwnerEnv(t)
		_, priv := ownerKey(t)
		e.mustEnroll(priv)
		for _, id := range e.d.enrollNames("ana@x.io") {
			_ = e.store.Delete(ctx, id)
		}
		e.clk.Add(reconcileGrace + time.Second)
		if u, _ := e.d.UnmappedAddresses(ctx); u.Count != 1 {
			t.Fatalf("not unmapped: %+v", u)
		}
		return e, priv
	}
	e, priv := crash()
	if _, err := e.enroll(OpEnroll, priv, nil); !errors.Is(err, ErrEnrolled) {
		t.Errorf("Enroll finds the mended record: %v", err)
	}
	if u, _ := e.d.UnmappedAddresses(ctx); u.Count != 0 {
		t.Errorf("Enroll mends: %+v", u)
	}
	e, priv = crash()
	_, _ = e.d.CancelReset(ctx, ana, e.sign(priv, OpResetCancel, ""))
	if u, _ := e.d.UnmappedAddresses(ctx); u.Count != 0 {
		t.Errorf("CancelReset mends: %+v", u)
	}
}
