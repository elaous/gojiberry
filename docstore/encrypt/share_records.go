package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"sort"
	"time"
)

// Shared is a key a document is shared with.
type Shared struct {
	Label       string
	Type        string
	Fingerprint string // computed from Key
	Key         string // the public key's text
	Hint        string // for a passkey key, the credential to ask
	Added       time.Time
}

// sharedRecord is the signed payload of a share record.
type sharedRecord struct {
	V   int    `json:"v"`
	Doc string `json:"doc"`
	// Seq grows with each change, so readers can spot stale records.
	Seq uint64 `json:"seq"`
	// Pending means the document's files may not be consistent yet.
	Pending bool `json:"pending,omitempty"`
	// Protection is the document's protection level.
	Protection string `json:"protection,omitempty"`
	// Signed says every stored file of the document was signed.
	Signed bool `json:"signed,omitempty"`
	// PendingSince is when Pending was set, in Unix seconds.
	PendingSince      int64         `json:"pendingSince,omitempty"`
	UnsignedLate      []string      `json:"unsignedLate,omitempty"`
	UnsignedLateCount int           `json:"unsignedLateCount,omitempty"`
	UnsignedLateSince int64         `json:"unsignedLateSince,omitempty"`
	Recipients        []sharedEntry `json:"recipients"`
}

// binding is the Binding the record gives its document.
func (r sharedRecord) binding(exists bool) Binding {
	b := Binding{Exists: exists, Seq: r.Seq, Pending: r.Pending, Signed: r.Signed}
	if r.Protection == ProtectionE2EE {
		b.Level = ProtectionE2EE
	}
	if !exists {
		return Binding{}
	}
	lines := make([]string, 0, len(r.Recipients))
	for _, e := range r.Recipients {
		lines = append(lines, e.Type+" "+e.Key)
	}
	sort.Strings(lines)
	h := sha256.New()
	h.Write([]byte("gojiberry/frame-recipients/v1\n"))
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	copy(b.Hash[:], h.Sum(nil))
	return b
}

// signedRecord is what is stored under keys:<ID>.
type signedRecord struct {
	Payload []byte `json:"payload"`
	Sig     []byte `json:"sig"`
	Signer  []byte `json:"signer,omitempty"` // a hint; only OwnerSigners are trusted
}

type sharedEntry struct {
	Label string `json:"label,omitempty"`
	Type  string `json:"type"`
	// Fingerprint is for people reading the record.
	Fingerprint string `json:"fingerprint,omitempty"`
	Key         string `json:"key"`
	Hint        string `json:"hint,omitempty"`
	Added       int64  `json:"added,omitempty"`
}

// parseShared reads an entry's key and returns the record.
func parseShared(e sharedEntry) (Recipient, error) {
	switch e.Type {
	case "age-x25519", TypePasskey:
		r, err := ParseAgeRecipient(e.Key)
		if err != nil {
			return Recipient{}, err
		}
		r.Label = e.Label
		if e.Type == TypePasskey {
			r.Type, r.Hint = TypePasskey, e.Hint
		}
		return r, nil
	}
	r, err := ParseSSHRecipient(e.Key)
	if err != nil {
		return Recipient{}, err
	}
	if r.Label == "" {
		r.Label = e.Label
	}
	return r, nil
}

// verifyRecord checks a stored record's signature.
func verifyRecord(raw []byte, docID string, owners []ed25519.PublicKey) (sharedRecord, error) {
	rec, _, err := verifyRecordBy(raw, docID, owners)
	return rec, err
}

