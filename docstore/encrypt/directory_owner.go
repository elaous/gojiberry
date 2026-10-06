package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// Owner-held identity for an address.
//
// The directory key lets the server publish a key for any address, so a Verified mark ends at the
// server. An owner who enrolls closes that: the owner holds an Ed25519 signing key (made and kept
// client-side, GenerateOwnerKey), registers its public half for the address (Enroll: needs the
// signed-in provider session AND a signature by the new key over a server challenge), and from
// then on every publish and revoke for that address needs the owner's signature over
// "directory-op|<op>|<address>|<fingerprint>|<time>|<prev log head>" in addition to the session.
// The server checks it and records it in the log entry (and in the record), so anyone who knows
// the address can check, from the log alone, that each entry for it carries a valid owner
// signature (AuditAddress): a server alone cannot fabricate entries, it can only withhold them or
// show an older state. Lookup marks such a key OwnerSigned.
//
// What stays with the server (and is stated plainly in docs/encryption-and-passkeys.md, 4b):
// withholding (an address that looks empty, a key that is not shown) and showing an older state;
// and trust on first use, because the first enrollment for an address is accepted on the provider
// session alone: whoever is signed in as the address first (the provider, or the server with a
// provider session of its own) can enroll first. The log shows the enrollment to everybody who
// watches the address, and a replacement by reset is delayed by ResetWait (24 hours) and visible
// in the log before it takes effect, which is the owner's window to cancel it with their own key.

// DefaultResetWait is how long a reset of the owner key (a provider-session replacement of a
// lost key) waits between its request and its taking effect.
const DefaultResetWait = 24 * time.Hour

// ownerSkew is how far an owner signature's time may be from the server's clock.
const ownerSkew = 10 * time.Minute

// auditSkew is the tolerance an auditor allows between a log entry's time and the time the owner
// signed: the server's skew check plus the time to append.
const auditSkew = ownerSkew + time.Minute

// OwnerOpMessage is what the owner key signs: "directory-op|<op>|<address>|<fingerprint>|<unix
// seconds>|<prev>", the address lower-cased and trimmed. op is "publish", "revoke", "enroll",
// "rotate", "accept" (the new key's acceptance of a rotation or reset), or "reset-cancel";
// fingerprint is the key's (for enroll, rotate and accept the new owner key's, OwnerFingerprint;
// empty for reset-cancel); prev is the log position the signer last saw (FormatLogPosition of
// the head).
func OwnerOpMessage(op, email, fingerprint string, at int64, prev string) []byte {
	return []byte("directory-op|" + op + "|" + normalizeEmail(email) + "|" + fingerprint + "|" + strconv.FormatInt(at, 10) + "|" + prev)
}

// OwnerFingerprint names an owner public key: "OWNER:" and the base64 SHA-256 of its 32 bytes.
func OwnerFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "OWNER:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// GenerateOwnerKey makes an owner signing key. It is held by the owner, client-side (a browser
// stores it, a CLI keeps a file): the server never sees the private half.
func GenerateOwnerKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// FormatOwnerPub is the form an owner public key travels in: base64 of its 32 bytes.
func FormatOwnerPub(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// ParseOwnerPub reads an owner public key.
func ParseOwnerPub(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("encrypt: not an owner public key")
	}
	return ed25519.PublicKey(b), nil
}

// FormatLogPosition is "<seq>:<hash>": the log position an owner signature names.
func FormatLogPosition(seq int64, hash string) string { return strconv.FormatInt(seq, 10) + ":" + hash }

// ParseLogPosition reads FormatLogPosition's output.
func ParseLogPosition(s string) (seq int64, hash string, err error) {
	n, h, ok := strings.Cut(s, ":")
	if !ok {
		return 0, "", errors.New("encrypt: not a log position")
	}
	seq, err = strconv.ParseInt(n, 10, 64)
	if err != nil || seq < 0 || len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
		return 0, "", errors.New("encrypt: not a log position")
	}
	return seq, h, nil
}

