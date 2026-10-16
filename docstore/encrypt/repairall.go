package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// encrypts what was never encrypted only with RepairAllOptions.EncryptPlaintext (or
	// Adapter.RekeyAll does).
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

// SignerFingerprint names an owner signing key in a report: the first 8 bytes of the SHA-256 of
// the public key, in hex.
func SignerFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SignerFrames is what RepairAll found signed by one owner signing key.
type SignerFrames struct {
	Key         ed25519.PublicKey
	Fingerprint string // SignerFingerprint(Key)
	// Current is set for the Adapter's own Signer.
	Current bool
	// Files is how many v3 frames the key signed, in how many Docs (documents, or items that are
	// not a document's); Records is how many documents' keys:<ID> records it signed.
	Files, Docs, Records int
}

// RepairAllReport is what RepairAll did. Every document or item it looked at is in exactly one of
// Signed, Repaired, Encrypted, Resigned, Skipped, or (in a DryRun) Todo, or is Remaining: Total
// is their sum (a document that was deleted after the scan is counted by none).
type RepairAllReport struct {
	// Total is the number of units this run covers that Skip does not leave readable: the
	// documents (ids DocID finds, with their versions) and every other encrypted item (expiry:,
	// collab:, passkey/, public/ ... each one alone, as RekeyAll groups them), after
	// RepairAllOptions.After when it is set.
	Total int
	// Signed had every file in a signed v3 frame already and was not touched (in a run without
	// Resign this includes the documents in OtherSigner).
	Signed int
	// Repaired had unsigned files and was sealed again, signed, by Sharing.Repair.
	Repaired int
	// Encrypted (RepairAllOptions.EncryptPlaintext) had a file stored in the clear and was
	// encrypted, signed (a document with unsigned files too: all its files are sealed again).
	Encrypted int
	// Resigned (RepairAllOptions.Resign) had every file signed already, but a frame or the record
	// by a signer other than the Adapter's, and was sealed again with the current one.
	Resigned int
	// Files is how many files Repaired, Resigned and Encrypted documents had written (all their
	// files, as Repair re-keys a document as a unit).
	Files int
	// BySigner says, for every owner signing key found (the documents that could be read, in the
	// state the scan saw before anything was written), how many frames and records it signed, sorted
	// by Fingerprint. A frame whose signer the Adapter does not list is not here (it is
	// ErrForgedFrame, SkipForged); an unsigned one is Repaired's business, not a signer's.
	BySigner []SignerFrames
	// Frames are the frames signed by RepairAllOptions.FramesOf, in document order, as each
	// document's scan found them; empty when it was not set.
	Frames []FrameMark
	// OtherSigner lists, in id order, the documents (and items) that had a frame or a record signed
	// by a key other than the Adapter's Signer and were left so, because Resign was not set. In a
	// DryRun they are in Todo only when Resign is set; with Resign false they are in Signed and
	// OtherSigner, as in a real run. A store with several legitimate owners has such documents on
	// purpose. It is empty when the Adapter has no Signer.
	OtherSigner []string
	// IndexBy is the owner signing key that signed the store's keys:index record as stored (nil
	// when there is no index, it does not verify, or the Adapter has no Signer: nothing to
	// compare with). IndexOther says it is not the current signer. RepairAll with Resign (and
	// no DryRun) signs the index again with Sharing.ResignIndex, then IndexResigned is set; if
	// that fails IndexErr says why and the old key must stay listed. Without Resign, IndexOther
	// is the report: run Sharing.ResignIndex (or RepairAll with Resign) before removing the old
	// key from OwnerSigners, or the index no longer verifies.
	IndexBy                   ed25519.PublicKey
	IndexOther, IndexResigned bool
	IndexErr                  error
	// Skipped were not repaired, with the reason. Most were not written at all; a document that
	// failed or conflicted while being written (SkipError, SkipConflict) may be partly rewritten,
	// which loses nothing and a rerun finishes.
	Skipped []RepairSkip
	// Todo lists, in a DryRun, the documents a real run would repair.
	Todo []string
	// Remaining were not looked at because the context ended or Limit was reached; run it again
	// with After set to Next.
	Remaining int
	// Next is the cursor to give RepairAllOptions.After to carry on: the id of the last group
	// (document or item) this run went through, in id order; After itself when it went through
	// none (so a run cut short before its first group, with After "", has Next "" too: look at
	// Finished or Remaining, not at Next alone). It is "" and Finished is set when the run
	// reached the end of the store, so a caller that persists Next and loops until Finished
	// walks the store exactly once. "Next == "" and Remaining == 0" also means finished.
	Next string
	// Finished says the run went through every group after After to the end of the store, with
	// no error and nothing left (Remaining is 0). It is false when the context ended or Limit was
	// reached, even if Next is "".
	Finished bool
}