// verifyRecordBy is verifyRecord that also says which key signed.
func verifyRecordBy(raw []byte, docID string, owners []ed25519.PublicKey) (sharedRecord, ed25519.PublicKey, error) {
	var rec sharedRecord
	var sr signedRecord
	if err := json.Unmarshal(raw, &sr); err != nil {
		return rec, nil, fmt.Errorf("encrypt: the recipient list of %s is damaged: %w", docID, err)
	}
	if len(sr.Sig) == 0 || len(sr.Payload) == 0 {
		return rec, nil, fmt.Errorf("%s: %w", docID, ErrUnsignedRecord)
	}
	msg := recordMessage(docID, sr.Payload)
	var by ed25519.PublicKey
	for _, k := range owners {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, msg, sr.Sig) {
			by = k
			break
		}
	}
	if by == nil {
		return rec, nil, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	if err := json.Unmarshal(sr.Payload, &rec); err != nil || rec.V != 1 || rec.Doc != docID {
		return sharedRecord{}, nil, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	return rec, by, nil
}

// loadRecord reads and verifies a document's record.
func loadRecord(ctx context.Context, store docstore.Adapter, docID string, owners []ed25519.PublicKey, floor SeqFloor) (rec sharedRecord, digest string, err error) {
	d, err := store.Load(ctx, KeysPrefix+docID)
	if errors.Is(err, docstore.ErrNotFound) {
		if floor != nil {
			if f, ferr := floorGet(ctx, floor, docID); ferr != nil {
				return sharedRecord{}, "", ferr
			} else if f > 0 {
				return sharedRecord{}, "", fmt.Errorf("%s: %w", docID, ErrStaleRecord)
			}
		}
		return sharedRecord{}, "", nil
	}
	if err != nil {
		return sharedRecord{}, "", err
	}
	rec, err = verifyRecord(d.Data, docID, owners)
	if err != nil {
		return rec, docstore.Digest(d.Data), err
	}
	if floor != nil {
		f, ferr := floorGet(ctx, floor, docID)
		if ferr != nil {
			return sharedRecord{}, "", ferr
		}
		if rec.Seq < f {
			return sharedRecord{}, docstore.Digest(d.Data), fmt.Errorf("%s: %w", docID, ErrStaleRecord)
		}
		if ferr := floorRaise(ctx, floor, docID, rec.Seq); ferr != nil {
			return sharedRecord{}, "", ferr
		}
	}
	return rec, docstore.Digest(d.Data), nil
}

// StoreKeyring is a Keyring whose recipients are the owners.
type StoreKeyring struct {
	Store docstore.Adapter
	// Owners are always recipients of everything.
	Owners []age.Recipient
	Keys   []age.Identity
	// OwnerSigners are the public signing keys trusted for records.
	OwnerSigners []ed25519.PublicKey
	// SeqFloor, when set, refuses a record older than the floor.
	SeqFloor SeqFloor
}

// Recipients implements Keyring.
func (k StoreKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	rs, _, err := k.Bind(ctx, id)
	return rs, err
}

// Bind implements Binder: the recipients and Binding of a record.
func (k StoreKeyring) Bind(ctx context.Context, id string) ([]age.Recipient, Binding, error) {
	out := append([]age.Recipient(nil), k.Owners...)
	doc := DocID(id)
	if doc == "" {
		return out, Binding{}, nil
	}
	rec, digest, err := loadRecord(ctx, k.Store, doc, k.OwnerSigners, k.SeqFloor)
	if err != nil {
		return nil, Binding{}, err
	}
	for _, e := range rec.Recipients {
		r, err := parseShared(e)
		if err != nil {
			return nil, Binding{}, fmt.Errorf("encrypt: a recipient of %s cannot be read: %w", doc, err)
		}
		out = append(out, r.Key)
	}
	return out, rec.binding(digest != ""), nil
}

// Binding implements Binder.
func (k StoreKeyring) Binding(ctx context.Context, id string) (Binding, error) {
	doc := DocID(id)
	if doc == "" {
		return Binding{}, nil
	}
	rec, digest, err := loadRecord(ctx, k.Store, doc, k.OwnerSigners, k.SeqFloor)
	if err != nil {
		return Binding{}, err
	}
	return rec.binding(digest != ""), nil
}

// Identities implements Keyring.
func (k StoreKeyring) Identities(context.Context, string) ([]age.Identity, error) { return k.Keys, nil }
