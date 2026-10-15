package directoryclient

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// ErrSplitView is returned when the log this client is shown differs from the one the witnesses
// cosigned: it is shorter than the cosigned entry, or has another entry at its number. The server
// is showing different people different logs (or rewrote one).
var ErrSplitView = errors.New("directoryclient: the log differs from the one the witnesses cosigned")

// ErrNotWitnessed is encrypt.ErrNotWitnessed: fewer than Client.WitnessQuorum of the pinned
// witnesses cosigned any entry.
var ErrNotWitnessed = encrypt.ErrNotWitnessed

// Witnessed fetches the entries witnesses cosigned (GET /directory/log/cosigned) and returns the
// newest one that at least Client.WitnessQuorum of the witnesses pinned in Client.Witnesses signed,
// each signature checked. With no quorum set it returns ErrNotWitnessed.
func (c *Client) Witnessed(ctx context.Context) (docstore.DirectoryCosigned, error) {
	if c.WitnessQuorum < 1 || len(c.Witnesses) < c.WitnessQuorum {
		return docstore.DirectoryCosigned{}, fmt.Errorf("%w: no quorum is pinned (Client.Witnesses, Client.WitnessQuorum)", ErrNotWitnessed)
	}
	var all []docstore.DirectoryCosigned
	if err := c.do(ctx, http.MethodGet, "/directory/log/cosigned", nil, &all); err != nil {
		return docstore.DirectoryCosigned{}, err
	}
	var last error
	for _, g := range all { // newest first
		if c.LogKeys != nil {
			if _, ok := c.LogKeys[g.Kid]; !ok {
				continue // cosigned under a log key that is not the pinned one
			}
		}
		err := encrypt.VerifyCosigned(g, c.Witnesses, c.WitnessQuorum)
		if err == nil {
			return g, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("%w: nothing is cosigned", ErrNotWitnessed)
	}
	return docstore.DirectoryCosigned{}, last
}

// checkWitnessed compares the log with the newest entry a quorum of witnesses cosigned: the log
// must reach it and have the cosigned hash there. entries are what this run read (it looks there
// first, then asks for the one entry); checked is false when the entry cannot be seen (a log
// filtered to the caller's own address shows only some entries), in which case only the head is
// compared when the cosigned entry is the head itself.
func (c *Client) checkWitnessed(ctx context.Context, head docstore.DirectoryHead, entries []docstore.DirectoryLogEntry, keys map[string]ed25519.PublicKey) (w docstore.DirectoryCosigned, checked bool, err error) {
	w, err = c.Witnessed(ctx)
	if err != nil {
		return w, false, err
	}
	switch {
	case w.Seq > head.Seq:
		return w, false, fmt.Errorf("%w: witnesses cosigned entry %d, the log's head is entry %d", ErrSplitView, w.Seq, head.Seq)
	case w.Seq == head.Seq:
		if w.Hash != head.Hash {
			return w, false, fmt.Errorf("%w: entry %d has another hash", ErrSplitView, w.Seq)
		}
		return w, true, nil
	}
	var at *docstore.DirectoryLogEntry
	for i := range entries {
		if entries[i].Seq == w.Seq {
			at = &entries[i]
		}
	}
	if at == nil {
		e, ok, err := c.entryAt(ctx, w.Seq, keys)
		if err != nil {
			return w, false, err
		}
		if !ok {
			return w, false, nil
		}
		at = &e
	}
	if at.Hash != w.Hash {
		return w, false, fmt.Errorf("%w: entry %d has another hash", ErrSplitView, w.Seq)
	}
	return w, true, nil
}

// WitnessState is what a witness remembers between runs: the newest entry it cosigned.
type WitnessState struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

// Witness is one run of a witness: it reads the log with the client (which needs the whole log:
// the operator's, or an enrolled owner's, view), checks it, and cosigns the head. It
//
//  1. fetches the signed head and checks its signature against Client.LogKeys (pinned: a witness
//     must not take the server's word for its key) and against st (a lower number, or the same
//     number with another hash, is ErrLogCut);
//  2. reads the log from the entry it cosigned last, checks that entry is still there with the
//     same hash, the chain and every signature, and that the log reaches the head;
//  3. signs the head's entry with sign (witness is the name the directory knows the key by) and
//     posts it (POST /directory/log/witness).
//
// st is updated only when the directory took the cosignature. A log that is shown filtered
// (not whole) is refused: a witness that cannot see the chain vouches for nothing.
func (c *Client) Witness(ctx context.Context, witness string, sign func(msg []byte) []byte, st *WitnessState) (docstore.DirectoryCosignature, error) {
	var zero docstore.DirectoryCosignature
	if c.LogKeys == nil {
		return zero, errors.New("directoryclient: a witness needs the log's pinned keys (Client.LogKeys)")
	}
	head, err := c.Head(ctx)
	if err != nil {
		return zero, err
	}
	if err := encrypt.CheckHead(st.Seq, st.Hash, head); err != nil {
		return zero, err
	}
	from := int64(0)
	if st.Seq > 0 {
		from = st.Seq - 1
	}
	read, err := c.readLog(ctx, from, c.LogKeys)
	if err != nil {
		return zero, err
	}
	if read.filtered {
		return zero, errors.New("directoryclient: the server shows only part of the log; a witness needs the whole of it")
	}
	entries := read.entries
	if st.Seq > 0 {
		if err := encrypt.VerifyLogHead(st.Seq, st.Hash, entries); err != nil {
			return zero, err
		}
	}
	if len(entries) > 0 {
		if st.Seq == 0 && (entries[0].Seq != 1 || entries[0].Prev != zeroPrev) {
			return zero, fmt.Errorf("%w: the log does not start at its beginning", ErrLogCut)
		}
		if _, err := encrypt.VerifyLog(entries, entries[0].Prev, entries[0].Seq-1, c.LogKeys); err != nil {
			return zero, err
		}
	}
	var at *docstore.DirectoryLogEntry
	for i := range entries {
		if entries[i].Seq == head.Seq {
			at = &entries[i]
		}
	}
	if head.Seq > 0 && (at == nil || at.Hash != head.Hash) {
		return zero, fmt.Errorf("%w: entry %d is not the one the signed head names", ErrLogCut, head.Seq)
	}
	if head.Seq == 0 {
		return zero, errors.New("directoryclient: the log is empty; nothing to cosign")
	}
	cs := encrypt.CosignEntry(sign, witness, head.Kid, head.Seq, head.Hash)
	if err := c.do(ctx, http.MethodPost, "/directory/log/witness", cs, nil); err != nil {
		return zero, err
	}
	st.Seq, st.Hash = head.Seq, head.Hash
	return cs, nil
}
