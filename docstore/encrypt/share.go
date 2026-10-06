package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// KeysPrefix is the prefix of the records that say who a document is shared with: "keys:<document
// ID>". They hold public keys only, so they are stored readable (DefaultSkip) and a person
// with only the store can see who has access, but not open anything.
//
// A record is signed by an owner key (Ed25519, see Signer), and it is what decides who
// the document is encrypted to, so it is never trusted unsigned: StoreKeyring.Recipients
// verifies it against the owners' signing keys and fails closed. A person who can only
// write the store cannot add themselves as a recipient, or change the list, without
// being refused. What the signature does not stop (C.5): a store writer can still delete a
// record, or put back an older validly signed one (a replay), which no signature check can
// tell from the current one. Three layers answer that, none needing the client to remember
// anything except the last:
//
//   - the frame inside every encrypted file carries the level and the record's seq (Binding),
//     so Save, Load and the re-keys cross-check the file with the record and report
//     ErrDowngrade for a record that is missing or older than the file was sealed under;
//   - the owner-signed index (IndexKey, Sharing.VerifyIndex) lists the end-to-end documents,
//     so a deleted or replaced record is found even for a document never opened here;
//   - the optional SeqFloor (MemorySeqFloor, FileSeqFloor, StorageSeqFloor) refuses a record
//     older than the highest seen on this client, and the saver re-asserts a confidential
//     document's level on each save (saver.Saver.Rekey).
const KeysPrefix = "keys:"

// ErrStaleRecord is returned when a document's record is older than the highest sequence
// number the SeqFloor has seen for it (an older signed record was put back), or is missing
// where one was seen. It is not used, because it decides who a document is encrypted to and
// at what level. ErrDowngrade is the same finding made from the files themselves, with no
// client state.
var ErrStaleRecord = errors.New("encrypt: the recipient list is older than one already seen (a replayed record)")

// SeqFloor remembers, per document, the highest record sequence number seen, so that an older
// validly signed record that a store writer put back is refused (ErrStaleRecord). It is
// optional (nil: none, a replay is then not detected on read) and must live somewhere the
// store's writers cannot reach, or it defends nothing: memory of the client, or the owner's
// own device storage, not the store itself.
type SeqFloor interface {
	// Get returns the highest sequence number raised for the document, 0 if none.
	Get(ctx context.Context, docID string) (uint64, error)
	// Raise records seq as seen; a lower value than the one held changes nothing.
	Raise(ctx context.Context, docID string, seq uint64) error
}

// MemorySeqFloor is a SeqFloor in memory: it lasts as long as the process (or the page), so
// it protects a long-lived client, and a fresh one starts with no floor.
type MemorySeqFloor struct {
	mu sync.Mutex
	m  map[string]uint64
}

// Get implements SeqFloor.
func (f *MemorySeqFloor) Get(_ context.Context, docID string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[docID], nil
}

// Raise implements SeqFloor.
func (f *MemorySeqFloor) Raise(_ context.Context, docID string, seq uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]uint64{}
	}
	if seq > f.m[docID] {
		f.m[docID] = seq
	}
	return nil
}

// ErrFingerprint is returned by Share when the fingerprint the owner confirmed is not the key's:
// adding a key is only done on the owner's word about whose it is.
var ErrFingerprint = errors.New("encrypt: the fingerprint confirmed is not the one of this key")

// ErrUnsignedRecord is returned when a recipient list is not signed (or not in the signed
// format): it is not used, because it decides who a document is encrypted to.
var ErrUnsignedRecord = errors.New("encrypt: the recipient list is not signed by an owner key")

// ErrBadSignature is returned when a recipient list's signature is not by any of the owners'
// signing keys, or does not match what it signs (the list was changed or came from another
// document).
var ErrBadSignature = errors.New("encrypt: the recipient list's signature is not valid")

// ErrNoSigner is returned by Sharing operations that change a list when it has no Signer.
var ErrNoSigner = errors.New("encrypt: sharing needs the owner's signing key")

