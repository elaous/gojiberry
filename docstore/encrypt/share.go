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

// KeysPrefix is the prefix of the sharing records.
const KeysPrefix = "keys:"

// Sharing changes who a document is shared with.
type Sharing struct {
	Store        docstore.Adapter
	Adapter      *Adapter
	Signer       *Signer
	OwnerSigners []ed25519.PublicKey
	// SeqFloor, when set, refuses a record older than the floor.
	SeqFloor SeqFloor
	// PendingWarnAfter is how long a re-key may stay pending.
	PendingWarnAfter time.Duration

	now        func() time.Time // tests
	afterRead  func()
	beforeFlip func()
}

// ErrUnsignedAfterFlip is returned when unsigned files remain.
var ErrUnsignedAfterFlip = errors.New("encrypt: a file was saved unsigned while the re-key was finishing, and the record now says Signed")

// RepairOption changes what Repair and Recover accept.
type RepairOption func(*repairOpts)

type repairOpts struct{ acceptUnsigned bool }

// AcceptUnsigned is the owner's confirmation for unsigned files.
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

const mutateAttempts = 5

type mutateOpts struct {
	recover bool
	minSeq  uint64
	// skips frame cross-check (not floor): only for recording an unsigned file
	noCheck bool
}

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
		before := rec.binding(digest != "")
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
					// a client's switch between record and files: read the record once more
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

// Resign signs the document's list again with Signer.
func (s *Sharing) Resign(ctx context.Context, docID string) error {
	_, err := s.mutate(ctx, docID, func(*sharedRecord) (bool, error) { return true, nil })
	return err
}

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

// Add shares a document with a key.
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

// Remove takes a key out and encrypts the document again.
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

// Usage lists the documents that remove a key.
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

// RemoveEverywhere takes a key out of every document.
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
