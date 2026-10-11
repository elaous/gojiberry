package encrypt

import (
	"bytes"
	"context"
	"testing"

	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
)

// A backup of the store under the encrypting adapter holds only what the server holds (ciphertext),
// is made and restored without a key, and after a restore the owner reads the same documents.
func TestBackupOfAnEncryptedStoreIsOpaqueAndRestoresReadable(t *testing.T) {
	ctx := context.Background()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	ring := Static{To: []age.Recipient{id.Recipient()}, Keys: []age.Identity{id}}
	inner := docstore.NewMemoryAdapter()
	if err := New(inner, ring).Save(ctx, "report", []byte("quarterly numbers: secret")); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	if _, err := docstore.Backup(ctx, inner, &archive); err != nil { // the inner adapter: no key involved
		t.Fatal(err)
	}
	if bytes.Contains(archive.Bytes(), []byte("quarterly numbers")) {
		t.Fatal("the backup shows plaintext")
	}

	fresh := docstore.NewMemoryAdapter()
	if n, err := docstore.Restore(ctx, fresh, bytes.NewReader(archive.Bytes())); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	d, err := New(fresh, ring).Load(ctx, "report")
	if err != nil || string(d.Data) != "quarterly numbers: secret" {
		t.Fatalf("%q %v", d.Data, err)
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := New(fresh, Static{Keys: []age.Identity{other}}).Load(ctx, "report"); err == nil {
		t.Fatal("a restored store is still closed to a stranger")
	}
}
