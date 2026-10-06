package encrypt

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// IndexKey is where the owner-signed index of end-to-end documents is stored. It is in the
// keys: keyspace on purpose (readable, never expiring, and not a document ID, so it cannot be
// the record of any document), and is signed like a record, under its own context.
const IndexKey = KeysPrefix + "index"

const indexContext = "gojiberry/keys-index/v1"

// IndexEntry is what the index says of one end-to-end document: its level and the seq of the
// keys:<ID> record when it was switched.
type IndexEntry struct {
	Level string `json:"level"`
	Seq   uint64 `json:"seq"`
}

// indexRecord is the signed payload of the index.
type indexRecord struct {
	V    int                   `json:"v"`
	Seq  uint64                `json:"seq"`
	Docs map[string]IndexEntry `json:"docs,omitempty"`
}

// Index is the owner's list of end-to-end documents, as read and verified.
type Index struct {
	// Seq grows with every change to the list.
	Seq uint64
	// Docs maps a document (or confidential folder) ID to its entry. Only end-to-end
	// documents are listed: switching one back to server removes it.
	Docs map[string]IndexEntry
}

func indexMessage(payload []byte) []byte {
	return append([]byte(indexContext+"\x00"+IndexKey+"\x00"), payload...)
}

// loadIndex reads and verifies the index. A missing one is empty, unless the floor has seen
// one (ErrStaleRecord); an older one than the floor's is ErrStaleRecord too; a good one raises
// the floor.
func loadIndex(ctx context.Context, store docstore.Adapter, owners []ed25519.PublicKey, floor SeqFloor) (rec indexRecord, digest string, err error) {
	d, err := store.Load(ctx, IndexKey)
	if errors.Is(err, docstore.ErrNotFound) {
		if floor != nil {
			f, ferr := floorGet(ctx, floor, IndexKey)
			if ferr != nil {
				return rec, "", ferr
			}
			if f > 0 {
				return indexRecord{}, "", fmt.Errorf("%s: %w", IndexKey, ErrStaleRecord)
			}
		}
		return indexRecord{V: 1}, "", nil
	}
	if err != nil {
		return rec, "", err
	}
	digest = docstore.Digest(d.Data)
	var sr signedRecord
	if err := json.Unmarshal(d.Data, &sr); err != nil {
		return rec, digest, fmt.Errorf("encrypt: the index of confidential documents is damaged: %w", err)
	}
	if len(sr.Sig) == 0 || len(sr.Payload) == 0 {
		return rec, digest, fmt.Errorf("%s: %w", IndexKey, ErrUnsignedRecord)
	}
	ok := false
	for _, k := range owners {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, indexMessage(sr.Payload), sr.Sig) {
			ok = true
			break
		}
	}
	if !ok {
		return rec, digest, fmt.Errorf("%s: %w", IndexKey, ErrBadSignature)
	}
	if err := json.Unmarshal(sr.Payload, &rec); err != nil || rec.V != 1 {
		return indexRecord{}, digest, fmt.Errorf("%s: %w", IndexKey, ErrBadSignature)
	}
	if floor != nil {
		f, ferr := floorGet(ctx, floor, IndexKey)
		if ferr != nil {
			return indexRecord{}, digest, ferr
		}
		if rec.Seq < f {
			return indexRecord{}, digest, fmt.Errorf("%s: %w", IndexKey, ErrStaleRecord)
		}
		if ferr := floorRaise(ctx, floor, IndexKey, rec.Seq); ferr != nil {
			return indexRecord{}, digest, ferr
		}
	}
	return rec, digest, nil
}

// Index returns the owner-signed list of end-to-end documents (empty when none was ever
// written). A list that does not verify, or is older than the floor's, is an error.
func (s *Sharing) Index(ctx context.Context) (Index, error) {
	rec, _, err := loadIndex(ctx, s.Store, s.verifiers(), s.SeqFloor)
	if err != nil {
		return Index{}, err
	}
	docs := make(map[string]IndexEntry, len(rec.Docs))
	for k, v := range rec.Docs {
		docs[k] = v
	}
	return Index{Seq: rec.Seq, Docs: docs}, nil
}

