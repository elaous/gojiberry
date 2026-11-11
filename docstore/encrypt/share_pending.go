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

// Pending says whether a list change is unfinished.
func (s *Sharing) Pending(ctx context.Context, docID string) (bool, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	return rec.Pending, err
}

// DefaultPendingWarnAfter is how long a re-key may stay pending.
const DefaultPendingWarnAfter = 30 * time.Minute

// PendingStatus is Pending with the time it was set.
type PendingStatus struct {
	Pending bool
	// Since is when the record was marked pending.
	Since time.Time
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

// PendingStatus reads the record and says whether it is pending.
func (s *Sharing) PendingStatus(ctx context.Context, docID string) (PendingStatus, error) {
	rec, _, err := loadRecord(ctx, s.Store, docID, s.verifiers(), s.SeqFloor)
	if err != nil {
		return PendingStatus{}, err
	}
	return s.pendingStatus(rec), nil
}

// PendingDoc is a document whose re-key is pending.
type PendingDoc struct {
	Doc string
	PendingStatus
}

// PendingDocs lists the pending documents, sorted by ID.
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

// PendingHook returns the function to set as the saver's hook.
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

// UnsignedHook returns the function to set as the saver's hook.
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

const finishAttempts = 5

func (s *Sharing) finish(ctx context.Context, docID string) (int, error) {
	n, _, _, err := s.finishFiles(ctx, docID, repairOpts{})
	return n, err
}

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
				late, err := s.Adapter.unsignedFiles(ctx, docID)
				if errors.Is(err, ErrForgedFrame) {
					// a v3 file whose signature fails is not one saved unsigned
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
			// tidying never fails a re-key that worked
			if err := s.clearUnsignedLate(ctx, docID); err != nil {
				log.Printf("encrypt: %s: the unsigned-late mark could not be checked and was kept: %v", docID, err)
			}
			return n, nil, nil, nil
		}
	}
	return n, nil, nil, docstore.ErrConflict
}

const unsignedLateCap = 64

func listCapped(files []string) string {
	if len(files) <= unsignedLateCap {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s ... and %d more", strings.Join(files[:unsignedLateCap], ", "), len(files)-unsignedLateCap)
}

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

// UnsignedDoc is a document whose re-key ended with unsigned files.
type UnsignedDoc struct {
	Doc string
	// at most 64 ids (first by id); Count is the total
	Files []string
	Count int
	// Since is when the re-key found them.
	Since time.Time
	// Reason says what to do.
	Reason string
}

// UnsignedDocs lists the documents with unsigned files, sorted by ID.
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