// SignOwnerOp signs an operation with the owner key at time at, naming the log position prev.
func SignOwnerOp(priv ed25519.PrivateKey, op, email, fingerprint string, at time.Time, prev string) docstore.DirectoryOwnerSig {
	return docstore.DirectoryOwnerSig{Time: at.Unix(), Prev: prev,
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, OwnerOpMessage(op, email, fingerprint, at.Unix(), prev)))}
}

func verifyOwner(pub ed25519.PublicKey, op, email, fingerprint string, at int64, prev, sig string) bool {
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(b) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, OwnerOpMessage(op, email, fingerprint, at, prev), b)
}

// VerifyOwnerEvidence checks, without the server's word, that a lookup row's owner signature is
// valid for the address: the evidence's key signed the publication of the row's fingerprint.
// Whether that key is the address's owner key is the client's to decide (pinned from an earlier
// lookup, or from the enrollment entry in the log: AuditAddress).
func VerifyOwnerEvidence(email string, k docstore.DirectoryKey) bool {
	if k.Owner == nil {
		return false
	}
	pub, err := ParseOwnerPub(k.Owner.Pub)
	if err != nil {
		return false
	}
	return verifyOwner(pub, OpPublish, email, k.Fingerprint, k.Owner.Time, k.Owner.Prev, k.Owner.Sig)
}

// enrollRecord is the stored enrollment of an address: the chain of owner keys and a pending
// reset, signed by the server key (so a record that was not written by the directory is
// ignored). The log is the proof; this is the state.
type enrollRecord struct {
	V          int                          `json:"v"`
	Kid        string                       `json:"kid"`
	Email      string                       `json:"email"`
	Keys       []docstore.DirectoryOwnerKey `json:"keys"`
	ResetSince int64                        `json:"reset_since,omitempty"`
	Seq        int64                        `json:"seq"`
	Sig        string                       `json:"sig"`
}

func (r enrollRecord) message(id string) []byte {
	b, _ := json.Marshal([]any{"gojiberry-directory-enroll-v1", id, r.Kid, r.Email, r.Keys, r.ResetSince, r.Seq})
	return b
}

func (r enrollRecord) current() (docstore.DirectoryOwnerKey, bool) {
	if len(r.Keys) == 0 {
		return docstore.DirectoryOwnerKey{}, false
	}
	k := r.Keys[len(r.Keys)-1]
	return k, k.Until == 0
}

func (d *AdapterDirectory) resetWait() time.Duration {
	if d.ResetWait > 0 {
		return d.ResetWait
	}
	return DefaultResetWait
}

func (d *AdapterDirectory) enrollNames(email string) []string {
	hs := d.Signer.Pseudonyms("enroll:" + normalizeEmail(email))
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = docstore.DirectoryEnrollPrefix + h
	}
	return out
}

var errEnrollmentCorrupt = errors.New("encrypt: the owner enrollment of this address cannot be read or verified")

// readEnrollment reads the enrollment of an address: nil, "" when there is none. A record that
// exists but is unreadable or whose signature does not verify is an error (the address must not
// look un-enrolled because someone damaged the record): publishing and revoking for it are
// refused until it is repaired.
func (d *AdapterDirectory) readEnrollment(ctx context.Context, email string) (*enrollRecord, string, error) {
	want := normalizeEmail(email)
	for _, id := range d.enrollNames(want) {
		doc, err := d.Store.Load(ctx, id)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		var rec enrollRecord
		if json.Unmarshal(doc.Data, &rec) != nil {
			return nil, "", errEnrollmentCorrupt
		}
		sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
		if rec.V != 1 || normalizeEmail(rec.Email) != want || !d.Signer.Verify(rec.Kid, rec.message(id), sig) {
			return nil, "", errEnrollmentCorrupt
		}
		return &rec, docstore.Digest(doc.Data), nil
	}
	return nil, "", nil
}

