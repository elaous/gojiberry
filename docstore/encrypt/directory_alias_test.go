package encrypt

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func seedOf(b byte) []byte { return []byte(strings.Repeat(string([]byte{b}), 32)) }

func TestRemovedDirectoryKeysPseudonymsAreKept(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	k1 := mustSigner(t, seedOf(1))
	d1 := &AdapterDirectory{Store: store, Signer: k1, Now: clk.Now}
	e := &ownerEnv{t: t, d: d1, store: store, clk: clk}
	_, priv := ownerKey(t)
	e.mustEnroll(priv) // an owner-state entry under key 1's pseudonym
	p1 := d1.LogAddressFor("ana@x.io")

	d2 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(2), seedOf(1)), Now: clk.Now}
	if _, err := d2.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	p2 := d2.LogAddressFor("ana@x.io")
	if p1 == p2 {
		t.Fatal("two keys, two pseudonyms")
	}
	if _, err := store.Load(ctx, aliasPrefix+p2); err != nil {
		t.Fatalf("no alias record: %v", err)
	}

	cold := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(2)), Now: clk.Now}
	if un, err := cold.UnmappedAddresses(ctx); err != nil || un.Count != 1 {
		t.Fatalf("without the alias loaded: %+v %v", un, err)
	}
	d3 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(2)), Now: clk.Now}
	if _, err := d3.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if got := d3.LogAddressesFor("ana@x.io"); !slices.Equal(got, []string{p2, p1}) {
		t.Fatalf("addresses = %v, want [%s %s]", got, p2, p1)
	}
	if un, err := d3.UnmappedAddresses(ctx); err != nil || un.Count != 0 {
		t.Fatalf("with the alias: %+v %v", un, err)
	}
	la, err := dirService{d3}.LogAddress(ctx, ana)
	if err != nil || la.Address != p2 || !slices.Equal(la.Previous, []string{p1}) {
		t.Fatalf("log address: %+v %v", la, err)
	}

	d4 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(3), seedOf(2)), Now: clk.Now}
	if _, err := d4.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	p3 := d4.LogAddressFor("ana@x.io")
	d5 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(3)), Now: clk.Now}
	if _, err := d5.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	got := d5.LogAddressesFor("ana@x.io")
	if got[0] != p3 || !slices.Contains(got, p2) || !slices.Contains(got, p1) || len(got) != 3 {
		t.Fatalf("after two rotations: %v (want %s then %s and %s)", got, p3, p2, p1)
	}
	if un, err := d5.UnmappedAddresses(ctx); err != nil || un.Count != 0 {
		t.Fatalf("after two rotations: %+v %v", un, err)
	}
}

func TestAnAlteredAliasRecordIsIgnored(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	d2 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(2), seedOf(1)), Now: clk.Now}
	e := &ownerEnv{t: t, d: d2, store: store, clk: clk}
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	if _, err := d2.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	cur := d2.LogAddressFor("ana@x.io")
	doc, err := store.Load(ctx, aliasPrefix+cur)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(doc.Data), `"previous":[`, `"previous":["deadbeef",`, 1)
	if err := store.Save(ctx, aliasPrefix+cur, []byte(forged)); err != nil {
		t.Fatal(err)
	}
	d3 := &AdapterDirectory{Store: store, Signer: mustSigner(t, seedOf(2)), Now: clk.Now}
	if err := d3.loadAliases(ctx); err != nil {
		t.Fatal(err)
	}
	if got := d3.LogAddressesFor("ana@x.io"); len(got) != 1 {
		t.Fatalf("a forged alias was believed: %v", got)
	}
}
