package encrypt

import (
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"log"
	"slices"
	"sort"
	"strings"
	"time"
)

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

// UnsignedHook returns the function to set as saver.Saver.UnsignedCheck: the IDs of the documents
// UnsignedDocs lists (a re-key left a file saved unsigned). It is a full scan of the keys: records.
func UnsignedHook(sharing *Sharing) func(ctx context.Context, target string) ([]string, error) {
	return func(ctx context.Context, _ string) ([]string, error) {
		docs, err := sharing.UnsignedDocs(ctx)
		var out []string
		for _, d := range docs {
			out = append(out, d.Doc)
		}
		return out, err
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
	n, _, _, err := s.finishFiles(ctx, docID, repairOpts{})
	return n, err
}

// finishFiles is finish that also says, when a pass failed, which files were left encrypted to
// their old recipients (stale) and which of them could not be opened (unreadable).
//
// A client without the Signer that read the record before it said Signed can save an unsigned
// file at any moment up to the change of the record (Saves do not change the record, so the change
// cannot be made conditional on them). So just before the record is changed to say Signed every
// file is looked at again: one that is not signed is sealed again (another pass) first. The
// window left is the few steps between that look and the write of the record; a file saved in it
// is what Repair with AcceptUnsigned recovers.
func (s *Sharing) finishFiles(ctx context.Context, docID string, ro repairOpts) (n int, stale, unreadable []string, err error) {
	if ro.acceptUnsigned && (s.Adapter == nil || s.Adapter.Signer == nil) {
		return 0, nil, nil, fmt.Errorf("%w: %s: AcceptUnsigned seals files again with the owner's signature", ErrNoFrameSigner, docID)
	}
	for range finishAttempts {
		rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
		if err != nil {
			return n, nil, nil, err
		}
		seq := rec.Seq
		var done []string
		done, stale, unreadable, err = s.Adapter.rekeyDocumentReport(ctx, docID, rekeyOpts(ro))
		if n = len(done); err != nil {
			return n, stale, unreadable, err
		}
		again, flipped := false, false
		// A re-key by an adapter that signs leaves every file with a signed frame: the record
		// then says so (Signed), which is what makes an unsigned frame in the document forged.
		signs := s.Adapter != nil && s.Adapter.Signer != nil
		if s.beforeFlip != nil {
			s.beforeFlip()
		}
		var flipSeq uint64
		if flipRec, merr := s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
			if !rec.Pending && (rec.Signed || !signs) {
				return false, nil
			}
			if rec.Seq != seq {
				again = true
				return false, nil
			}
			if signs && !rec.Signed {
				// Last look, as close to the write as it can be: an unsigned file that a
				// client without the Signer saved since the re-key would be forged from the
				// flip on. Seal it again (another pass) before saying every file is signed.
				unsigned, err := s.Adapter.unsignedFiles(ctx, docID)
				if err != nil {
					return false, err
				}
				if len(unsigned) > 0 {
					again = true
					return false, nil
				}
			}
			rec.Pending = false
			rec.Signed = rec.Signed || signs
			flipped = signs
			return true, nil
		}); merr != nil {
			return n, nil, nil, merr
		} else {
			flipSeq = flipRec.Seq
		}
		if !again {
			if flipped {
				// Look again, now that the record says Signed: a save by a client with no
				// Signer that landed after the look above is found here, and a save that
				// landed after this one is told by its own writer (Adapter.afterUnsignedWrite).
				// Between them no such file goes unnoticed.
				late, err := s.Adapter.unsignedFiles(ctx, docID)
				if errors.Is(err, ErrForgedFrame) {
					// A v3 file whose signature does not verify: not one saved unsigned.
					return n, nil, nil, fmt.Errorf("encrypt: a file with a bad signature was found after the record was written: %w", err)
				}
				if err != nil {
					return n, nil, nil, err
				}
				if len(late) > 0 {
					sort.Strings(late)
					named := listCapped(late)
					if merr := s.markUnsignedLate(ctx, docID, late, flipSeq); merr != nil {
						return n, nil, nil, fmt.Errorf("%w: %w: %s"+unsignedHint+" (the document could not be marked for UnsignedDocs: %w)", ErrUnsignedAfterFlip, ErrForgedFrame, named, merr)
					}
					return n, nil, nil, fmt.Errorf("%w: %w: %s"+unsignedHint, ErrUnsignedAfterFlip, ErrForgedFrame, named)
				}
			}
			// Tidying the mark never fails a re-key that worked: if the files cannot be looked
			// at, the mark stays (UnsignedDocs keeps listing the document) and the next re-key
			// or Repair tries again.
			if err := s.clearUnsignedLate(ctx, docID); err != nil {
				log.Printf("encrypt: %s: the unsigned-late mark could not be checked and was kept: %v", docID, err)
			}
			return n, nil, nil, nil
		}
	}
	return n, nil, nil, docstore.ErrConflict
}