// Left is how many documents or items still have unsigned frames or could not be checked:
// Skipped, Todo and Remaining. Zero means every encrypted file Skip does not leave readable was
// in a signed frame when RepairAll looked: documents, and items that are not a document's too.
// A plaintext item outside DefaultSkip counts as SkipPlaintext, so Left is above zero until it
// is skipped (Adapter.Skip) or encrypted (RepairAllOptions.EncryptPlaintext, or
// Adapter.RekeyAll). It covers only the groups after RepairAllOptions.After, and says nothing of
// what was saved after the scan, or of a file Skip leaves readable.
func (r RepairAllReport) Left() int { return len(r.Skipped) + len(r.Todo) + r.Remaining }

// RepairProgress is passed to RepairAllOptions.Progress after each document.
type RepairProgress struct {
	Done, Total int
	Doc         string
	// Outcome is "signed", "repaired", "resigned", "todo", "gone" (deleted since the scan) or the
	// RepairSkipReason.
	Outcome string
}

// RepairAllOptions bound and observe RepairAll.
type RepairAllOptions struct {
	// Progress, when set, is called after each document (in id order), from the calling goroutine.
	Progress func(RepairProgress)
	// Limit, when positive, stops after that many documents were repaired (or, in a DryRun, found
	// to need it): the rest, in id order, is Remaining and Next says where to carry on.
	Limit int
	// After, when set, starts the run after that group: only the documents and items whose id
	// (as a group, ids in byte order) is greater are looked at. Give it the Next of the previous
	// run to walk a large store in slices (Limit) without scanning the finished part again. An
	// id of a file of a document counts as the document's, so any id stored in the group will
	// do; one beyond the end finishes at once. A rerun with After "" starts at the first id and
	// finds the repaired ones Signed (one scan each), as before.
	After string
	// DryRun reports what would be repaired (Todo) and writes nothing.
	DryRun bool
	// Resign also seals again, with the Adapter's Signer (and signs the record, and the keys:index
	// record, with the Sharing's Signer, which must be the same key as the Adapter's: a document
	// or the index that needs it is otherwise skipped, SkipNoSigner, or left, IndexErr), every
	// document that has a v3 frame or a record signed by another owner signer: the step after
	// rotating the owner signer, before removing the old key from every OwnerSigners list (C.5,
	// "Revoking an owner signer"). The old key must still be listed while it runs, or its frames
	// are refused (SkipForged). Use it only when the other signers are retired: a store with
	// several owners would be moved to this one's signature. It needs the Signer (SkipNoSigner
	// without), is idempotent, and does not stop the old key from writing: that ends only when
	// it is removed from the lists.
	Resign bool
	// EncryptPlaintext also encrypts the items stored in the clear that Skip does not leave
	// readable (otherwise SkipPlaintext): each such document or item is sealed with its keyring's
	// recipients and the Adapter's Signer, in a signed v3 frame, by the same Repair and Rekey
	// paths (same locks, same conditional writes, a document is all or none, never an id Skip
	// leaves readable). It needs Adapter.AllowPlaintext (the explicit opt-in to opening clear
	// files) and a Signer, or RepairAll returns an error before doing anything. It is
	// idempotent: once encrypted, an item is Signed. In a DryRun they are in Todo.
	//
	// It changes the stored bytes of documents that other readers (a tool, another service, an
	// older release) may expect in the clear, and nothing afterwards reads them without the
	// keys. In particular a host that reads passkey/, auth/, public/ or collab: records raw from
	// the same backend would break: those are encrypted too (they are not in DefaultSkip), and
	// Skip is the control that keeps an id out of it. Anything that does not start with the age header counts as clear, a damaged or
	// foreign non-age file too. Run it with DryRun first and read Todo.
	EncryptPlaintext bool
	// FramesOf, when set, makes the report list (Frames) the v3 frames signed by this key as the
	// scan found them, before the run wrote anything to that document. RevokeSigner uses it to know
	// what the key had written when the removal began.
	FramesOf ed25519.PublicKey
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
// Cost: the store is listed once, then each document is scanned by reading the head of every
// one of its files (docstore.HeadReader: a ranged or substring read on localfs, sqlite, postgres,
// s3 and blob, and on the memory store; at most repairHeadBytes, 128 KiB, per file, because the
// age layer must be opened to see the frame inside it). The saving is only for files larger than
// 128 KiB: a smaller file is read whole either way. A store whose adapter has no head read
// (MongoDB, or one wrapped in something that hides it) is read in full, as before, so a large
// store then costs a read of all of it per run, whether or not anything is repaired. Each
// repaired document is listed again, under the lock, by Repair and read in full there.
//
// It is idempotent (a second run finds every repaired document Signed and writes nothing) and
// restartable. A run that is cut short, by the context, Limit, a crash or a failed document, is
// finished by running it again, from the first id (the finished ones are found Signed, one scan
// each) or, to skip them, from the cursor: RepairAllReport.Next is the last group it went
// through, and RepairAllOptions.After starts after it, so Limit and After walk a huge store in
// slices. The cursor only moves forward: a group that sorts before it is not looked at again, so a
// file saved there after it passed is missed until a run from the start (After ""); Left covers
// only the groups after After. It is safe next to other writers for the reason Repair is. When the context ends it
// returns the report so far and the context's error; a store that cannot be listed returns that
// error. A skipped document is not an error: see Left.
//
// With RepairAllOptions.EncryptPlaintext it also encrypts what is stored in the clear (see
// there for the risk); otherwise such an item is skipped as SkipPlaintext.
func (s *Sharing) RepairAll(ctx context.Context, o RepairAllOptions) (RepairAllReport, error) {
	var rep RepairAllReport
	if s.Adapter == nil {
		return rep, errors.New("encrypt: RepairAll needs the Sharing's Adapter")
	}
	if o.EncryptPlaintext {
		if !s.Adapter.AllowPlaintext {
			return rep, errors.New("encrypt: RepairAllOptions.EncryptPlaintext needs Adapter.AllowPlaintext, the opt-in to touching files stored in the clear")
		}
		if s.Adapter.Signer == nil {
			return rep, fmt.Errorf("%w: EncryptPlaintext needs the Adapter's Signer", ErrNoFrameSigner)
		}
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
	if o.After != "" {
		// The groups after the cursor; a file id of a group is that group's cursor.
		after := groupOf(o.After)
		docs = docs[sort.Search(len(docs), func(i int) bool { return docs[i] > after }):]
	}
	rep.Total = len(docs)
	rep.Next = o.After
	signs := s.Adapter.Signer != nil
	same := signs && s.Signer != nil && bytes.Equal(s.Signer.Public(), s.Adapter.Signer.Public())
	if signs {
		rep.IndexBy = s.indexSigner(ctx)
		rep.IndexOther = rep.IndexBy != nil && !bytes.Equal(rep.IndexBy, s.Adapter.Signer.Public())
		if rep.IndexOther && o.Resign && !o.DryRun {
			if !same {
				rep.IndexErr = fmt.Errorf("%w: the Sharing's Signer is not the Adapter's", ErrNoFrameSigner)
			} else if rep.IndexErr = s.ResignIndex(ctx); rep.IndexErr == nil {
				// ResignIndex does not say whether it wrote (an index with no entries and seq 0
				// is left as it is), so look at who signed what is stored now.
				rep.IndexResigned = bytes.Equal(s.indexSigner(ctx), s.Adapter.Signer.Public())
			}
		}
	}
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
		sc := s.Adapter.scanFrames(ctx, groups[doc], o.EncryptPlaintext)
		recKey := s.recordSigner(ctx, doc)
		var other bool
		if sc.err == nil && ctx.Err() == nil {
			other = tally(&rep, sc, recKey, s.Adapter.Signer, signs)
		}
		if o.FramesOf != nil {
			for _, m := range sc.marks {
				if bytes.Equal(m.signer, o.FramesOf) {
					rep.Frames = append(rep.Frames, FrameMark{File: m.file, Hash: hex.EncodeToString(m.hash[:])})
				}
			}
		}
		resign := o.Resign && other
		switch {
		case ctx.Err() != nil:
			rep.Remaining = len(docs) - i
			return rep, ctx.Err()
		case sc.err != nil:
			outcome = s.skip(&rep, doc, sc.reason, sc.files, sc.err)
		case len(sc.unsigned) == 0 && len(sc.plain) == 0 && !resign:
			rep.Signed++
			if other {
				rep.OtherSigner = append(rep.OtherSigner, doc)
			}
		case !signs:
			outcome = s.skip(&rep, doc, SkipNoSigner, sc.unsigned, fmt.Errorf("%w: %s", ErrNoFrameSigner, doc))
		case resign && !same:
			// The record is signed by the Sharing's Signer, the files by the Adapter's: unless they
			// are one key the document would be counted Resigned on every run and never move.
			outcome = s.skip(&rep, doc, SkipNoSigner, nil, fmt.Errorf("%w: Resign needs the Sharing's Signer to be the Adapter's: %s", ErrNoFrameSigner, doc))
		case o.DryRun:
			rep.Todo = append(rep.Todo, doc)
			outcome = "todo"
			acted++
		default:
			var n int
			var err error
			if resign && recKey != nil && !bytes.Equal(recKey, s.Signer.Public()) {
				// The record first: files sealed after it are at its seq or below, which is accepted,
				// and the re-key that follows signs every file with the current key.
				err = s.Resign(ctx, doc)
			}
			if err == nil {
				n, err = s.repairGroup(ctx, doc, groups[doc])
			}
			switch {
			case err == nil:
				outcome = "repaired"
				switch {
				case len(sc.plain) > 0:
					rep.Encrypted++
					outcome = "encrypted"
				case len(sc.unsigned) == 0:
					rep.Resigned++
					outcome = "resigned"
				default:
					rep.Repaired++
				}
				rep.Files += n
				acted++
			case ctx.Err() != nil:
				rep.Remaining = len(docs) - i
				return rep, ctx.Err()
			case errors.Is(err, docstore.ErrNotFound) && s.allGone(ctx, groups[doc]):
				// Deleted since the scan: gone, as the scan itself ignores a deleted file. Nothing
				// to count. Only when the files are really missing now: an ErrNotFound that came
				// from anywhere else (a keyring) leaves them stored, and is a skip below.
				outcome = "gone"
			default:
				reason, files := classifyRepairErr(err)
				if errors.Is(err, docstore.ErrNotFound) {
					// Some of the files went, the others are stored: not a keyring problem.
					err = fmt.Errorf("some files of this item are gone, rerun to settle: %w", err)
				}
				outcome = s.skip(&rep, doc, reason, files, err)
			}
		}
		if o.Progress != nil {
			o.Progress(RepairProgress{Done: i + 1, Total: len(docs), Doc: doc, Outcome: outcome})
		}
		rep.Next = doc
	}
	rep.Next = ""
	rep.Finished = true
	sort.Slice(rep.BySigner, func(i, j int) bool { return rep.BySigner[i].Fingerprint < rep.BySigner[j].Fingerprint })
	return rep, nil
}

// recordSigner is the owner signing key that signed the document's keys:<ID> record as stored, or
// nil when there is no record, it does not verify, or the id is not a document's.
func (s *Sharing) recordSigner(ctx context.Context, doc string) ed25519.PublicKey {
	if !docfile.ValidID(doc) {
		return nil
	}
	d, err := s.Store.Load(ctx, KeysPrefix+doc)
	if err != nil {
		return nil
	}
	_, by, err := verifyRecordBy(d.Data, doc, s.verifiers())
	if err != nil {
		return nil
	}
	return by
}

// indexSigner is the owner signing key that signed the keys:index record as stored, or nil when
// there is none or it does not verify.
func (s *Sharing) indexSigner(ctx context.Context) ed25519.PublicKey {
	d, err := s.Store.Load(ctx, IndexKey)
	if err != nil {
		return nil
	}
	var sr signedRecord
	if json.Unmarshal(d.Data, &sr) != nil || len(sr.Sig) == 0 || len(sr.Payload) == 0 {
		return nil
	}
	for _, k := range s.verifiers() {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, indexMessage(sr.Payload), sr.Sig) {
			return k
		}
	}
	return nil
}