// recordContext separates what the owner signs here from any other use of the key.
const recordContext = "gojiberry/keys-record/v1"

// Signer is the owner's key for signing recipient lists: Ed25519, from an owner's own key,
// so whoever can change who a document is shared with holds the same secret that opens it.
// Each owner key has its own signing key; every one that may change lists must be in
// StoreKeyring.OwnerSigners (and Sharing.OwnerSigners) of everyone who reads them.
type Signer struct{ key ed25519.PrivateKey }

// NewSigner makes a Signer from a 32-byte secret seed.
func NewSigner(seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("encrypt: a signing seed is 32 bytes")
	}
	return &Signer{ed25519.NewKeyFromSeed(seed)}, nil
}

// SignerFromAge derives the signing key of an age identity (an owner's or a PRF-derived
// one): the seed is the HMAC-SHA256 of a fixed context under the identity's secret key, so
// the same identity always gives the same signing key and nothing else uses it.
func SignerFromAge(id *age.X25519Identity) *Signer {
	m := hmac.New(sha256.New, []byte(id.String()))
	m.Write([]byte("gojiberry/keys-record-signer/v1"))
	s, _ := NewSigner(m.Sum(nil))
	return s
}

// SignerFromSSH uses an ed25519 SSH private key (unencrypted PEM) as the signing key. An RSA
// key cannot: use an age identity or another owner key for signing.
func SignerFromSSH(pem []byte) (*Signer, error) {
	k, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	switch k := k.(type) {
	case *ed25519.PrivateKey:
		return &Signer{*k}, nil
	case ed25519.PrivateKey:
		return &Signer{k}, nil
	}
	return nil, errors.New("encrypt: only an ed25519 SSH key can sign recipient lists")
}

// Public is the key that verifies this Signer's signatures: what goes in OwnerSigners.
func (s *Signer) Public() ed25519.PublicKey { return s.key.Public().(ed25519.PublicKey) }