func (d *AdapterDirectory) enrollmentView(rec *enrollRecord) docstore.DirectoryEnrollment {
	if rec == nil {
		return docstore.DirectoryEnrollment{}
	}
	out := docstore.DirectoryEnrollment{Enrolled: true, Keys: append([]docstore.DirectoryOwnerKey(nil), rec.Keys...), ResetSince: rec.ResetSince, Seq: rec.Seq}
	if rec.ResetSince > 0 {
		out.ResetDue = rec.ResetSince + int64(d.resetWait()/time.Second)
	}
	return out
}

// Enrollment is the owner enrollment of an address: whether it has an owner key, the chain of
// keys (the last is current) and a pending reset.
func (d *AdapterDirectory) Enrollment(ctx context.Context, email string) (docstore.DirectoryEnrollment, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollment{}, ErrDirectoryOff
	}
	if !docstore.ValidAddress(email) {
		return docstore.DirectoryEnrollment{}, docstore.ErrDirectoryBadAddress
	}
	rec, _, err := d.readEnrollment(ctx, email)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	return d.enrollmentView(rec), nil
}

// writeEnrollment saves rec (signed now) under the current key's name, only if the record is
// still the one with digest prev ("" if there was none), and drops a copy under a retired name.
func (d *AdapterDirectory) writeEnrollment(ctx context.Context, email string, rec enrollRecord, prev string) error {
	names := d.enrollNames(email)
	id := names[0]
	rec.V, rec.Kid, rec.Email = 1, d.Signer.KeyID(), normalizeEmail(email)
	rec.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(rec.message(id)))
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if prev != "" {
		// The record read may sit under a retired name: then the current name is new.
		if _, err := d.Store.Load(ctx, id); errors.Is(err, docstore.ErrNotFound) {
			prev = ""
		}
	}
	if err := d.saveIf(ctx, id, data, prev); err != nil {
		return err
	}
	for _, other := range names[1:] {
		_ = d.Store.Delete(ctx, other)
	}
	return nil
}

// checkLogPosition says prev names an entry that is in the log (or the empty log).
func (d *AdapterDirectory) checkLogPosition(ctx context.Context, prev string) error {
	seq, hash, err := ParseLogPosition(prev)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadOwnerSig, err)
	}
	if seq == 0 {
		if hash != zeroHash {
			return fmt.Errorf("%w: not the start of the log", ErrBadOwnerSig)
		}
		return nil
	}
	e, found, err := d.readEntry(ctx, seq)
	if err != nil {
		return err
	}
	if !found || e.Hash != hash {
		return fmt.Errorf("%w: no such log position", ErrBadOwnerSig)
	}
	return nil
}

// Errors of the owner part.
var (
	ErrOwnerRequired = docstore.ErrDirectoryOwnerRequired
	ErrBadOwnerSig   = docstore.ErrDirectoryBadOwnerSig
	ErrEnrolled      = docstore.ErrDirectoryEnrolled
	ErrNotEnrolled   = docstore.ErrDirectoryNotEnrolled
	ErrResetWait     = docstore.ErrDirectoryResetWait
)

// checkOwner checks the owner signature an operation on an enrolled address needs: it must be
// there, dated within ownerSkew of now, name a position that is in the log, and verify under the
// current owner key. With no enrollment nothing is required (and a signature sent is ignored).
func (d *AdapterDirectory) checkOwner(ctx context.Context, enr *enrollRecord, op, email, fingerprint string, o *docstore.DirectoryOwnerSig, now time.Time) (*docstore.DirectoryOwnerSig, error) {
	if enr == nil {
		return nil, nil
	}
	if o == nil {
		return nil, ErrOwnerRequired
	}
	if skew := now.Unix() - o.Time; skew > int64(ownerSkew/time.Second) || -skew > int64(ownerSkew/time.Second) {
		return nil, fmt.Errorf("%w: it is dated %d seconds from now", ErrBadOwnerSig, skew)
	}
	cur, ok := enr.current()
	if !ok {
		return nil, fmt.Errorf("%w: no current owner key", ErrBadOwnerSig)
	}
	pub, err := ParseOwnerPub(cur.Pub)
	if err != nil || !verifyOwner(pub, op, email, fingerprint, o.Time, o.Prev, o.Sig) {
		return nil, ErrBadOwnerSig
	}
	if err := d.checkLogPosition(ctx, o.Prev); err != nil {
		return nil, err
	}
	return o, nil
}

