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

// RepairSkipReason says why RepairAll skipped a document.
type RepairSkipReason string

const (
	// SkipNoSigner: unsigned files and no signer are available.
	SkipNoSigner RepairSkipReason = "no-signer"
	// SkipNoKey: a file is encrypted to keys the keyring lacks.
	SkipNoKey RepairSkipReason = "no-key"
	// SkipUnreadable: a file cannot be opened.
	SkipUnreadable RepairSkipReason = "unreadable"
	// SkipForged: a file is refused as forged.
	SkipForged RepairSkipReason = "forged"
	// SkipDowngrade: the document's files and its record disagree.
	SkipDowngrade RepairSkipReason = "downgrade"
	// SkipPlaintext: a file is stored in the clear.
	SkipPlaintext RepairSkipReason = "plaintext"
	// SkipConflict: a writer kept changing the document during the run.
	SkipConflict RepairSkipReason = "conflict"
	// SkipError: any other failure; Err says what.
	SkipError RepairSkipReason = "error"
)

// RepairSkip is a document RepairAll skipped, and why.
type RepairSkip struct {
	// Doc is the document's id, or the item's id.
	Doc    string
	Reason RepairSkipReason
	// Files are the files that caused the skip.
	Files []string
	Err   error
}

// SignerFingerprint names an owner signing key in a report.
func SignerFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SignerFrames is what RepairAll found signed by one owner key.
type SignerFrames struct {
	Key         ed25519.PublicKey
	Fingerprint string // SignerFingerprint(Key)
	// Current is set for the Adapter's own Signer.
	Current bool
	// Files is how many v3 frames the key signed.
	Files, Docs, Records int
}

// RepairAllReport is what RepairAll did.
type RepairAllReport struct {
	// Total is how many units this run covers.
	Total int
	// Signed had every file in a signed v3 frame already.
	Signed int
	// Repaired had unsigned files and was sealed again, signed.
	Repaired int
	// Encrypted had plaintext files encrypted, with EncryptPlaintext.
	Encrypted int
	// Resigned had every file re-signed, with Resign.
	Resigned int
	// Files is how many files were written.
	Files int
	// BySigner counts documents per owner signing key.
	BySigner []SignerFrames
	// Frames are the frames the report lists.
	Frames []FrameMark
	// OtherSigner lists documents signed by other owner keys.
	OtherSigner []string
	// IndexBy is the owner key that signed the keys index.
	IndexBy                   ed25519.PublicKey
	IndexOther, IndexResigned bool
	IndexErr                  error
	// Skipped lists documents not repaired, with reasons.
	Skipped []RepairSkip
	// Todo lists documents a real run would repair.
	Todo []string
	// Remaining were not looked at because the context ended.
	Remaining int
	// Next is the cursor for After to continue the run.
	Next string
	// Finished says the run went through every group after After.
	Finished bool
}

// Left is how many documents still have unsigned frames.
func (r RepairAllReport) Left() int { return len(r.Skipped) + len(r.Todo) + r.Remaining }

// RepairProgress is passed to Progress after each document.
type RepairProgress struct {
	Done, Total int
	Doc         string
	// Outcome is signed, repaired, resigned, todo, or skipped.
	Outcome string
}

// RepairAllOptions bound and observe RepairAll.
type RepairAllOptions struct {
	// Progress, when set, is called after each document.
	Progress func(RepairProgress)
	// Limit, when positive, stops after that many documents.
	Limit int
	// After, when set, starts the run after that group.
	After string
	// DryRun reports what would be repaired, writing nothing.
	DryRun bool
	// Resign also seals again, with the Adapter's Signer.
	Resign bool
	// EncryptPlaintext also encrypts items stored in the clear.
	EncryptPlaintext bool
	// FramesOf, when set, lets the report list frames.
	FramesOf ed25519.PublicKey
}

// RepairAll moves a whole store off unsigned v1/v2 frames.
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
				// ResignIndex does not say whether it wrote: check who signed it
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
			outcome = s.skip(&rep, doc, SkipNoSigner, nil, fmt.Errorf("%w: Resign needs the Sharing's Signer to be the Adapter's: %s", ErrNoFrameSigner, doc))
		case o.DryRun:
			rep.Todo = append(rep.Todo, doc)
			outcome = "todo"
			acted++
		default:
			var n int
			var err error
			if resign && recKey != nil && !bytes.Equal(recKey, s.Signer.Public()) {
				// record first: later files sit at its seq or below
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
				outcome = "gone"
			default:
				reason, files := classifyRepairErr(err)
				if errors.Is(err, docstore.ErrNotFound) {
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

// any other answer, a store failure included, is false
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

type frameMark struct {
	file   string
	signer ed25519.PublicKey
	hash   [sha256.Size]byte
}

// FrameMark names a frame: its stored file and content hash.
type FrameMark struct {
	File string `json:"f"`
	Hash string `json:"h"`
}

type frameScan struct {
	unsigned []string            // v1, v2 frames
	plain    []string            // files stored in the clear (only with encryptPlain)
	signers  []ed25519.PublicKey // the key of each v3 frame, one per file
	marks    []frameMark         // the same frames with their file and content hash
	files    []string            // the files behind err
	reason   RepairSkipReason
	err      error // set when the document cannot be judged
}

// the frame header sits inside the age stream: read enough
var repairHeadBytes = 128 << 10

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
		settled := whole || errors.Is(err, ErrNoKey) || (errors.Is(err, ErrNotEncrypted) && len(data) >= len(header))
		if err != nil && !settled {
			// cut short: head may lack the age header; judge the whole file
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
