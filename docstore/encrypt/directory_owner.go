package encrypt

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"strconv"
	"strings"
	"time"
)

const DefaultResetWait = 24 * time.Hour

// max clock difference for an owner signature
const ownerSkew = 10 * time.Minute

const auditSkew = ownerSkew + time.Minute

// OwnerOpMessage is what the owner key signs for an operation.
func OwnerOpMessage(op, email, fingerprint string, at int64, prev string) []byte {
	return []byte("directory-op|" + op + "|" + normalizeEmail(email) + "|" + fingerprint + "|" + strconv.FormatInt(at, 10) + "|" + prev)
}

// OwnerPublishMessageV2 is what the owner key signs for a publication.
func OwnerPublishMessageV2(email, fingerprint, labelHash string, at int64, prev string) []byte {
	return []byte("directory-op-v2|" + OpPublish + "|" + normalizeEmail(email) + "|" + fingerprint + "|" + labelHash + "|" + strconv.FormatInt(at, 10) + "|" + prev)
}

// LabelHash is the hex SHA-256 of a key's cleaned label.
func LabelHash(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

// CleanLabel is the label as the directory stores it.
func CleanLabel(s string) string { return cleanLabel(s) }

// SignOwnerPublish signs a publication of the key.
func SignOwnerPublish(priv ed25519.PrivateKey, email, fingerprint, label string, at time.Time, prev string) docstore.DirectoryOwnerSig {
	return docstore.DirectoryOwnerSig{Time: at.Unix(), Prev: prev, V: 2,
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, OwnerPublishMessageV2(email, fingerprint, LabelHash(CleanLabel(label)), at.Unix(), prev)))}
}

// SignOwnerPublishWith is SignOwnerPublish with a given signer.
func SignOwnerPublishWith(signer crypto.Signer, email, fingerprint, label string, at time.Time, prev string) (docstore.DirectoryOwnerSig, error) {
	sig, err := signer.Sign(nil, OwnerPublishMessageV2(email, fingerprint, LabelHash(CleanLabel(label)), at.Unix(), prev), crypto.Hash(0))
	if err != nil {
		return docstore.DirectoryOwnerSig{}, fmt.Errorf("encrypt: signing with the owner key: %w", err)
	}
	return docstore.DirectoryOwnerSig{Time: at.Unix(), Prev: prev, V: 2, Sig: base64.StdEncoding.EncodeToString(sig)}, nil
}

func verifyOwnerPublishV2(pub ed25519.PublicKey, email, fingerprint, labelHash string, at int64, prev, sig string) bool {
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(b) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, OwnerPublishMessageV2(email, fingerprint, labelHash, at, prev), b)
}

func verifyOwnerVersion(pub ed25519.PublicKey, v int, op, email, fingerprint, labelHash string, at int64, prev, sig string) bool {
	if v == 2 {
		return op == OpPublish && verifyOwnerPublishV2(pub, email, fingerprint, labelHash, at, prev, sig)
	}
	return verifyOwner(pub, op, email, fingerprint, at, prev, sig)
}

// OwnerFingerprint names an owner public key.
func OwnerFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "OWNER:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// GenerateOwnerKey makes an owner signing key.
func GenerateOwnerKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// FormatOwnerPub is the form an owner public key travels in.
func FormatOwnerPub(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// ParseOwnerPub reads an owner public key.
func ParseOwnerPub(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("encrypt: not an owner public key")
	}
	return ed25519.PublicKey(b), nil
}

// FormatLogPosition is "<seq>:<hash>": the log position.
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

// SignOwnerOp signs an operation with the owner key.
func SignOwnerOp(priv ed25519.PrivateKey, op, email, fingerprint string, at time.Time, prev string) docstore.DirectoryOwnerSig {
	return docstore.DirectoryOwnerSig{Time: at.Unix(), Prev: prev,
		Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, OwnerOpMessage(op, email, fingerprint, at.Unix(), prev)))}
}

// SignOwnerOpWith is SignOwnerOp for any crypto.Signer.
func SignOwnerOpWith(signer crypto.Signer, op, email, fingerprint string, at time.Time, prev string) (docstore.DirectoryOwnerSig, error) {
	sig, err := signer.Sign(nil, OwnerOpMessage(op, email, fingerprint, at.Unix(), prev), crypto.Hash(0))
	if err != nil {
		return docstore.DirectoryOwnerSig{}, fmt.Errorf("encrypt: signing with the owner key: %w", err)
	}
	return docstore.DirectoryOwnerSig{Time: at.Unix(), Prev: prev, Sig: base64.StdEncoding.EncodeToString(sig)}, nil
}

func verifyOwner(pub ed25519.PublicKey, op, email, fingerprint string, at int64, prev, sig string) bool {
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(b) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, OwnerOpMessage(op, email, fingerprint, at, prev), b)
}

func VerifyOwnerOp(pub ed25519.PublicKey, op, email, fingerprint string, at int64, prev, sig string) bool {
	return verifyOwner(pub, op, email, fingerprint, at, prev, sig)
}

// VerifyOwnerOpV is VerifyOwnerOp for a message of a given version.
func VerifyOwnerOpV(pub ed25519.PublicKey, v int, op, email, fingerprint, labelHash string, at int64, prev, sig string) bool {
	return verifyOwnerVersion(pub, v, op, email, fingerprint, labelHash, at, prev, sig)
}

// VerifyOwnerEvidence checks owner evidence without the server.
func VerifyOwnerEvidence(email string, k docstore.DirectoryKey) bool {
	if k.Owner == nil {
		return false
	}
	pub, err := ParseOwnerPub(k.Owner.Pub)
	if err != nil {
		return false
	}
	return verifyOwnerVersion(pub, k.Owner.V, OpPublish, email, k.Fingerprint, LabelHash(k.Label), k.Owner.Time, k.Owner.Prev, k.Owner.Sig)
}

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

// an unreadable or unverifiable record is an error, never none
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

// Enrollment is the owner enrollment of an address.
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
		// the record may sit under a retired name: current name is new
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

func (d *AdapterDirectory) checkOwner(ctx context.Context, enr *enrollRecord, op, email, fingerprint, labelHash string, o *docstore.DirectoryOwnerSig, now time.Time) (*docstore.DirectoryOwnerSig, error) {
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
	if err != nil {
		return nil, ErrBadOwnerSig
	}
	if op == OpPublish {
		// signed over the label too (v2): a v1 signature allows any label
		if o.V != 2 || !verifyOwnerPublishV2(pub, email, fingerprint, labelHash, o.Time, o.Prev, o.Sig) {
			return nil, fmt.Errorf("%w: a publication is signed with version 2 of the message, which covers the label", ErrBadOwnerSig)
		}
	} else if !verifyOwner(pub, op, email, fingerprint, o.Time, o.Prev, o.Sig) {
		return nil, ErrBadOwnerSig
	}
	if err := d.checkLogPosition(ctx, o.Prev); err != nil {
		return nil, err
	}
	return o, nil
}

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

// EnrollChallenge starts an enrollment, rotation or reset.
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
	// only the current key: a challenge under a retired key is refused
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

// Enroll installs an owner key for a user's own address.
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
	if err := d.reconcileLocked(ctx, email, now); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
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

// RequestReset starts the waiting period of a reset.
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
	if err := d.reconcileLocked(ctx, email, now); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
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

// CancelReset ends a pending reset.
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
	if err := d.reconcileLocked(ctx, email, now); err != nil {
		return docstore.DirectoryEnrollment{}, err
	}
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
		o, err := d.checkOwner(ctx, enr, OpResetCancel, email, "", "", owner, now)
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

const maxCASRounds = 8