// enrollToken is the payload of an enroll challenge's token: it fixes the time and log position
// the enrollment signatures must carry, for this user and address, until Exp. It is signed by the
// current server key only.
type enrollToken struct {
	ID    string `json:"id"`
	Kid   string `json:"kid"`
	User  string `json:"u"`
	Email string `json:"e"`
	Time  int64  `json:"t"`
	Prev  string `json:"p"`
	Exp   int64  `json:"x"`
}

func enrollMessage(payload []byte) []byte {
	return append([]byte("gojiberry-directory-enroll-challenge-v1|"), payload...)
}

// EnrollChallenge starts an enrollment, rotation or reset for user's own address: the time and
// log position the signatures must name, and a single-use token that binds them to the user and
// address (valid ChallengeTTL).
func (d *AdapterDirectory) EnrollChallenge(ctx context.Context, user auth.User) (docstore.DirectoryEnrollChallenge, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollChallenge{}, ErrDirectoryOff
	}
	if err := d.checkUser(user); err != nil {
		return docstore.DirectoryEnrollChallenge{}, err
	}
	head, err := d.latest(ctx)
	if err != nil {
		return docstore.DirectoryEnrollChallenge{}, err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return docstore.DirectoryEnrollChallenge{}, err
	}
	now := d.now()
	tok := enrollToken{ID: hex.EncodeToString(raw[:]), Kid: d.Signer.KeyID(), User: user.ID, Email: normalizeEmail(user.Email),
		Time: now.Unix(), Prev: FormatLogPosition(head.Seq, head.Hash), Exp: now.Add(ChallengeTTL).Unix()}
	payload, _ := json.Marshal(tok)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(d.Signer.Sign(enrollMessage(payload)))
	return docstore.DirectoryEnrollChallenge{Token: token, Time: tok.Time, Prev: tok.Prev, Expires: tok.Exp}, nil
}

func (d *AdapterDirectory) checkEnrollToken(user auth.User, token string, now time.Time) (enrollToken, error) {
	bad := func(why string) (enrollToken, error) { return enrollToken{}, fmt.Errorf("%w: %s", ErrBadProof, why) }
	pl, sg, ok := strings.Cut(token, ".")
	if !ok {
		return bad("malformed")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(pl)
	sig, err2 := base64.RawURLEncoding.DecodeString(sg)
	var tok enrollToken
	if err1 != nil || err2 != nil || json.Unmarshal(payload, &tok) != nil {
		return bad("malformed")
	}
	// Only the current key: a challenge issued under a retired key is refused (it lives two minutes).
	if tok.Kid == "" || tok.Kid != d.Signer.KeyID() || !d.Signer.Verify(tok.Kid, enrollMessage(payload), sig) {
		return bad("not issued by this directory")
	}
	if now.Unix() > tok.Exp {
		return bad("expired")
	}
	if tok.User != user.ID || tok.Email != normalizeEmail(user.Email) {
		return bad("issued for another user")
	}
	if tok.ID == "" || strings.Trim(tok.ID, "0123456789abcdef") != "" {
		return bad("malformed")
	}
	return tok, nil
}

// checkUser is the common gate of the operations on the user's own address.
func (d *AdapterDirectory) checkUser(user auth.User) error {
	if !publishable(user) {
		return ErrNotPublishable
	}
	if !docstore.ValidAddress(user.Email) {
		return fmt.Errorf("%w: %w", ErrNotPublishable, docstore.ErrDirectoryBadAddress)
	}
	return nil
}

func decodePub(s string) (ed25519.PublicKey, error) {
	pub, err := ParseOwnerPub(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadOwnerSig, err)
	}
	return pub, nil
}