func recordMessage(docID string, payload []byte) []byte {
	return append([]byte(recordContext+"\x00"+docID+"\x00"), payload...)
}

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
	PendingSince int64         `json:"pendingSince,omitempty"`
	Recipients   []sharedEntry `json:"recipients"`
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
	var rec sharedRecord
	var sr signedRecord
	if err := json.Unmarshal(raw, &sr); err != nil {
		return rec, fmt.Errorf("encrypt: the recipient list of %s is damaged: %w", docID, err)
	}
	if len(sr.Sig) == 0 || len(sr.Payload) == 0 {
		return rec, fmt.Errorf("%s: %w", docID, ErrUnsignedRecord)
	}
	msg := recordMessage(docID, sr.Payload)
	ok := false
	for _, k := range owners {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, msg, sr.Sig) {
			ok = true
			break
		}
	}
	if !ok {
		return rec, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	if err := json.Unmarshal(sr.Payload, &rec); err != nil || rec.V != 1 || rec.Doc != docID {
		return sharedRecord{}, fmt.Errorf("%s: %w", docID, ErrBadSignature)
	}
	return rec, nil
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

// Sharing changes who a document is shared with. Adapter is the encrypting adapter over Store,
// whose keyring is a StoreKeyring over the same Store. Signer is the owner's signing key, which
// every change to a list needs; OwnerSigners are further owner keys whose lists are accepted
// (Signer's own key always is).
//
// A change to a list is a read, a change and a write of the record. On a Store with
// docstore.ConditionalAdapter (SaveIf) a change made by someone else in between is noticed
// and the change is made again; on a Store without it, two changes to one document's list at
// the same moment can lose one of them, so serialize them (one owner client, or a Store
// Lock) there. A custom Adapter.Skip must keep the "keys:" prefix (KeysPrefix, which includes
// IndexKey) readable, as DefaultSkip does: the records are plain signed lists, and encrypting
// them would make the keyring unable to read them.
//
// The Adapter must be the encrypting adapter over Store: a change to a list first checks the
// record against the document's stored files (ErrDowngrade) so it never signs over a replayed
// or deleted record, and re-keys through it.
type Sharing struct {
	Store        docstore.Adapter
	Adapter      *Adapter
	Signer       *Signer
	OwnerSigners []ed25519.PublicKey
	// SeqFloor, when set, refuses a record older than the highest seen (ErrStaleRecord) and
	// is raised by every record this Sharing writes. Share the one of the StoreKeyring.
	SeqFloor SeqFloor
	// PendingWarnAfter is how long a pending re-key may last before PendingStatus calls it
	// stuck; 0 is DefaultPendingWarnAfter.
	PendingWarnAfter time.Duration

	now func() time.Time // tests
	// afterRead, when set, is called by mutate between reading the record and checking the
	// files against it (tests).
	afterRead func()
}

func (s *Sharing) verifiers() []ed25519.PublicKey {
	out := append([]ed25519.PublicKey(nil), s.OwnerSigners...)
	if s.Signer != nil {
		out = append(out, s.Signer.Public())
	}
	return out
}

// mutateAttempts is how many times a change to a list is redone when it was changed meanwhile.
const mutateAttempts = 5

// mutateOpts changes how mutate reads and numbers a record.
type mutateOpts struct {
	// recover is Recover's: the record is read without the floor and without the check against
	// the files' frames (it may be the replayed or missing one being replaced), and the new seq
	// is above minSeq.
	recover bool
	minSeq  uint64
}

// mutate reads and verifies the list, lets fn change it, and writes it back signed, only if it
// is still what was read (on a store that writes conditionally). It returns the list as it is
// after fn, whether or not fn changed it.
//
// Before anything is written the record is cross-checked with what the document's newest
// stored file says it was sealed under (Adapter.checkDocument): a record that is missing, or
// older than that file, is ErrDowngrade, never signed over (signing the replayed record again
// would make the replay the truth). Sharing.Recover is the way past it.
func (s *Sharing) mutate(ctx context.Context, docID string, fn func(*sharedRecord) (changed bool, err error)) (sharedRecord, error) {
	return s.mutateWith(ctx, docID, mutateOpts{}, fn)
}

func (s *Sharing) mutateWith(ctx context.Context, docID string, o mutateOpts, fn func(*sharedRecord) (changed bool, err error)) (sharedRecord, error) {
	if s.Signer == nil {
		return sharedRecord{}, ErrNoSigner
	}
	floor := s.SeqFloor
	if o.recover {
		floor = nil
	}
	raced := false
	for range mutateAttempts {
		rec, digest, err := loadRecord(ctx, s.Store, docID, s.verifiers(), floor)
		if err != nil {
			return rec, err
		}
		before := rec.binding(digest != "") // fn changes rec: the check is of the record as read
		changed, err := fn(&rec)
		if err != nil || !changed {
			return rec, err
		}
		if s.afterRead != nil {
			s.afterRead()
		}
		if !o.recover && s.Adapter != nil {
			if err := s.Adapter.checkDocumentByID(ctx, docID, before); err != nil {
				if !raced && errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrForgedFrame) {
					// A client that completed a switch between the read of the record and the
					// files leaves files sealed under a newer record: read the record again
					// once. A record really missing or replaced is the same the second time.
					raced = true
					continue
				}
				return rec, err
			}
		}
		rec.V, rec.Doc = 1, docID
		switch {
		case !rec.Pending:
			rec.PendingSince = 0
		case !before.Pending || rec.PendingSince == 0:
			rec.PendingSince = s.clock().Unix()
		}
		if rec.Seq < o.minSeq {
			rec.Seq = o.minSeq
		}
		if rec.Seq == math.MaxUint64 {
			return rec, errors.New("encrypt: the record's sequence number cannot go higher: a file claims the highest one possible")
		}
		rec.Seq++
		payload, err := json.Marshal(rec)
		if err != nil {
			return rec, err
		}
		data, err := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(s.Signer.key, recordMessage(docID, payload)), Signer: s.Signer.Public()})
		if err != nil {
			return rec, err
		}
		if c, ok := s.Store.(docstore.ConditionalAdapter); ok {
			err = c.SaveIf(ctx, KeysPrefix+docID, data, digest)
			if errors.Is(err, docstore.ErrConflict) {
				continue
			}
		} else {
			err = s.Store.Save(ctx, KeysPrefix+docID, data)
		}
		if err == nil && s.SeqFloor != nil {
			err = floorRaise(ctx, s.SeqFloor, docID, rec.Seq)
		}
		return rec, err
	}
	return sharedRecord{}, docstore.ErrConflict
}

