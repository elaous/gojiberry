package encrypt

import (
	"bytes"
	"context"
	"testing"

	"filippo.io/age"
)

// collided makes r report the given key tag in its stanza, as two distinct SSH keys whose 4-byte
// tags collide would (engineered: a real collision costs about 2^32 work). With keyed it still
// carries the full key's fingerprint, as a recipient from ParseSSHRecipient does; without, it is
// a raw recipient that only the tag can identify.
type collided struct {
	age.Recipient
	tag string
}

func (c collided) Wrap(fk []byte) ([]*age.Stanza, error) {
	ss, err := c.Recipient.Wrap(fk)
	for _, s := range ss {
		s.Args[0] = c.tag
	}
	return ss, err
}

func collide(r Recipient, keyed bool) age.Recipient {
	if !keyed {
		if k, ok := r.Key.(sshKeyed); ok {
			return collided{Recipient: k.Recipient, tag: "COLL"}
		}
	}
	return collidedKeyed{collided{Recipient: r.Key, tag: "COLL"}}
}

// collidedKeyed forwards the fingerprint of the wrapped sshKeyed.
type collidedKeyed struct{ collided }

func (c collidedKeyed) keyFingerprint() string {
	return c.Recipient.(keyFingerprinter).keyFingerprint()
}

func TestSSHKeyTagCollisionIsToldApartByFullKey(t *testing.T) {
	ctx := context.Background()
	doc := "00000000-0000-4000-8000-0000000000a1"
	for _, kind := range []string{"ed25519", "rsa"} {
		mk := func() Recipient {
			var pub string
			if kind == "rsa" {
				pub, _ = rsaKey(t)
			} else {
				pub, _ = sshKey(t, "x")
			}
			r, err := ParseSSHRecipient(pub)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		person, server := mk(), mk()
		if person.Fingerprint == server.Fingerprint {
			t.Fatal("same key")
		}
		run := func(name string, p, s age.Recipient, wantPerson bool) {
			t.Run(kind+"/"+name, func(t *testing.T) {
				w := newWorld(t)
				w.ring.People.Owners = []age.Recipient{w.ana.r.Key, p}
				w.ring.ServerRecipients = []age.Recipient{s}
				if _, err := w.sharing.SetProtection(ctx, doc, ProtectionE2EE); err != nil {
					t.Fatal(err)
				}
				got, err := w.ring.Recipients(ctx, doc+"~aaaaaaaaaaaa.json")
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if wantPerson {
					want = 2
				}
				if len(got) != want {
					t.Errorf("got %d recipients, want %d", len(got), want)
				}
			})
		}
		// Distinct keys, equal tags, both with their full key: the person is kept.
		run("distinct keyed", collide(person, true), collide(server, true), true)
		// The same key (equal tag, equal fingerprint) is still taken for the server's and dropped.
		run("same keyed", collide(person, true), collide(person, true), false)
		// A raw server recipient has only a tag: a tag match fails closed (dropped, not admitted).
		run("server raw", collide(person, true), collide(server, false), false)
		run("person raw", collide(person, false), collide(server, true), false)
		// Different tags are never the same key, keyed or not.
		run("legit", person.Key, server.Key, true)
	}
}

func TestSSHKeyedRecipientStillWraps(t *testing.T) {
	p := newPerson(t, "ana")
	if _, ok := p.r.Key.(keyFingerprinter); !ok {
		t.Fatal("ParseSSHRecipient's key carries no fingerprint")
	}
	id, err := recipientID(p.r.Key)
	if err != nil || id.fp != p.r.Fingerprint {
		t.Fatalf("%+v %v", id, err)
	}
	// Wrapping through sshKeyed is the inner recipient's wrapping: same stanza type and tag. (The
	// rest of a stanza is fresh each time: an ephemeral share, so only Type and Args[0] compare.)
	k, ok := p.r.Key.(sshKeyed)
	if !ok {
		t.Fatalf("the key is %T, want sshKeyed", p.r.Key)
	}
	fk := bytes.Repeat([]byte{1}, 16)
	got, err := k.Wrap(fk)
	if err != nil {
		t.Fatal(err)
	}
	want, err := k.Recipient.Wrap(fk)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(want) != 1 || len(got[0].Args) == 0 || len(want[0].Args) == 0 {
		t.Fatalf("stanzas %+v, %+v", got, want)
	}
	if got[0].Type != want[0].Type || got[0].Args[0] != want[0].Args[0] {
		t.Errorf("sshKeyed wraps as %s %s, its recipient as %s %s", got[0].Type, got[0].Args[0], want[0].Type, want[0].Args[0])
	}
}