// Enroll installs an owner key for user's own address. mode "enroll": the address has none, and
// req.Sig is the new key's signature over the enroll operation (possession; the session is the
// authority: trust on first use). "rotate": req.Sig is the current owner key's signature over
// the rotate operation naming the new key, and req.Proof the new key's acceptance. "reset": a
// reset was requested at least ResetWait ago (RequestReset) and not cancelled; req.Proof is the
// new key's acceptance. The time and log position in the signatures are the ones the challenge
// (EnrollChallenge) fixed, and the challenge is used up. The change is logged first, with the
// signatures, so that anyone who knows the address can audit it (AuditAddress).
func (d *AdapterDirectory) Enroll(ctx context.Context, user auth.User, req docstore.DirectoryEnroll) (docstore.DirectoryEnrollment, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollment{}, ErrDirectoryOff
	}
	if err := d.checkUser(user); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	email := normalizeEmail(user.Email)
	now := d.now()
	tok, err := d.checkEnrollToken(user, req.Token, now)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	if req.Time != tok.Time || req.Prev != tok.Prev {
		return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: the time and log position are not the challenge's", ErrBadOwnerSig)
	}
	pub, err := decodePub(req.OwnerPub)
	if err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	newFP := OwnerFingerprint(pub)
	var op string
	switch req.Mode {
	case OpEnroll, OpRotate, OpReset:
		op = req.Mode
	default:
		return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: mode is enroll, rotate or reset", ErrBadOwnerSig)
	}
	defer d.lockAddress(email)()
	claimed := false
	for round := 0; round < maxCASRounds; round++ {
		enr, digest, err := d.readEnrollment(ctx, email)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		in := logInput{Op: op, Email: email, Fingerprint: newFP, OwnerPub: FormatOwnerPub(pub), OpTime: tok.Time, OwnerPrev: tok.Prev}
		next := enrollRecord{}
		if enr != nil {
			next = *enr
			next.Keys = append([]docstore.DirectoryOwnerKey(nil), enr.Keys...)
		}
		switch op {
		case OpEnroll:
			if enr != nil {
				return docstore.DirectoryEnrollment{}, ErrEnrolled
			}
			if !verifyOwner(pub, OpEnroll, email, newFP, tok.Time, tok.Prev, req.Sig) {
				return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: the new key did not sign the enrollment", ErrBadOwnerSig)
			}
			in.OwnerSig = req.Sig
		case OpRotate, OpReset:
			if enr == nil {
				return docstore.DirectoryEnrollment{}, ErrNotEnrolled
			}
			cur, _ := enr.current()
			if cur.Pub == FormatOwnerPub(pub) {
				return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: that is the current owner key", ErrEnrolled)
			}
			if !verifyOwner(pub, "accept", email, newFP, tok.Time, tok.Prev, req.Proof) {
				return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: the new key did not accept", ErrBadOwnerSig)
			}
			in.ProofSig = req.Proof
			if op == OpRotate {
				curPub, err := ParseOwnerPub(cur.Pub)
				if err != nil || !verifyOwner(curPub, OpRotate, email, newFP, tok.Time, tok.Prev, req.Sig) {
					return docstore.DirectoryEnrollment{}, fmt.Errorf("%w: the current owner key did not sign the rotation", ErrBadOwnerSig)
				}
				in.OwnerSig = req.Sig
			} else if enr.ResetSince == 0 || now.Unix() < enr.ResetSince+int64(d.resetWait()/time.Second) {
				return docstore.DirectoryEnrollment{}, ErrResetWait
			}
		}
		if !claimed {
			if err := d.claimChallenge(ctx, challengeToken{ID: tok.ID, Exp: tok.Exp}); err != nil {
				return docstore.DirectoryEnrollment{}, err
			}
			claimed = true
		}
		seq, err := d.appendLog(ctx, in, now)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		if n := len(next.Keys); n > 0 {
			next.Keys[n-1].Until = seq
		}
		next.Keys = append(next.Keys, docstore.DirectoryOwnerKey{Pub: FormatOwnerPub(pub), Seq: seq})
		next.ResetSince, next.Seq = 0, seq
		switch err := d.writeEnrollment(ctx, email, next, digest); {
		case err == nil:
			view := d.enrollmentView(&next)
			return view, nil
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, newFP, now)
		default:
			d.voidLog(ctx, email, seq, newFP, now)
			return docstore.DirectoryEnrollment{}, err
		}
	}
	return docstore.DirectoryEnrollment{}, docstore.ErrDirectoryBusy
}