// tally adds one document's signers to the report and says whether any is not the current one
// (always false without a current signer).
func tally(rep *RepairAllReport, sc frameScan, rec ed25519.PublicKey, cur *Signer, signs bool) (other bool) {
	row := func(k ed25519.PublicKey) *SignerFrames {
		for i := range rep.BySigner {
			if bytes.Equal(rep.BySigner[i].Key, k) {
				return &rep.BySigner[i]
			}
		}
		rep.BySigner = append(rep.BySigner, SignerFrames{Key: k, Fingerprint: SignerFingerprint(k), Current: signs && bytes.Equal(k, cur.Public())})
		return &rep.BySigner[len(rep.BySigner)-1]
	}
	seen := map[string]bool{}
	for _, k := range sc.signers {
		r := row(k)
		r.Files++
		if !seen[string(k)] {
			seen[string(k)] = true
			r.Docs++
		}
		other = other || (signs && !r.Current)
	}
	if rec != nil {
		r := row(rec)
		r.Records++
		other = other || (signs && !r.Current)
	}
	return other
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

// allGone says whether none of ids is stored any more. Any other answer (a file is there, or
// the store fails) is false: the item is then reported, never called gone.
func (s *Sharing) allGone(ctx context.Context, ids []string) bool {
	for _, id := range ids {
		if _, _, err := docstore.LoadHead(ctx, s.Adapter.Inner, id, 1); !errors.Is(err, docstore.ErrNotFound) {
			return false
		}
	}
	return true
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

// frameMark is a v3 frame as the scan saw it: the file, its signer and the content hash it names.
type frameMark struct {
	file   string
	signer ed25519.PublicKey
	hash   [sha256.Size]byte
}

// FrameMark names a frame: the stored file and the content hash (hex) its header carries. A frame
// rewritten with other content has another mark; the same content signed again has the same.
type FrameMark struct {
	File string `json:"f"`
	Hash string `json:"h"`
}

// frameScan is what scanFrames found in one document's files.
type frameScan struct {
	unsigned []string            // v1, v2 frames
	plain    []string            // files stored in the clear (only with encryptPlain)
	signers  []ed25519.PublicKey // the key of each v3 frame, one per file
	marks    []frameMark         // the same frames with their file and content hash
	files    []string            // the files behind err
	reason   RepairSkipReason
	err      error // set when the document cannot be judged
}

// repairHeadBytes is how much of each file the scan asks the store for when it can read a head
// (docstore.HeadReader). A frame header is under 100 bytes, but it sits inside the age stream:
// the age header (about 100 bytes per recipient), the stream nonce and then the first payload
// chunk, which age authenticates whole (64 KiB and a 16 byte tag) before it releases a byte. So
// the least that can be read is a chunk and a header, and 128 KiB leaves room for the headers of
// some 450 recipients (an X25519 stanza is 130 to 150 bytes). A file whose head is cut inside what is needed (more recipients than
// that) is read in full instead; a file shorter than this is read in full anyway.
var repairHeadBytes = 128 << 10

// scanFrames reads the frame header of every one of ids, the files of a document or the one
// item of a group (listed by the caller, once for the whole run; no lock: a hint that Repair
// checks again under the lock), and sorts them into unsigned ones and the reason, if any, that
// the document cannot be repaired. It asks the store for the head of each file only
// (repairHeadBytes), and for the whole file when the head does not settle it: a head that was cut
// short (the file is longer) and does not open to a frame is judged again from the whole file,
// except for a file that is plainly in the clear or encrypted to keys not held (both are known
// from the age header at the start). A file that cannot be judged settles the document: it is
// not repaired even if other files are unsigned, as a re-key is all or none. With encryptPlain a
// file in the clear is listed in plain instead of settling the document.
func (a *Adapter) scanFrames(ctx context.Context, ids []string, encryptPlain bool) frameScan {
	var sc frameScan
	fail := func(id string, reason RepairSkipReason, err error) {
		if sc.err == nil || reason == SkipForged { // a forgery is the most important thing to say
			sc.reason, sc.err = reason, fmt.Errorf("%s: %w", id, err)
		}
		sc.files = append(sc.files, id)
	}
	for _, id := range ids {
		data, whole, err := docstore.LoadHead(ctx, a.Inner, id, repairHeadBytes)
		if errors.Is(err, docstore.ErrNotFound) {
			continue // deleted since it was listed
		}
		if err != nil {
			fail(id, SkipError, err)
			continue
		}
		fi, err := a.peekFrameWith(ctx, id, data, true)
		// A cut head settles a file only when it says so by the start of the age file: clear (the
		// head is longer than the age magic and is not it) or encrypted to keys not held (the age
		// header was read whole to find that out).
		settled := whole || errors.Is(err, ErrNoKey) || (errors.Is(err, ErrNotEncrypted) && len(data) >= len(header))
		if err != nil && !settled {
			// Cut short: the head may not hold the age header and the first chunk. Judge the file
			// from all of it.
			d, lerr := a.Inner.Load(ctx, id)
			if errors.Is(lerr, docstore.ErrNotFound) {
				continue
			}
			if lerr != nil {
				fail(id, SkipError, lerr)
				continue
			}
			fi, err = a.peekFrameWith(ctx, id, d.Data, true)
		}
		switch {
		case err == nil:
			if fi.version < 3 {
				sc.unsigned = append(sc.unsigned, id)
			} else if k := a.frameSigner(id, fi); k != nil {
				sc.signers = append(sc.signers, k)
				sc.marks = append(sc.marks, frameMark{file: id, signer: k, hash: fi.b.Hash})
			}
		case errors.Is(err, ErrNoKey):
			fail(id, SkipNoKey, err)
		case errors.Is(err, ErrNotEncrypted):
			if encryptPlain {
				sc.plain = append(sc.plain, id)
			} else {
				fail(id, SkipPlaintext, err)
			}
		case errors.Is(err, ErrForgedFrame):
			fail(id, SkipForged, err)
		default:
			fail(id, SkipUnreadable, fmt.Errorf("%w: %w", ErrUnreadable, err))
		}
	}
	return sc
}