// unsignedLateCap is how many file ids the signed record and the error text keep when the second
// look finds unsigned files: the ids come from the store's listing, so a store writer could
// otherwise grow the record, and the error, without bound.
const unsignedLateCap = 64

// listCapped joins the ids (sorted) with commas, at most unsignedLateCap of them, and says how
// many more there were.
func listCapped(files []string) string {
	if len(files) <= unsignedLateCap {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s ... and %d more", strings.Join(files[:unsignedLateCap], ", "), len(files)-unsignedLateCap)
}

// markUnsignedLate records in the signed record (still Signed, not Pending) the files the second
// look found unsigned, so UnsignedDocs lists the document from any client until it is settled.
// It keeps the first unsignedLateCap ids (by id) and the total count. A new mark replaces the
// one before and keeps its Since.
//
// The write skips the cross-check with the files (it would refuse the very file it records), so
// it applies only to the record the flip just wrote: wantSeq is that record's seq, and a
// different one (a store writer put back an older signed record, or a client changed the list)
// is refused rather than signed again, which would make the replay the truth. The refusal wraps
// ErrDowngrade because a replayed record and a concurrent legitimate seq bump are indistinguishable
// at that point, so a caller seeing ErrDowngrade together with ErrUnsignedAfterFlip should rerun
// the finishing operation or Repair before treating it as an attack.
func (s *Sharing) markUnsignedLate(ctx context.Context, docID string, files []string, wantSeq uint64) error {
	files = slices.Clone(files)
	sort.Strings(files)
	count := len(files)
	if count > unsignedLateCap {
		files = files[:unsignedLateCap]
	}
	_, err := s.mutateWith(ctx, docID, mutateOpts{noCheck: true}, func(rec *sharedRecord) (bool, error) {
		if rec.Seq != wantSeq {
			return false, fmt.Errorf("encrypt: the record is no longer the one the re-key wrote (seq %d, want %d): %w", rec.Seq, wantSeq, ErrDowngrade)
		}
		if !rec.Signed {
			return false, nil
		}
		if slices.Equal(rec.UnsignedLate, files) && rec.UnsignedLateCount == count {
			return false, nil
		}
		rec.UnsignedLate, rec.UnsignedLateCount = files, count
		if rec.UnsignedLateSince == 0 {
			rec.UnsignedLateSince = s.clock().Unix()
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("encrypt: marking %s for UnsignedDocs: %w", docID, err)
	}
	return nil
}

// clearUnsignedLate drops the mark once no file of the document is unsigned (the owner sealed
// them again with AcceptUnsigned, or they were replaced or deleted). It reads the files only
// when the record carries the mark.
func (s *Sharing) clearUnsignedLate(ctx context.Context, docID string) error {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	if err != nil || len(rec.UnsignedLate) == 0 {
		return err
	}
	left, err := s.Adapter.unsignedFiles(ctx, docID)
	if err != nil || len(left) > 0 {
		return err
	}
	_, err = s.mutate(ctx, docID, func(rec *sharedRecord) (bool, error) {
		if len(rec.UnsignedLate) == 0 {
			return false, nil
		}
		rec.UnsignedLate, rec.UnsignedLateCount, rec.UnsignedLateSince = nil, 0, 0
		return true, nil
	})
	return err
}

// UnsignedDoc is a document whose re-key ended in ErrUnsignedAfterFlip: the record says Signed
// and is not pending, but Files were saved unsigned in the last steps and are refused as forged.
type UnsignedDoc struct {
	Doc string
	// Files are the ids of the files that were found unsigned: at most 64 (the first by id),
	// Count is how many there were in all.
	Files []string
	Count int
	// Since is when the re-key found them.
	Since time.Time
	// Reason says what to do.
	Reason string
}

// UnsignedDocs lists the documents (sorted by ID) the re-key left with a late unsigned file (the
// error ErrUnsignedAfterFlip): the ones PendingDocs does not list, because their record is
// not pending, and RepairAll skips as SkipForged. The owner checks the files are theirs and runs
// Repair with AcceptUnsigned, which empties the entry. It reads each record, as PendingDocs does;
// a record that does not verify is skipped and reported in the error, joined.
func (s *Sharing) UnsignedDocs(ctx context.Context) ([]UnsignedDoc, error) {
	metas, err := s.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []UnsignedDoc
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
		if len(rec.UnsignedLate) > 0 {
			d := UnsignedDoc{Doc: doc, Files: slices.Clone(rec.UnsignedLate), Count: max(rec.UnsignedLateCount, len(rec.UnsignedLate)), Reason: "a file was saved unsigned while the re-key was finishing and is refused as forged: if it is yours, run Repair with AcceptUnsigned"}
			if rec.UnsignedLateSince > 0 {
				d.Since = time.Unix(rec.UnsignedLateSince, 0)
			}
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Doc < out[j].Doc })
	return out, errors.Join(errs...)
}
