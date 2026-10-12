package encrypt

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"math"
	"sort"
	"strings"
	"time"
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
	// beforeFlip, when set, is called by finishFiles after the files were re-keyed and before the
	// record is changed to say they are signed (tests).
	beforeFlip func()
}

// ErrUnsignedAfterFlip is returned by the re-key (Repair, Add, Remove, SetProtection, Recover)
// when, after it wrote the record (which says Signed, whether it was Signed before and only
// Pending or is marked so now), a file of the document is still unsigned: a client with no
// Signer saved it in the few steps between the re-key's last look and the write of the record. The record does say Signed and the file is refused as forged (it
// wraps ErrForgedFrame); the owner checks it is theirs and runs Repair with AcceptUnsigned.
var ErrUnsignedAfterFlip = errors.New("encrypt: a file was saved unsigned while the re-key was finishing, and the record now says Signed")

// RepairOption changes what Repair and Recover accept.
type RepairOption func(*repairOpts)

type repairOpts struct{ acceptUnsigned bool }

// AcceptUnsigned is the owner's confirmation that a file of the document whose frame is not
// signed (v1, v2) is theirs, so that Repair (or Recover) seals it again with a signed frame
// even though the record says every file is signed (or the Adapter has RequireSigned): the
// state a client without the owner's signing key, or a build that wrote unsigned frames, leaves
// behind once the record has been marked signed, where Save, Load and every re-key otherwise
// refuse the file as forged (ErrForgedFrame). Without the option these refuse, naming the file.
//
// It launders nothing that carries a signature: a v3 frame whose signature is not an owner's, or
// that does not match its content, is a forgery and is refused (ErrForgedFrame) with or without
// it. It cannot tell an unsigned file the owner's own client wrote from one minted by someone
// who knows a recipient's public key (an unsigned frame carries no proof): confirm that the
// file is yours (its content, with Load through an Adapter that has no RequireSigned, if need
// be) before using it. It needs the Adapter's Signer (ErrNoFrameSigner otherwise).
func AcceptUnsigned() RepairOption { return func(o *repairOpts) { o.acceptUnsigned = true } }

func repairOptions(opts []RepairOption) repairOpts {
	var o repairOpts
	for _, f := range opts {
		f(&o)
	}
	return o
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
	// noCheck skips the cross-check with the files' frames (not the floor): for a write that
	// only records that a file is unsigned, which the check would refuse for that very file.
	noCheck bool
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
		if !o.recover && !o.noCheck && s.Adapter != nil {
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

// Resign signs the document's list again with Signer, unchanged but for its sequence number: what
// a store does for every list when the owner key that signed them is taken out of OwnerSigners
// (it would no longer verify). OwnerSigners must still hold the key that signed it now.
func (s *Sharing) Resign(ctx context.Context, docID string) error {
	_, err := s.mutate(ctx, docID, func(*sharedRecord) (bool, error) { return true, nil })
	return err
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
//
// A file whose frame is not signed in a document whose record says every file is signed is
// ErrForgedFrame (naming the file) and nothing is written, unless the owner confirms with the
// AcceptUnsigned option; see there for what that does and does not accept.
func (s *Sharing) Repair(ctx context.Context, docID string, opts ...RepairOption) (int, error) {
	if !docfile.ValidID(docID) {
		return 0, errors.New("encrypt: not a document ID")
	}
	if _, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor); err != nil {
		return 0, err
	}
	n, _, _, err := s.finishFiles(ctx, docID, repairOptions(opts))
	return n, err
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