// RequestReset starts the waiting period of a reset of user's own address's owner key: a provider
// session replaces a lost owner key, but not at once. The request is logged (op "reset-request",
// no signature), so the owner and everybody who watches the address see it, and Enroll with mode
// "reset" is accepted only ResetWait later; the owner cancels it with their own key
// (CancelReset). Asking again while one is pending changes nothing.
func (d *AdapterDirectory) RequestReset(ctx context.Context, user auth.User) (docstore.DirectoryEnrollment, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollment{}, ErrDirectoryOff
	}
	if err := d.checkUser(user); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	email := normalizeEmail(user.Email)
	now := d.now()
	defer d.lockAddress(email)()
	for round := 0; round < maxCASRounds; round++ {
		enr, digest, err := d.readEnrollment(ctx, email)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		if enr == nil {
			return docstore.DirectoryEnrollment{}, ErrNotEnrolled
		}
		if enr.ResetSince > 0 {
			return d.enrollmentView(enr), nil
		}
		seq, err := d.appendLog(ctx, logInput{Op: OpResetRequest, Email: email}, now)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		next := *enr
		next.ResetSince, next.Seq = now.Unix(), seq
		switch err := d.writeEnrollment(ctx, email, next, digest); {
		case err == nil:
			return d.enrollmentView(&next), nil
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, "", now)
		default:
			d.voidLog(ctx, email, seq, "", now)
			return docstore.DirectoryEnrollment{}, err
		}
	}
	return docstore.DirectoryEnrollment{}, docstore.ErrDirectoryBusy
}

// CancelReset ends a pending reset; it needs the current owner key's signature over the
// "reset-cancel" operation, so a provider session alone cannot cancel (or keep) a reset the owner
// wants gone.
func (d *AdapterDirectory) CancelReset(ctx context.Context, user auth.User, owner *docstore.DirectoryOwnerSig) (docstore.DirectoryEnrollment, error) {
	if d.Signer == nil {
		return docstore.DirectoryEnrollment{}, ErrDirectoryOff
	}
	if err := d.checkUser(user); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
	email := normalizeEmail(user.Email)
	now := d.now()
	defer d.lockAddress(email)()
	for round := 0; round < maxCASRounds; round++ {
		enr, digest, err := d.readEnrollment(ctx, email)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		if enr == nil {
			return docstore.DirectoryEnrollment{}, ErrNotEnrolled
		}
		if enr.ResetSince == 0 {
			return docstore.DirectoryEnrollment{}, ErrResetWait
		}
		o, err := d.checkOwner(ctx, enr, OpResetCancel, email, "", owner, now)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		seq, err := d.appendLog(ctx, logInput{Op: OpResetCancel, Email: email, OwnerSig: o.Sig, OpTime: o.Time, OwnerPrev: o.Prev}, now)
		if err != nil {
			return docstore.DirectoryEnrollment{}, err
		}
		next := *enr
		next.ResetSince, next.Seq = 0, seq
		switch err := d.writeEnrollment(ctx, email, next, digest); {
		case err == nil:
			return d.enrollmentView(&next), nil
		case errors.Is(err, docstore.ErrConflict):
			d.voidLog(ctx, email, seq, "", now)
		default:
			d.voidLog(ctx, email, seq, "", now)
			return docstore.DirectoryEnrollment{}, err
		}
	}
	return docstore.DirectoryEnrollment{}, docstore.ErrDirectoryBusy
}