// List says who a document is shared with, besides the owner's own keys. A list whose
// signature does not verify is an error.
func (s *Sharing) List(ctx context.Context, docID string) ([]Shared, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	if err != nil {
		return nil, err
	}
	out := make([]Shared, 0, len(rec.Recipients))
	for _, e := range rec.Recipients {
		r, err := parseShared(e)
		if err != nil {
			return nil, fmt.Errorf("encrypt: a recipient of %s cannot be read: %w", docID, err)
		}
		out = append(out, Shared{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Key: r.Text, Hint: r.Hint, Added: time.Unix(e.Added, 0)})
	}
	return out, nil
}

// Pending says whether a change to the list was saved whose re-key did not finish, so some
// files of the document may still be encrypted to the old list. Repair (or Remove or Add
// again) finishes it. While it is set Load does not apply the strict rule that a file wrapped
// below the document's level is a downgrade (the stale files are the ones the re-key has not
// reached), so a flag that stays set weakens that check for the document: PendingStatus says
// for how long, and the saver's Verify reports it (saver.VerifyReport.Pending).
func (s *Sharing) Pending(ctx context.Context, docID string) (bool, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	return rec.Pending, err
}

// DefaultPendingWarnAfter is how long a re-key may be in flight before PendingStatus calls it
// stuck.
const DefaultPendingWarnAfter = 30 * time.Minute

// PendingStatus is Pending with the time it was set.
type PendingStatus struct {
	Pending bool
	// Since is when the record was marked pending (zero if it did not say).
	Since time.Time
	// Stuck says it has been pending for longer than Sharing.PendingWarnAfter (30 minutes if
	// unset), or for an unknown time: a re-key that was cut short and never repaired. Run Repair.
	Stuck bool
}

func (s *Sharing) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Sharing) pendingStatus(rec sharedRecord) PendingStatus {
	st := PendingStatus{Pending: rec.Pending}
	if !rec.Pending {
		return st
	}
	warn := s.PendingWarnAfter
	if warn <= 0 {
		warn = DefaultPendingWarnAfter
	}
	if rec.PendingSince > 0 {
		st.Since = time.Unix(rec.PendingSince, 0)
	}
	st.Stuck = st.Since.IsZero() || s.clock().Sub(st.Since) > warn
	return st
}

// PendingStatus reads the record and says whether, and for how long, a re-key is pending.
func (s *Sharing) PendingStatus(ctx context.Context, docID string) (PendingStatus, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	if err != nil {
		return PendingStatus{}, err
	}
	return s.pendingStatus(rec), nil
}

// PendingDoc is a document whose re-key is pending, as PendingDocs lists it.
type PendingDoc struct {
	Doc string
	PendingStatus
}