// setIndex makes the index say a document is at level with the record seq: listed for the
// end-to-end level (an entry already at that seq or a higher one is left), removed for the
// server level. It is idempotent, so a SetProtection that failed after the record was written
// repeats it.
func (s *Sharing) setIndex(ctx context.Context, docID, level string, seq uint64) error {
	if s.Signer == nil {
		return ErrNoSigner
	}
	if _, cas := s.Store.(docstore.ConditionalAdapter); !cas {
		// No compare-and-swap: two clients changing the index at the same moment would each
		// write their own version of it and lose the other's entry. The store's Lock, where it
		// has one, serializes them; without one, serialize index changes yourself (one owner
		// client at a time) and let VerifyIndex's findings tell you of a document left unlisted.
		if l, ok := s.Store.(docstore.Locker); ok {
			unlock, err := l.Lock(ctx)
			if err != nil {
				return err
			}
			defer unlock()
		}
	}
	for range mutateAttempts {
		rec, digest, err := loadIndex(ctx, s.Store, s.verifiers(), s.SeqFloor)
		if err != nil {
			return err
		}
		cur, has := rec.Docs[docID]
		switch {
		case level == ProtectionE2EE && has && cur.Level == ProtectionE2EE && cur.Seq >= seq:
			return nil
		case level != ProtectionE2EE && !has:
			return nil
		}
		if rec.Docs == nil {
			rec.Docs = map[string]IndexEntry{}
		}
		if level == ProtectionE2EE {
			rec.Docs[docID] = IndexEntry{Level: ProtectionE2EE, Seq: seq}
		} else {
			delete(rec.Docs, docID)
		}
		rec.V = 1
		rec.Seq++
		payload, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		data, err := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(s.Signer.key, indexMessage(payload)), Signer: s.Signer.Public()})
		if err != nil {
			return err
		}
		if c, ok := s.Store.(docstore.ConditionalAdapter); ok {
			err = c.SaveIf(ctx, IndexKey, data, digest)
			if errors.Is(err, docstore.ErrConflict) {
				continue
			}
		} else {
			err = s.Store.Save(ctx, IndexKey, data)
		}
		if err == nil && s.SeqFloor != nil {
			err = floorRaise(ctx, s.SeqFloor, IndexKey, rec.Seq)
		}
		return err
	}
	return docstore.ErrConflict
}

// IndexFinding is a document the index lists whose record is not what it should be. Err wraps
// ErrDowngrade and says whether the record is missing or older than the index says.
type IndexFinding struct {
	Doc   string
	Entry IndexEntry
	Err   error
}

// ErrIndexUnverifiable is the Err of the finding VerifyIndex returns when the index lists no
// documents and no floor has ever seen one (no SeqFloor, or an empty one): an index that was
// deleted cannot be told from one that was never written, so "no end-to-end documents" is only
// the store's word. It is a warning, not an ErrDowngrade: set a SeqFloor on the Sharing and the
// StoreKeyring (the first VerifyIndex after an index exists raises it) to turn it into a check.
var ErrIndexUnverifiable = errors.New("encrypt: the index of end-to-end documents is empty or missing and no floor has seen one: a deleted index cannot be told from none")

// VerifyIndex compares the owner-signed index with the stored records, for every document it
// lists, whether or not this client ever opened it: a listed document whose keys:<ID> record
// is missing, does not verify, is older than the entry's seq, or disagrees with it at the same
// seq is a finding (ErrDowngrade). Nothing is changed. A client calls it once after sign-in.
//
// When the Sharing has a SeqFloor it is raised, as a side effect, to the index's own seq and
// to every entry's seq (or the record's, if higher), in one batch (BatchSeqFloor: one write for
// a FileSeqFloor), so from then on the ordinary reads refuse a deleted or older record of any
// listed document (ErrStaleRecord) even on a client that never saw that document: the index is
// how a fresh client gets a floor for the documents that matter.
//
// The index is only as good as its own freshness: a store writer can delete it or put back an
// older one (see ErrStaleRecord, and the floor). When it lists nothing and the floor has never
// seen an index (or there is no floor), a finding with ErrIndexUnverifiable (Doc is IndexKey)
// says so: that is the one state in which a deleted index looks like an honest empty one. What
// it adds over the frames is a check that needs no file to be opened, and covers files still
// in the v1 frame. Without a floor, a client holds no state at all and an index rolled back
// to an older one that lists fewer documents is not noticed either.
func (s *Sharing) VerifyIndex(ctx context.Context) ([]IndexFinding, error) {
	idx, _, err := loadIndex(ctx, s.Store, s.verifiers(), s.SeqFloor)
	if err != nil {
		return nil, err
	}
	docs := make([]string, 0, len(idx.Docs))
	for d := range idx.Docs {
		docs = append(docs, d)
	}
	sort.Strings(docs)
	var out []IndexFinding
	if len(docs) == 0 {
		var seen uint64
		if s.SeqFloor != nil {
			if seen, err = floorGet(ctx, s.SeqFloor, IndexKey); err != nil {
				return nil, err
			}
		}
		if seen == 0 {
			out = append(out, IndexFinding{Doc: IndexKey, Err: ErrIndexUnverifiable})
		}
	}
	raises := map[string]uint64{}
	for _, doc := range docs {
		e := idx.Docs[doc]
		raise := e.Seq
		d, err := s.Store.Load(ctx, KeysPrefix+doc)
		var ferr error
		switch {
		case errors.Is(err, docstore.ErrNotFound):
			ferr = downgrade(doc, "the index lists it as %s (record %d) and its record is missing: it was deleted"+recoverHint, levelName(e.Level), e.Seq)
		case err != nil:
			return out, err
		default:
			rec, verr := verifyRecord(d.Data, doc, s.verifiers())
			switch {
			case verr != nil:
				ferr = fmt.Errorf("%w: %s: the index lists it and its record does not verify: %w", ErrDowngrade, doc, verr)
			case rec.Seq < e.Seq:
				ferr = downgrade(doc, "the index lists it at record %d and the record is %d: an older signed record was put back"+recoverHint, e.Seq, rec.Seq)
			case rec.Seq == e.Seq && levelOf(rec) != e.Level:
				ferr = downgrade(doc, "the index lists it as %s and the record at the same seq says %s"+recoverHint, levelName(e.Level), levelName(levelOf(rec)))
			default:
				if rec.Seq > raise {
					raise = rec.Seq
				}
			}
		}
		if ferr != nil {
			out = append(out, IndexFinding{Doc: doc, Entry: e, Err: ferr})
		}
		raises[doc] = raise
	}
	if s.SeqFloor != nil {
		if err := floorRaiseMany(ctx, s.SeqFloor, raises); err != nil {
			return out, err
		}
	}
	return out, nil
}

