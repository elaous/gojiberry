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

// Shared is a key a document is shared with, as the list shows it.
type Shared struct {
	Label       string
	Type        string
	Fingerprint string // computed from Key
	Key         string // the public key's text
	Hint        string // for a passkey key, the credential to ask
	Added       time.Time
}

// sharedRecord is the signed payload.
type sharedRecord struct {
	V   int    `json:"v"`
	Doc string `json:"doc"`
	// Seq grows with each change, so a reader that remembers the last one it saw can notice a
	// signed record being put back.
	Seq uint64 `json:"seq"`
	// Pending means the files of the document may not all be encrypted to Recipients yet (a
	// change was saved and its re-key did not finish): Remove, Add and Repair finish it.
	Pending bool `json:"pending,omitempty"`
	// Protection is the document's protection level ("" for server, ProtectionE2EE for end to
	// end), signed with the list: it is what ConfidentialKeyring trusts to know that a
	// document's files must be wrapped for people's keys only. The catalog and the document
	// say it too, but the keyring reads this one, which a store writer cannot forge (it can
	// still put back an older signed record: see SeqFloor and C.5).
	Protection string `json:"protection,omitempty"`
	// Signed says every stored file of the document was last written, or re-keyed, with a signed
	// frame (v3): set when a re-key that a signing adapter ran finished (pending cleared). From
	// then on an unsigned frame in the document is ErrForgedFrame, whatever it claims, and
	// whoever saves it must sign (ErrNoFrameSigner). A document whose record does not say it
	// still accepts older unsigned frames, with a warning, until Sharing.Repair upgrades it.
	Signed bool `json:"signed,omitempty"`
	// PendingSince is when Pending was set (Unix seconds), so a re-key that never finished can be
	// told from one in progress (Sharing.PendingStatus).
	PendingSince int64 `json:"pendingSince,omitempty"`
	// UnsignedLate names the files the re-key's second look found unsigned after it wrote the
	// record (ErrUnsignedAfterFlip), and UnsignedLateSince is when (Unix seconds). The record
	// is not Pending then, so this is what keeps the document in Sharing.UnsignedDocs until
	// the owner settles it (Repair or Recover with AcceptUnsigned, which clears it). Older
	// builds ignore the fields when they read the record, and drop them when they write it:
	// the files are refused as forged either way, so only the listing is lost.
	//
	// The ids are capped (unsignedLateCap, the first ones by id) because they come from the
	// store's listing, which a store writer can grow: UnsignedLateCount is how many files there
	// were in all. Each mark replaces the one before and keeps its UnsignedLateSince.
	UnsignedLate      []string      `json:"unsignedLate,omitempty"`
	UnsignedLateCount int           `json:"unsignedLateCount,omitempty"`
	UnsignedLateSince int64         `json:"unsignedLateSince,omitempty"`
	Recipients        []sharedEntry `json:"recipients"`
}

// binding is the Binding the record gives its document's files: level, seq, pending, and a
// hash of the recipient list (the type and key of each entry, sorted: the entries' own order
// and labels do not matter). exists says the record was found.
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
	// Fingerprint is for people reading the record; it is recomputed from Key, never trusted.
	Fingerprint string `json:"fingerprint,omitempty"`
	Key         string `json:"key"`
	Hint        string `json:"hint,omitempty"`
	Added       int64  `json:"added,omitempty"`
}

// parseShared reads an entry's key and returns the Recipient, with the fingerprint and type
// computed from the key itself.
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

// verifyRecord checks a stored record's signature against the owners' signing keys and
// returns its payload.
func verifyRecord(raw []byte, docID string, owners []ed25519.PublicKey) (sharedRecord, error) {
	rec, _, err := verifyRecordBy(raw, docID, owners)
	return rec, err
}

// verifyRecordBy is verifyRecord that also says which of owners signed the record. The record's
// own "signer" field is only a hint and is not used: the key is the one whose signature verifies.
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

// loadRecord reads and verifies a document's record. A document with no record has no
// shared keys; one that has a record that does not verify is an error (fail closed).
//
// With a floor, a record whose seq is below the highest seen for the document is refused
// (ErrStaleRecord), and so is a missing one where a record was seen; a good one raises it.
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

// StoreKeyring is a Keyring whose recipients are the owner's keys and, for each document, the
// keys it is shared with, kept in signed "keys:<ID>" records in Store. Store is the store
// itself (not the encrypting adapter: the records are readable).
type StoreKeyring struct {
	Store docstore.Adapter
	// Owners are always recipients of everything; Keys are the private keys that open it.
	Owners []age.Recipient
	Keys   []age.Identity
	// OwnerSigners are the public signing keys (Signer.Public) of the owner's keys: a
	// record is used only if one of them signed it. With none, a document that has a record
	// cannot be saved (fail closed).
	OwnerSigners []ed25519.PublicKey
	// SeqFloor, when set, refuses a record older than the highest one seen for the document
	// (ErrStaleRecord). Nil means none.
	SeqFloor SeqFloor
}

// Recipients implements Keyring. A record that is missing its signature, or has a wrong
// one, is an error, never a shorter or longer list.
func (k StoreKeyring) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	rs, _, err := k.Bind(ctx, id)
	return rs, err
}

// Bind implements Binder: the recipients (the owners' keys and the signed list's) and the
// record's Binding, from one read.
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
