package encrypt

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/elaous/gojiberry/docstore"
)

// RepairSkipReason says why RepairAll left a document as it was.
type RepairSkipReason string

const (
	// SkipNoSigner: the document has unsigned (v1, v2) files and the Adapter has no Signer, so
	// nothing can seal them again signed. Nothing is written: sealing them again without a
	// signature would only rewrite the same old frame.
	SkipNoSigner RepairSkipReason = "no-signer"
	// SkipNoKey: a file of the document is encrypted to keys the keyring does not hold (a
	// protected document of another owner, or one whose key is not loaded), so it can be neither
	// read nor sealed again. Run it again with the keys.
	SkipNoKey RepairSkipReason = "no-key"
	// SkipUnreadable: a file of the document cannot be opened (damaged, cut short, or not a
	// frame). A document is re-keyed all or none, so none of its files is written.
	SkipUnreadable RepairSkipReason = "unreadable"
	// SkipForged: a file is refused as forged (ErrForgedFrame): a v3 frame whose signature is not
	// an owner's, or an unsigned frame in a document whose record already says every file is
	// signed. Only the owner, after checking the file, may settle the second with
	// Sharing.Repair(doc, AcceptUnsigned()): RepairAll never vouches for a file.
	SkipForged RepairSkipReason = "forged"
	// SkipDowngrade: the document's files and its record disagree (a record deleted or replaced,
	// a file below the record's level or seq). Nothing is written; Sharing.Recover is the way out.
	SkipDowngrade RepairSkipReason = "downgrade"
	// SkipPlaintext: a file of the document is stored in the clear. RepairAll upgrades frames; it
	// does not encrypt what was never encrypted (Adapter.RekeyAll does).
	SkipPlaintext RepairSkipReason = "plaintext"
	// SkipConflict: a writer kept changing the document while it was being repaired. Run it
	// again.
	SkipConflict RepairSkipReason = "conflict"
	// SkipError: any other failure (the store, the record); Err says which.
	SkipError RepairSkipReason = "error"
)

// RepairSkip is a document RepairAll did not repair, and why.
type RepairSkip struct {
	Doc    string
	Reason RepairSkipReason
	// Files are the files of the document that caused it, when they are known.
	Files []string
	Err   error
}

// RepairAllReport is what RepairAll did. Every document it looked at is in exactly one of
// Signed, Repaired, Skipped, or (in a DryRun) Todo, or is Remaining: Total is their sum.
type RepairAllReport struct {
	// Total is the number of documents (ids DocID finds, not the ones Skip leaves out) in the store.
	Total int
	// Signed had every file in a signed v3 frame already and was not touched.
	Signed int
	// Repaired had unsigned files and was sealed again, signed, by Sharing.Repair.
	Repaired int
	// Files is how many files Repaired documents had written (all their files, as Repair re-keys
	// a document as a unit).
	Files int
	// Skipped were left exactly as they were, with the reason.
	Skipped []RepairSkip
	// Todo lists, in a DryRun, the documents a real run would repair.
	Todo []string
	// Remaining were not looked at because the context ended or Limit was reached; run it again.
	Remaining int
}

// Left is how many documents still have unsigned frames or could not be checked: Skipped, Todo
// and Remaining. Zero means the store has no unsigned frame left that RepairAll can see.
func (r RepairAllReport) Left() int { return len(r.Skipped) + len(r.Todo) + r.Remaining }

// RepairProgress is passed to RepairAllOptions.Progress after each document.
type RepairProgress struct {
	Done, Total int
	Doc         string
	// Outcome is "signed", "repaired", "todo" or the RepairSkipReason.
	Outcome string
}

// RepairAllOptions bound and observe RepairAll.
type RepairAllOptions struct {
	// Progress, when set, is called after each document (in id order), from the calling goroutine.
	Progress func(RepairProgress)
	// Limit, when positive, stops after that many documents were repaired (or, in a DryRun, found
	// to need it): the rest is Remaining, and a second run carries on.
	Limit int
	// DryRun reports what would be repaired (Todo) and writes nothing.
	DryRun bool
}