// PendingDocs lists the documents (sorted by ID) whose record is pending, with how long: the
// ones a Repair would finish. A record that does not verify is skipped and reported in the
// error, joined, beside the documents found.
func (s *Sharing) PendingDocs(ctx context.Context) ([]PendingDoc, error) {
	metas, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []PendingDoc
	var errs []error
	for _, m := range metas {
		doc, ok := strings.CutPrefix(m.ID, KeysPrefix)
		if !ok || m.ID == IndexKey || !docfile.ValidID(doc) {
			continue
		}
		rec, _, err := loadRecord(ctx, s.Store, doc, s.verifiers(), s.SeqFloor)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if rec.Pending {
			out = append(out, PendingDoc{Doc: doc, PendingStatus: s.pendingStatus(rec)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Doc < out[j].Doc })
	return out, errors.Join(errs...)
}

// PendingHook returns the function to set as saver.Saver.PendingCheck: the documents whose
// re-key is pending, and, among them, the ones pending for too long (PendingStatus.Stuck).
func PendingHook(sharing *Sharing) func(ctx context.Context, target string) (pending, stuck []string, err error) {
	return func(ctx context.Context, _ string) (pending, stuck []string, err error) {
		docs, err := sharing.PendingDocs(ctx)
		for _, d := range docs {
			pending = append(pending, d.Doc)
			if d.Stuck {
				stuck = append(stuck, d.Doc)
			}
		}
		return pending, stuck, err
	}
}

// finishAttempts is how many times finish re-keys a document whose list changed while it
// was being re-keyed.
const finishAttempts = 5

// finish encrypts every file of the document again to the current list (always: it is
// idempotent), and once that worked clears the pending mark. It returns how many files were
// written (the last pass's).
//
// The mark is cleared only if the list is still the one the re-key started from (its seq is
// unchanged): a Remove or Add that saved a newer list meanwhile set it pending for files this
// pass may have sealed to the old one, so the document is re-keyed again.
func (s *Sharing) finish(ctx context.Context, docID string) (int, error) {
	n, _, _, err := s.finishFiles(ctx, docID)
	return n, err
}

// finishFiles is finish that also says, when a pass failed, which files were left encrypted to
// their old recipients (stale) and which of them could not be opened (unreadable).
func (s *Sharing) finishFiles(ctx context.Context, docID string) (n int, stale, unreadable []string, err error) {
	for range finishAttempts {
		rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
		if err != nil {
			return n, nil, nil, err
		}
		seq := rec.Seq
		var done []string
		done, stale, unreadable, err = s.Adapter.RekeyDocumentReport(ctx, docID)
		if n = len(done); err != nil {
			return n, stale, unreadable, err
		}
		again := false
		// A re-key by an adapter that signs leaves every file with a signed frame: the record
		// then says so (Signed), which is what makes an unsigned frame in the document forged.
		signs := s.Adapter != nil && s.Adapter.Signer != nil
		if _, err = s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
			if !rec.Pending && (rec.Signed || !signs) {
				return false, nil
			}
			if rec.Seq != seq {
				again = true
				return false, nil
			}
			rec.Pending = false
			rec.Signed = rec.Signed || signs
			return true, nil
		}); err != nil {
			return n, nil, nil, err
		}
		if !again {
			return n, nil, nil, nil
		}
	}
	return n, nil, nil, docstore.ErrConflict
}

// Add shares a document with a key. confirmed is the fingerprint the owner says they checked
// with the person it belongs to; it must be the key's own (ErrFingerprint), computed here from
// the key's text, so a key pasted for the wrong person is not added by a click alone. The
// list is saved (signed), then every file of the document (its current and previous copies
// and its versions) is encrypted again to it, so the new person can open all of it. Adding a
// key that is already listed still encrypts the files again (it finishes an earlier add that
// was cut short); it changes nothing else.
func (s *Sharing) Add(ctx context.Context, docID string, r Recipient, confirmed string) error {
	if !docfile.ValidID(docID) {
		return errors.New("encrypt: not a document ID")
	}
	if r.Text == "" {
		return errors.New("encrypt: the recipient has no key text")
	}
	parsed, err := parseShared(sharedEntry{Type: r.Type, Key: r.Text, Label: r.Label, Hint: r.Hint})
	if err != nil {
		return err
	}
	if parsed.Fingerprint == "" || strings.TrimSpace(confirmed) != parsed.Fingerprint {
		return ErrFingerprint
	}
	if _, err := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == parsed.Fingerprint {
				return false, nil
			}
		}
		label := r.Label
		if label == "" {
			label = parsed.Label
		}
		rec.Recipients = append(rec.Recipients, sharedEntry{Label: label, Type: parsed.Type, Fingerprint: parsed.Fingerprint, Key: parsed.Text, Hint: parsed.Hint, Added: time.Now().Unix()})
		rec.Pending = true
		return true, nil
	}); err != nil {
		return err
	}
	_, err = s.finish(ctx, docID)
	return err
}