// maxCASRounds bounds how often an operation re-reads and retries after a conditional write lost
// to another replica.
const maxCASRounds = 8

// AuditViolation is one entry of the log that breaks the rules of an enrolled address.
type AuditViolation struct {
	Seq int64
	Why string
}

// AddressAudit is what AuditAddress found about one address in a run of the log.
type AddressAudit struct {
	// Entries are the entries about the address that took effect (void ones and the entries
	// they cancelled are left out), in order.
	Entries []LogEntry
	// Enrolled says the address has an owner key after the last entry; OwnerKeys is the chain
	// (the last is current; Seq is the enrolling entry and Until the replacing one) and
	// ResetSince the time (Unix seconds) of a reset request still pending, 0 if none.
	Enrolled   bool
	OwnerKeys  []docstore.DirectoryOwnerKey
	ResetSince int64
	// Violations are the entries that break the rules: an operation on an enrolled address
	// without a valid signature of the owner key then current, an enrollment on an enrolled
	// address, a signature dated far from the entry, a reset that took effect before its
	// waiting period was over or was never requested, a rotation or reset with no acceptance by
	// the new key. A server alone cannot make an entry that passes; one that does not is proof
	// that somebody with the directory key (not the owner) wrote it.
	Violations []AuditViolation
}

// AuditOptions are the rules AuditAddress applies.
type AuditOptions struct {
	// ResetWait is the shortest time a reset may take between its request and its taking effect
	// (DefaultResetWait when 0): an auditor holds the server to the 24 hours it promises, not to
	// whatever the server was set to.
	ResetWait time.Duration
	// Start is the state before the entries, when they continue a run already audited (the
	// AddressAudit of the earlier run); nil for the start of the log.
	Start *AddressAudit
}

