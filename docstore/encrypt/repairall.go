package encrypt

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// RepairSkipReason says why RepairAll did not repair a document (or other encrypted item).
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
	// frame). A document is re-keyed all or none, so none of its files is written (RepairAll
	// found it so in its scan; see SkipError for one that fails while being written).
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
	// SkipConflict: a writer kept changing the document while it was being repaired (Repair gave
	// up after its attempts). The document may be partly rewritten (some files already signed,
	// the record not yet saying so); nothing is lost and running it again finishes it.
	SkipConflict RepairSkipReason = "conflict"
	// SkipError: any other failure (the store, the record); Err says which. A store error in the
	// middle of a document leaves it partly rewritten (some files in v3, the record not yet
	// flipped): nothing is lost, every file still opens, and running it again finishes it.
	SkipError RepairSkipReason = "error"
)

// RepairSkip is a document RepairAll did not repair, and why.
type RepairSkip struct {
	// Doc is the document's id or, for an encrypted item that is not a document's (see
	// RepairAllReport.Total), the item's id.
	Doc    string
	Reason RepairSkipReason
	// Files are the files of the document that caused it, when they are known.
	Files []string
	Err   error
}

// RepairAllReport is what RepairAll did. Every document or item it looked at is in exactly one of
// Signed, Repaired, Skipped, or (in a DryRun) Todo, or is Remaining: Total is their sum.
type RepairAllReport struct {
	// Total is the number of units in the store that Skip does not leave readable: the documents
	// (ids DocID finds, with their versions) and every other encrypted item (expiry:, collab:,
	// passkey/, public/ ... each one alone, as RekeyAll groups them).
	Total int
	// Signed had every file in a signed v3 frame already and was not touched.
	Signed int
	// Repaired had unsigned files and was sealed again, signed, by Sharing.Repair.
	Repaired int
	// Files is how many files Repaired documents had written (all their files, as Repair re-keys
	// a document as a unit).
	Files int
	// Skipped were not repaired, with the reason. Most were not written at all; a document that
	// failed or conflicted while being written (SkipError, SkipConflict) may be partly rewritten,
	// which loses nothing and a rerun finishes.
	Skipped []RepairSkip
	// Todo lists, in a DryRun, the documents a real run would repair.
	Todo []string
	// Remaining were not looked at because the context ended or Limit was reached; run it again.
	Remaining int
}

// Left is how many documents or items still have unsigned frames or could not be checked:
// Skipped, Todo and Remaining. Zero means every encrypted file Skip does not leave readable was
// in a signed frame when RepairAll looked: documents, and items that are not a document's too.
// It says nothing of files stored in the clear that the adapter does not read (those are
// SkipPlaintext, so not zero), of what was saved after the scan, or of a file Skip leaves readable.
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
	// to need it): the rest, in id order, is Remaining. There is no cursor: a second run starts
	// at the first id again, finds the repaired ones Signed (one scan each) and carries on.
	Limit int
	// DryRun reports what would be repaired (Todo) and writes nothing.
	DryRun bool
}

// RepairAll moves a whole store off the unsigned v1/v2 frames: it lists the store once, groups
// the files into documents (and, for an encrypted item that is not a document's, the item
// alone, as RekeyAll groups them), and for each one that has a file in an unsigned frame
// repairs it. A document goes through Repair (so the same lock, the same checks against the
// record and the same conditional writes apply), which seals all its files again signed by the
// Adapter's Signer and then says in the record that every file is signed (from then on an
// unsigned frame in it is ErrForgedFrame). Any other item is sealed again, signed, by
// Adapter.Rekey, the path RekeyAll takes; it has no record to flip.
//
// A document that was never shared and has no keys: record (an at-rest document) gets one
// when it is repaired: a signed record with Signed set. That is intended: from then on an
// unsigned frame in it is forged too.
//
// It never downgrades and never deletes: a document is repaired as a unit or not at all, and
// one it cannot repair is reported in Skipped with the reason (no signer, no key held,
// unreadable, forged, downgrade, plaintext, conflict). Most are not written at all; one that
// fails or conflicts while being written (SkipError, SkipConflict) may be partly rewritten, some
// files in v3 and the record not yet flipped. Nothing is lost by that (every file opens, as
// before) and running it again finishes the document. It does not use AcceptUnsigned: a file
// a record already calls forged stays for the owner to settle by hand with Repair(doc,
// AcceptUnsigned()). The scan that decides which documents need it takes no lock; Repair
// looks again under the lock, so a Save that signs a file meanwhile is harmless, and one that
// writes an unsigned file after the scan is caught by the next run.
//
// Cost: the store is listed once, then each document is scanned by loading every one of its
// files (the Adapter interface has no header-only read, so a large store costs a read of all
// of it per run, whether or not anything is repaired), and each repaired document is listed
// again, under the lock, by Repair.
//
// It is idempotent (a second run finds every repaired document Signed and writes nothing) and
// restartable (a run that is cut short, by the context, Limit, a crash or a failed document, is
// finished by running it again: there is no saved cursor, so it starts at the first id in id
// order and finds the finished ones Signed). It is safe next to other writers for the reason
// Repair is. When the context ends it returns the report so far and the context's error; a
// store that cannot be listed returns that error. A skipped document is not an error: see Left.
func (s *Sharing) RepairAll(ctx context.Context, o RepairAllOptions) (RepairAllReport, error) {
	var rep RepairAllReport
	if s.Adapter == nil {
		return rep, errors.New("encrypt: RepairAll needs the Sharing's Adapter")
	}
	metas, err := s.Adapter.Inner.List(ctx)
	if err != nil {
		return rep, err
	}
	// One listing for the whole run: the files of each group are known from it, and Repair (or
	// Rekey) lists again under its lock for the groups it writes.
	groups := map[string][]string{}
	var docs []string
	for _, m := range metas {
		if s.Adapter.skip(m.ID) {
			continue
		}
		g := groupOf(m.ID)
		if _, ok := groups[g]; !ok {
			docs = append(docs, g)
		}
		groups[g] = append(groups[g], m.ID)
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
		sc := s.Adapter.scanFrames(ctx, groups[doc])
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
			n, err := s.repairGroup(ctx, doc, groups[doc])
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

// repairGroup seals one group again, signed: a document through Repair, any other encrypted item
// through Adapter.Rekey (the files it was listed with, one).
func (s *Sharing) repairGroup(ctx context.Context, group string, ids []string) (int, error) {
	if docfile.ValidID(group) {
		return s.Repair(ctx, group)
	}
	n := 0
	for _, id := range ids {
		if err := s.Adapter.Rekey(ctx, id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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

// scanFrames reads the frame header of every one of ids, the files of a document or the one
// item of a group (listed by the caller, once for the whole run; no lock: a hint that Repair
// checks again under the lock), by loading the whole file, and sorts them into unsigned ones and the reason, if any, that the
// document cannot be repaired. A file that cannot be judged settles the document: it is not
// repaired even if other files are unsigned, as a re-key is all or none.
func (a *Adapter) scanFrames(ctx context.Context, ids []string) frameScan {
	var sc frameScan
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
