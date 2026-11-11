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

// IndexKey is where the owner-signed index is stored.
const IndexKey = KeysPrefix + "index"

const indexContext = "gojiberry/keys-index/v1"

// IndexEntry is what the index says of one end-to-end document.
type IndexEntry struct {
	Level string `json:"level"`
	Seq   uint64 `json:"seq"`
}

type indexRecord struct {
	V    int                   `json:"v"`
	Seq  uint64                `json:"seq"`
	Docs map[string]IndexEntry `json:"docs,omitempty"`
}

// Index is the owner's list of end-to-end documents.
type Index struct {
	// Seq grows with every change to the list.
	Seq uint64
	// Docs maps a document ID to its protection level.
	Docs map[string]IndexEntry
}

func indexMessage(payload []byte) []byte {
	return append([]byte(indexContext+"\x00"+IndexKey+"\x00"), payload...)
}

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

// Index returns the owner-signed list of end-to-end documents.
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

func (s *Sharing) setIndex(ctx context.Context, docID, level string, seq uint64) error {
	return s.changeIndex(ctx, func(rec *indexRecord) bool {
		cur, has := rec.Docs[docID]
		switch {
		case level == ProtectionE2EE && has && cur.Level == ProtectionE2EE && cur.Seq >= seq:
			return false
		case level != ProtectionE2EE && !has:
			return false
		}
		if rec.Docs == nil {
			rec.Docs = map[string]IndexEntry{}
		}
		if level == ProtectionE2EE {
			rec.Docs[docID] = IndexEntry{Level: ProtectionE2EE, Seq: seq}
		} else {
			delete(rec.Docs, docID)
		}
		return true
	})
}

// ResignIndex signs the owner-signed index again with the signer.
func (s *Sharing) ResignIndex(ctx context.Context) error {
	return s.changeIndex(ctx, func(rec *indexRecord) bool { return rec.Seq > 0 || len(rec.Docs) > 0 })
}

func (s *Sharing) changeIndex(ctx context.Context, fn func(rec *indexRecord) bool) error {
	if s.Signer == nil {
		return ErrNoSigner
	}
	if _, cas := s.Store.(docstore.ConditionalAdapter); !cas {
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
		if !fn(&rec) {
			return nil
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

// IndexFinding is a problem found in the index.
type IndexFinding struct {
	Doc   string
	Entry IndexEntry
	Err   error
}

// ErrIndexUnverifiable is the Err of an index finding.
var ErrIndexUnverifiable = errors.New("encrypt: the index of end-to-end documents is empty or missing and no floor has seen one: a deleted index cannot be told from none")

// VerifyIndex compares the owner-signed index with the store.
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

// Recover is the owner's way forward after ErrDowngrade.
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