// AuditAddress checks the entries of the log that are about one address (addresses are the
// address's pseudonyms, LogAddressFor under the directory's keys, as GET /directory/log/address
// gives the owner; email the address itself, which the owner signatures name) against the rules
// of an owner-enrolled address: it replays the enrollment state and flags every entry that the
// owner key then current did not sign. entries is a run of the log (all addresses; the entries
// of other addresses are skipped), already checked for chain and signatures with VerifyLog.
func AuditAddress(email string, addresses []string, entries []LogEntry, opt AuditOptions) AddressAudit {
	mine := map[string]bool{}
	for _, a := range addresses {
		mine[a] = true
	}
	wait := opt.ResetWait
	if wait <= 0 {
		wait = DefaultResetWait
	}
	var out AddressAudit
	if opt.Start != nil {
		out = *opt.Start
		out.Entries = nil
		out.Violations = nil
		out.OwnerKeys = append([]docstore.DirectoryOwnerKey(nil), opt.Start.OwnerKeys...)
	}
	bySeq := map[int64]LogEntry{}
	voided := map[int64]bool{}
	for _, e := range entries {
		bySeq[e.Seq] = e
		if e.Op == OpVoid && mine[e.Address] {
			voided[e.Ref] = true
		}
	}
	bad := func(e LogEntry, why string) { out.Violations = append(out.Violations, AuditViolation{e.Seq, why}) }
	cur := func() (ed25519.PublicKey, bool) {
		if !out.Enrolled || len(out.OwnerKeys) == 0 {
			return nil, false
		}
		pub, err := ParseOwnerPub(out.OwnerKeys[len(out.OwnerKeys)-1].Pub)
		return pub, err == nil
	}
	// timed checks what every owner signature names: the time near the entry's, a position that
	// is earlier in the log (and, when we hold that entry, its hash).
	timed := func(e LogEntry) bool {
		if d := e.At - e.OpTime; d > int64(auditSkew/time.Second) || -d > int64(auditSkew/time.Second) {
			bad(e, "the owner signature is dated far from the entry")
			return false
		}
		seq, hash, err := ParseLogPosition(e.OwnerPrev)
		if err != nil || seq >= e.Seq {
			bad(e, "the owner signature names a log position that is not before the entry")
			return false
		}
		if p, ok := bySeq[seq]; ok && p.Hash != hash {
			bad(e, "the owner signature names a log position that is not the log's")
			return false
		}
		return true
	}
	for _, e := range entries {
		if !mine[e.Address] || e.Op == OpVoid || voided[e.Seq] {
			continue
		}
		out.Entries = append(out.Entries, e)
		switch e.Op {
		case OpPublish, OpRevoke:
			if !out.Enrolled {
				continue
			}
			pub, ok := cur()
			switch {
			case !ok || e.OwnerSig == "":
				bad(e, "no owner signature on an enrolled address")
			case !timed(e):
			case !verifyOwner(pub, e.Op, email, e.Fingerprint, e.OpTime, e.OwnerPrev, e.OwnerSig):
				bad(e, "the owner signature does not verify under the current owner key")
			}
		case OpEnroll:
			if out.Enrolled {
				bad(e, "an enrollment on an address that already has an owner key")
				continue
			}
			pub, err := ParseOwnerPub(e.OwnerPub)
			if err != nil {
				bad(e, "the enrollment names no owner key")
				continue
			}
			if !timed(e) {
				continue
			}
			if !verifyOwner(pub, OpEnroll, email, OwnerFingerprint(pub), e.OpTime, e.OwnerPrev, e.OwnerSig) {
				bad(e, "the new key did not sign its enrollment")
				continue
			}
			out.Enrolled = true
			out.OwnerKeys = []docstore.DirectoryOwnerKey{{Pub: e.OwnerPub, Seq: e.Seq}}
			out.ResetSince = 0
		case OpRotate, OpReset:
			newPub, err := ParseOwnerPub(e.OwnerPub)
			if err != nil || !out.Enrolled {
				bad(e, "a rotation or reset of an address with no owner key")
				continue
			}
			if !timed(e) {
				continue
			}
			ok := verifyOwner(newPub, "accept", email, OwnerFingerprint(newPub), e.OpTime, e.OwnerPrev, e.ProofSig)
			if !ok {
				bad(e, "the new key did not accept")
				continue
			}
			if e.Op == OpRotate {
				pub, have := cur()
				if !have || !verifyOwner(pub, OpRotate, email, OwnerFingerprint(newPub), e.OpTime, e.OwnerPrev, e.OwnerSig) {
					bad(e, "the current owner key did not sign the rotation")
					continue
				}
			} else if out.ResetSince == 0 || e.At < out.ResetSince+int64(wait/time.Second) {
				bad(e, "a reset that took effect before its waiting period was over, or was never requested")
				continue
			}
			out.OwnerKeys[len(out.OwnerKeys)-1].Until = e.Seq
			out.OwnerKeys = append(out.OwnerKeys, docstore.DirectoryOwnerKey{Pub: e.OwnerPub, Seq: e.Seq})
			out.ResetSince = 0
		case OpResetRequest:
			if !out.Enrolled {
				bad(e, "a reset requested for an address with no owner key")
				continue
			}
			if out.ResetSince == 0 {
				out.ResetSince = e.At
			}
		case OpResetCancel:
			pub, ok := cur()
			switch {
			case !out.Enrolled || out.ResetSince == 0:
				bad(e, "a reset cancelled that was not pending")
			case !ok || !timed(e):
			case !verifyOwner(pub, OpResetCancel, email, "", e.OpTime, e.OwnerPrev, e.OwnerSig):
				bad(e, "the cancellation is not signed by the current owner key")
			default:
				out.ResetSince = 0
			}
		}
	}
	return out
}