// Remove takes a key out and encrypts the document again, with a new data key, to who is left.
// That stops them reading what is saved from now on; nothing takes back what they already
// opened or copied, and a caller should say so. It returns how many files were encrypted again.
//
// It can be run again after a failure: the list is saved first, marked pending, and the
// mark is cleared only when every file is re-keyed. Remove of a key that is no longer listed
// finishes a pending re-key, and is docstore.ErrNotFound only when nothing is pending.
func (s *Sharing) Remove(ctx context.Context, docID, fingerprint string) (int, error) {
	found := false
	rec, err := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		kept := make([]sharedEntry, 0, len(rec.Recipients))
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == fingerprint {
				found = true
				continue
			}
			kept = append(kept, e)
		}
		if !found {
			return false, nil
		}
		rec.Recipients, rec.Pending = kept, true
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if !found && !rec.Pending {
		return 0, docstore.ErrNotFound
	}
	return s.finish(ctx, docID)
}

// Repair encrypts every file of a document again to its current list and clears a pending
// mark: the way to finish a share or removal that was cut short, whichever key it was for.
func (s *Sharing) Repair(ctx context.Context, docID string) (int, error) {
	if !docfile.ValidID(docID) {
		return 0, errors.New("encrypt: not a document ID")
	}
	if _, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor); err != nil {
		return 0, err
	}
	return s.finish(ctx, docID)
}

// Usage lists the documents (their IDs, sorted) that removing a key changes: the ones it is a
// recipient of, and the ones with a pending re-key (a change that was cut short, whose files
// may still be encrypted to an older list), so a caller can say what removing it will do and a
// re-run finds what an earlier run left. A record that does not verify is skipped and
// reported in the error, joined, beside the documents found.
func (s *Sharing) Usage(ctx context.Context, fingerprint string) ([]string, error) {
	metas, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var docs []string
	var errs []error
	for _, m := range metas {
		doc, ok := strings.CutPrefix(m.ID, KeysPrefix)
		if !ok || m.ID == IndexKey {
			continue
		}
		rec, _, err := loadRecord(ctx, s.Store, doc, s.verifiers(), s.SeqFloor)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		hit := rec.Pending
		for _, e := range rec.Recipients {
			if p, err := parseShared(e); err == nil && p.Fingerprint == fingerprint {
				hit = true
				break
			}
		}
		if hit {
			docs = append(docs, doc)
		}
	}
	sort.Strings(docs)
	return docs, errors.Join(errs...)
}

// RemoveEverywhere takes a key out of every document it is shared with: each is decrypted and
// encrypted again, with a new data key, to who is left. It returns the documents done and how
// many files were encrypted again. A document that fails does not stop the others: every
// failure is returned, joined and each naming its document, and running it again finishes
// those (Usage lists a document whose re-key is pending even when the key is already out of
// its list, and Remove finishes it). As with Remove, nothing takes back what the key's owner
// already opened or copied.
func (s *Sharing) RemoveEverywhere(ctx context.Context, fingerprint string) (docs []string, files int, err error) {
	all, uerr := s.Usage(ctx, fingerprint)
	var errs []error
	if uerr != nil {
		errs = append(errs, uerr)
	}
	for _, doc := range all {
		n, err := s.Remove(ctx, doc, fingerprint)
		files += n
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", doc, err))
			continue
		}
		docs = append(docs, doc)
	}
	return docs, files, errors.Join(errs...)
}
