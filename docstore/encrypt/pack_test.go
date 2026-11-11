package encrypt

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

func TestPackSealerSealsForTheOwnerKeysAndOpensWithThem(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	seal, open := PackSealer([]age.Recipient{ana.r.Key}, []age.Identity{ana.id})
	const id = "00000000-0000-4000-8000-000000000042"
	env, err := seal(ctx, id, []byte("secret payroll"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(env, []byte("payroll")) || !IsEncrypted(env) {
		t.Fatal("the envelope shows the message")
	}
	got, err := open(ctx, id, env)
	if err != nil || string(got) != "secret payroll" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := open(ctx, "00000000-0000-4000-8000-000000000043", env); err == nil {
		t.Error("an envelope opened under another record's id")
	}
	_, openBo := PackSealer(nil, []age.Identity{bo.id})
	if _, err := openBo(ctx, id, env); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("a stranger: %v", err)
	}
	_, openNone := PackSealer(nil, nil)
	if _, err := openNone(ctx, id, env); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("no keys: %v", err)
	}
	// No recipients: nothing is sealed.
	sealNone, _ := PackSealer(nil, nil)
	if _, err := sealNone(ctx, id, []byte("x")); err == nil {
		t.Error("sealed to no one")
	}
	if _, err := open(ctx, id, []byte("plain text")); err == nil {
		t.Error("opened a record that is not encrypted")
	}
}

func TestPackSealerIsSafeForConcurrentUse(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	seal, open := PackSealer([]age.Recipient{ana.r.Key}, []age.Identity{ana.id})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := seal(ctx, "00000000-0000-4000-8000-000000000001", []byte("m"))
			if err != nil {
				t.Error(err)
				return
			}
			if got, err := open(ctx, "00000000-0000-4000-8000-000000000001", env); err != nil || string(got) != "m" {
				t.Errorf("%q %v", got, err)
			}
		}()
	}
	wg.Wait()
}

func TestPackOpenSaysWrongKeyWhenKeysAreHeld(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	seal, _ := PackSealer([]age.Recipient{ana.r.Key}, nil)
	const id = "00000000-0000-4000-8000-000000000042"
	env, err := seal(ctx, id, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, openBo := PackSealer(nil, []age.Identity{bo.id})
	_, err = openBo(ctx, id, env)
	if !errors.Is(err, docstore.ErrWrongKey) || !errors.Is(err, docstore.ErrLocked) || !errors.Is(err, ErrWrongPackKey) {
		t.Fatalf("wrong key: %v", err)
	}
	_, openNone := PackSealer(nil, nil)
	if _, err := openNone(ctx, id, env); errors.Is(err, docstore.ErrWrongKey) {
		t.Fatalf("no key at all is locked, not a wrong key: %v", err)
	}
}
