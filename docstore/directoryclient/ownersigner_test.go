package directoryclient

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"errors"
	"io"
	"testing"
)

type handleSigner struct {
	key   ed25519.PrivateKey
	calls int
	fail  error
}

func (h *handleSigner) Public() crypto.PublicKey { return h.key.Public() }
func (h *handleSigner) Sign(r io.Reader, msg []byte, o crypto.SignerOpts) ([]byte, error) {
	h.calls++
	if h.fail != nil {
		return nil, h.fail
	}
	return h.key.Sign(r, msg, o)
}

func TestOwnerKeyThatCannotBeCopiedOutEnrollsPublishesAndWatches(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	k, _ := GenerateOwnerKey()
	h := &handleSigner{key: k}
	if _, err := c.Enroll(ctx, "ana@x.io", h); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Owner: h}); err != nil {
		t.Fatalf("a signed publish: %v", err)
	}
	if h.calls < 2 {
		t.Fatalf("the signer was asked %d times", h.calls)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{Owner: h}); err != nil {
		t.Fatalf("watching: %v", err)
	}
	if _, err := e.client(ana).Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Owner: k}); err != nil {
		t.Fatalf("the bytes of the same key: %v", err)
	}
	h.fail = errors.New("the browser refused")
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Owner: h}); err == nil || !errors.Is(err, h.fail) {
		t.Fatalf("a failing signer: %v", err)
	}
	if _, err := c.Revoke(ctx, "ana@x.io", "SHA256:x", h); !errors.Is(err, h.fail) {
		t.Fatalf("revoke with a failing signer: %v", err)
	}
	pub2, _, _ := ageKey(t)
	if _, err := e.client(bo).Publish(ctx, PublishRequest{Email: "bo@x.io", Key: pub2, Owner: OwnerKey(nil)}); err != nil {
		t.Fatalf("an empty OwnerKey in the interface: %v", err)
	}
}