func levelOf(r sharedRecord) string {
	if r.Protection == ProtectionE2EE {
		return ProtectionE2EE
	}
	return ""
}

// highestSeq is the highest record seq any stored file of the document was sealed under (every
// file, snapshots included, that can be opened here), and the floor's value: what a record the
// owner writes in Recover has to exceed.
func (s *Sharing) highestSeq(ctx context.Context, docID string) (uint64, error) {
	var top uint64
	if s.SeqFloor != nil {
		f, err := floorGet(ctx, s.SeqFloor, docID)
		if err != nil {
			return 0, err
		}
		top = f
	}
	if s.Adapter == nil {
		return top, nil
	}
	ids, err := s.Adapter.filesOf(ctx, docID)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		d, err := s.Adapter.Inner.Load(ctx, id)
		if err != nil {
			continue
		}
		fi, err := s.Adapter.peekFrame(ctx, id, d.Data)
		if err == nil && fi.version >= 2 && fi.b.Seq > top {
			top = fi.b.Seq
		}
	}
	return top, nil
}

// Recover is the owner's way forward after ErrDowngrade when the record was deleted or an older
// one put back and the owner knows what it should say: it writes a new signed record for the
// document with exactly the given protection level and shared keys (the owner's own keys are
// always recipients and are not listed), numbered above every record any stored file was sealed
// under and above the floor, marked pending, then re-keys every file of the document to it and
// clears pending, as SetProtection does. The replayed or missing record is not consulted, so
// nothing it listed survives unless it is in shared: a person the owner had removed and the
// replay brought back is not carried over. It needs the Signer. The record keeps its Signed
// claim (a Recover does not make unsigned files acceptable): an unsigned file left in the document
// stops the re-key, naming it, unless the owner confirms it with AcceptUnsigned (see there). Check what was lost with
// Sharing.List before and after; what a person already opened cannot be taken back.
func (s *Sharing) Recover(ctx context.Context, docID, protection string, shared []Recipient, opts ...RepairOption) (int, error) {
	want, err := normalizeProtection(protection)
	if err != nil {
		return 0, err
	}
	if !docfile.ValidID(docID) {
		return 0, errors.New("encrypt: not a document ID")
	}
	if s.Signer == nil {
		return 0, ErrNoSigner
	}
	entries := make([]sharedEntry, 0, len(shared))
	for _, r := range shared {
		if r.Text == "" {
			return 0, errors.New("encrypt: the recipient has no key text")
		}
		p, err := parseShared(sharedEntry{Type: r.Type, Key: r.Text, Label: r.Label, Hint: r.Hint})
		if err != nil {
			return 0, err
		}
		label := r.Label
		if label == "" {
			label = p.Label
		}
		entries = append(entries, sharedEntry{Label: label, Type: p.Type, Fingerprint: p.Fingerprint, Key: p.Text, Hint: p.Hint, Added: time.Now().Unix()})
	}
	minSeq, err := s.highestSeq(ctx, docID)
	if err != nil {
		return 0, err
	}
	rec, err := s.mutateWith(ctx, docID, mutateOpts{recover: true, minSeq: minSeq}, func(rec *sharedRecord) (bool, error) {
		rec.Protection, rec.Pending, rec.Recipients = want, true, entries
		return true, nil
	})
	if err != nil {
		return 0, err
	}
	if err := s.setIndex(ctx, docID, want, rec.Seq); err != nil {
		return 0, err
	}
	n, stale, unreadable, err := s.finishFiles(ctx, docID, repairOptions(opts))
	if err != nil && len(stale) > 0 {
		err = &RekeyError{Doc: docID, Stale: stale, Unreadable: unreadable, Err: err}
	}
	return n, err
}
