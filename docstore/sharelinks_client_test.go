package docstore

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPAdapterMakesOpensAndRevokesALink(t *testing.T) {
	f := newLinkFixture(t)
	ts := httptest.NewServer(f.h)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	owner := NewHTTPAdapter(ts.URL, nil)
	owner.Header.Set("Authorization", "Bearer "+f.owner)

	tok, id, exp, err := owner.CreateLink(ctx, "doc1", RoleViewer, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(f.clock.Load(), 0).Add(3 * time.Hour).UTC().Truncate(time.Second); !exp.Equal(want) {
		t.Fatalf("expires %v, want %v", exp, want)
	}
	guest := NewHTTPAdapter(ts.URL, nil)
	if _, err := guest.Load(ctx, "doc1"); err == nil {
		t.Fatal("a guest without the link read the document")
	}
	guest.UseShareLink(tok)
	if doc, err := guest.Load(ctx, "doc1"); err != nil || string(doc.Data) != "v1" {
		t.Fatalf("a guest with the link: %v %q", err, doc.Data)
	}
	if err := guest.Save(ctx, "doc1", []byte("x")); err == nil {
		t.Fatal("a view link wrote")
	}
	acl, err := owner.LoadACL(ctx, "doc1")
	if err != nil || len(acl.WhoCanSee(time.Unix(f.clock.Load(), 0))) != 3 { // owner, viewer, the link
		t.Fatalf("%v %+v", err, acl)
	}
	if err := owner.RevokeLink(ctx, "doc1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.Load(ctx, "doc1"); err == nil {
		t.Fatal("a revoked link still reads")
	}
	if err := guest.RevokeLink(ctx, "doc1", id); err == nil {
		t.Fatal("a guest revoked a link")
	}
	guest.UseShareLink("")
	if guest.Header.Get(ShareHeader) != "" {
		t.Fatal("UseShareLink with an empty token kept the token")
	}
}