// RepairAll moves a whole store off the unsigned v1/v2 frames: it lists every document, and for
// each one that has a file in an unsigned frame calls Repair (so the same lock, the same checks
// against the record and the same conditional writes apply), which seals all its files again
// signed by the Adapter's Signer and then says in the record that every file is signed (from
// then on an unsigned frame in it is ErrForgedFrame).
//
// It never downgrades and never deletes: a document is repaired as a unit or left as it was, and
// one it cannot repair is reported in Skipped with the reason (no signer, no key held,
// unreadable, forged, downgrade, plaintext, conflict). It does not use AcceptUnsigned: a file a
// record already calls forged stays for the owner to settle by hand with Repair(doc,
// AcceptUnsigned()). The scan that decides which documents need it takes no lock; Repair
// looks again under the lock, so a Save that signs a file meanwhile is harmless, and one that
// writes an unsigned file after the scan is caught by the next run.
//
// It is idempotent (a second run finds every repaired document Signed and writes nothing) and
// resumable (a run that is cut short, by the context, Limit, a crash or a failed document, is
// finished by running it again; documents are visited in id order). It is safe next to other
// writers for the reason Repair is. When the context ends it returns the report so far and
// the context's error; a store that cannot be listed returns that error. A skipped document is
// not an error: see Left.
func (s *Sharing) RepairAll(ctx context.Context, o RepairAllOptions) (RepairAllReport, error) {
	var rep RepairAllReport
	if s.Adapter == nil {
		return rep, errors.New("encrypt: RepairAll needs the Sharing's Adapter")
	}
	metas, err := s.Adapter.Inner.List(ctx)
	if err != nil {
		return rep, err
	}
	seen := map[string]bool{}
	var docs []string
	for _, m := range metas {
		if s.Adapter.skip(m.ID) {
			continue
		}
		if d := DocID(m.ID); d != "" && !seen[d] {
			seen[d] = true
			docs = append(docs, d)
		}
	}
	sort.Strings(docs)
	rep.Total = len(docs)
	signs := s.Adapter.Signer != nil
	acted := 0
	for i, doc := range docs {
		if err := ctx.Err(); err != nil {
			rep.Remaining = len(docs) - i
			return rep, err
		}
		if o.Limit > 0 && acted >= o.Limit {
			rep.Remaining = len(docs) - i
			return rep, nil
		}
		outcome := "signed"
		sc := s.Adapter.scanFrames(ctx, doc)
		switch {
		case ctx.Err() != nil:
			rep.Remaining = len(docs) - i
			return rep, ctx.Err()
		case sc.err != nil:
			outcome = s.skip(&rep, doc, sc.reason, sc.files, sc.err)
		case len(sc.unsigned) == 0:
			rep.Signed++
		case !signs:
			outcome = s.skip(&rep, doc, SkipNoSigner, sc.unsigned, fmt.Errorf("%w: %s", ErrNoFrameSigner, doc))
		case o.DryRun:
			rep.Todo = append(rep.Todo, doc)
			outcome = "todo"
			acted++
		default:
			n, err := s.Repair(ctx, doc)
			switch {
			case err == nil:
				rep.Repaired++
				rep.Files += n
				outcome = "repaired"
				acted++
			case ctx.Err() != nil:
				rep.Remaining = len(docs) - i
				return rep, ctx.Err()
			default:
				reason, files := classifyRepairErr(err)
				outcome = s.skip(&rep, doc, reason, files, err)
			}
		}
		if o.Progress != nil {
			o.Progress(RepairProgress{Done: i + 1, Total: len(docs), Doc: doc, Outcome: outcome})
		}
	}
	return rep, nil
}

func (s *Sharing) skip(rep *RepairAllReport, doc string, reason RepairSkipReason, files []string, err error) string {
	rep.Skipped = append(rep.Skipped, RepairSkip{Doc: doc, Reason: reason, Files: files, Err: err})
	return string(reason)
}

func classifyRepairErr(err error) (RepairSkipReason, []string) {
	switch {
	case errors.Is(err, ErrForgedFrame):
		return SkipForged, nil
	case errors.Is(err, ErrDowngrade), errors.Is(err, ErrStaleRecord):
		return SkipDowngrade, nil
	case errors.Is(err, ErrNoKey):
		return SkipNoKey, nil
	case errors.Is(err, ErrUnreadable):
		return SkipUnreadable, nil
	case errors.Is(err, ErrNoFrameSigner):
		return SkipNoSigner, nil
	case errors.Is(err, docstore.ErrConflict):
		return SkipConflict, nil
	}
	return SkipError, nil
}

// frameScan is what scanFrames found in one document's files.
type frameScan struct {
	unsigned []string // v1, v2 frames
	files    []string // the files behind err
	reason   RepairSkipReason
	err      error // set when the document cannot be judged
}

// scanFrames reads the frame header of every file of a document (no lock: a hint that Repair
// checks again under the lock) and sorts them into unsigned ones and the reason, if any, that the
// document cannot be repaired. A file that cannot be judged settles the document: it is not
// repaired even if other files are unsigned, as a re-key is all or none.
func (a *Adapter) scanFrames(ctx context.Context, docID string) frameScan {
	var sc frameScan
	ids, err := a.filesOf(ctx, docID)
	if err != nil {
		return frameScan{reason: SkipError, err: err}
	}
	fail := func(id string, reason RepairSkipReason, err error) {
		if sc.err == nil || reason == SkipForged { // a forgery is the most important thing to say
			sc.reason, sc.err = reason, fmt.Errorf("%s: %w", id, err)
		}
		sc.files = append(sc.files, id)
	}
	for _, id := range ids {
		d, err := a.Inner.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue // deleted since it was listed
		}
		if err != nil {
			fail(id, SkipError, err)
			continue
		}
		fi, err := a.peekFrameWith(ctx, id, d.Data, true)
		switch {
		case err == nil:
			if fi.version < 3 {
				sc.unsigned = append(sc.unsigned, id)
			}
		case errors.Is(err, ErrNoKey):
			fail(id, SkipNoKey, err)
		case errors.Is(err, ErrNotEncrypted):
			fail(id, SkipPlaintext, err)
		case errors.Is(err, ErrForgedFrame):
			fail(id, SkipForged, err)
		default:
			fail(id, SkipUnreadable, fmt.Errorf("%w: %w", ErrUnreadable, err))
		}
	}
	return sc
}
