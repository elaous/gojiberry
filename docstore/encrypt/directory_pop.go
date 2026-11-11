package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// ChallengeTTL is how long a challenge can be answered.
const ChallengeTTL = 2 * time.Minute

const popGrace = time.Minute

const attestSkew = 10 * time.Minute

var errBadPublicKey = errors.New("not a public key")

type challengeToken struct {
	ID    string `json:"id"`
	Kid   string `json:"kid"`
	User  string `json:"u"`
	Email string `json:"e"`
	FP    string `json:"f"`
	Exp   int64  `json:"x"`
	H     string `json:"h"`
}

func challengeMessage(payload []byte) []byte {
	return append([]byte("gojiberry-directory-challenge-v1|"), payload...)
}

func popHash(id, nonce string) string { return "pop:" + id + ":" + nonce }

// Challenge makes a proof-of-possession challenge for a user.
func (d *AdapterDirectory) Challenge(_ context.Context, user auth.User, keyText string) (docstore.DirectoryChallenge, error) {
	if d.Signer == nil {
		return docstore.DirectoryChallenge{}, ErrDirectoryOff
	}
	if err := d.checkUser(user); err != nil {
		return docstore.DirectoryChallenge{}, err
	}
	r, err := parsePublic(keyText)
	if err != nil {
		return docstore.DirectoryChallenge{}, fmt.Errorf("%w: %v", errBadPublicKey, err)
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return docstore.DirectoryChallenge{}, err
	}
	id, nonce := hex.EncodeToString(raw[:16]), hex.EncodeToString(raw[16:])
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, r.Key)
	if err != nil {
		return docstore.DirectoryChallenge{}, err
	}
	if _, err := io.WriteString(w, nonce); err != nil {
		return docstore.DirectoryChallenge{}, err
	}
	if err := w.Close(); err != nil {
		return docstore.DirectoryChallenge{}, err
	}
	exp := d.now().Add(ChallengeTTL).Unix()
	payload, _ := json.Marshal(challengeToken{ID: id, Kid: d.Signer.KeyID(), User: user.ID, Email: normalizeEmail(user.Email), FP: r.Fingerprint, Exp: exp, H: d.Signer.Pseudonym(popHash(id, nonce))})
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(d.Signer.Sign(challengeMessage(payload)))
	return docstore.DirectoryChallenge{Token: token, Encrypted: base64.StdEncoding.EncodeToString(enc.Bytes()), Expires: exp}, nil
}

const nonceLen = 32

// ErrNotAChallenge is returned by OpenChallenge for a non-challenge.
var ErrNotAChallenge = errors.New("encrypt: this is not a directory challenge: it decrypts to something other than a nonce, so nothing was sent back")

// OpenChallenge is the client's half: it decrypts a challenge.
func OpenChallenge(c docstore.DirectoryChallenge, id age.Identity) (docstore.DirectoryProof, error) {
	b, err := base64.StdEncoding.DecodeString(c.Encrypted)
	if err != nil {
		return docstore.DirectoryProof{}, errors.New("encrypt: the challenge is not base64")
	}
	r, err := age.Decrypt(bytes.NewReader(b), id)
	if err != nil {
		return docstore.DirectoryProof{}, fmt.Errorf("encrypt: this key cannot open the challenge: %w", err)
	}
	nonce, err := io.ReadAll(io.LimitReader(r, nonceLen+1))
	if err != nil {
		return docstore.DirectoryProof{}, err
	}
	if len(nonce) != nonceLen || strings.Trim(string(nonce), "0123456789abcdef") != "" {
		return docstore.DirectoryProof{}, ErrNotAChallenge
	}
	return docstore.DirectoryProof{Token: c.Token, Nonce: string(nonce)}, nil
}

func (d *AdapterDirectory) checkProof(user auth.User, r Recipient, token, nonce string, now time.Time) (challengeToken, error) {
	bad := func(why string) (challengeToken, error) {
		return challengeToken{}, fmt.Errorf("%w: %s", ErrBadProof, why)
	}
	pl, sg, ok := strings.Cut(token, ".")
	if !ok {
		return bad("malformed")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(pl)
	sig, err2 := base64.RawURLEncoding.DecodeString(sg)
	var tok challengeToken
	if err1 != nil || err2 != nil || json.Unmarshal(payload, &tok) != nil {
		return bad("malformed")
	}
	if tok.Kid == "" || tok.Kid != d.Signer.KeyID() || !d.Signer.Verify(tok.Kid, challengeMessage(payload), sig) {
		return bad("not issued by this directory")
	}
	if now.Unix() > tok.Exp {
		return bad("expired")
	}
	if tok.User != user.ID || tok.Email != normalizeEmail(user.Email) || tok.FP != r.Fingerprint {
		return bad("issued for another user or key")
	}
	okNonce := subtle.ConstantTimeCompare([]byte(d.Signer.Pseudonym(popHash(tok.ID, nonce))), []byte(tok.H)) == 1
	if !okNonce || tok.ID == "" || strings.Trim(tok.ID, "0123456789abcdef") != "" {
		return bad("wrong nonce")
	}
	return tok, nil
}

func (d *AdapterDirectory) claimChallenge(ctx context.Context, tok challengeToken) error {
	data, _ := json.Marshal(map[string]int64{"exp": tok.Exp})
	err := d.createOnly(ctx, popPrefix+tok.ID, data)
	if errors.Is(err, docstore.ErrConflict) {
		return fmt.Errorf("%w: already used", ErrBadProof)
	}
	return err
}

func (d *AdapterDirectory) createOnly(ctx context.Context, id string, data []byte) error {
	if c, ok := d.Store.(docstore.ConditionalAdapter); ok {
		err := c.SaveIf(ctx, id, data, "")
		if !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return err
		}
	}
	d.casMu.Lock()
	defer d.casMu.Unlock()
	if _, err := d.Store.Load(ctx, id); err == nil {
		return docstore.ErrConflict
	} else if !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	return d.Store.Save(ctx, id, data)
}

func (d *AdapterDirectory) saveIf(ctx context.Context, id string, data []byte, prev string) error {
	if c, ok := d.Store.(docstore.ConditionalAdapter); ok {
		err := c.SaveIf(ctx, id, data, prev)
		if !errors.Is(err, docstore.ErrConditionalUnsupported) {
			return err
		}
	}
	d.casMu.Lock()
	defer d.casMu.Unlock()
	doc, err := d.Store.Load(ctx, id)
	switch {
	case errors.Is(err, docstore.ErrNotFound):
		if prev != "" {
			return docstore.ErrConflict
		}
	case err != nil:
		return err
	case prev == "" || docstore.Digest(doc.Data) != prev:
		return docstore.ErrConflict
	}
	return d.Store.Save(ctx, id, data)
}

// AttestationMessage is what an ssh-ed25519 key signs.
func AttestationMessage(email, fingerprint string, at int64) []byte {
	return []byte("directory-binding|" + normalizeEmail(email) + "|" + fingerprint + "|" + strconv.FormatInt(at, 10))
}

func Attest(priv ed25519.PrivateKey, email string, at time.Time) (docstore.DirectoryAttestation, error) {
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return docstore.DirectoryAttestation{}, err
	}
	msg := AttestationMessage(email, ssh.FingerprintSHA256(pub), at.Unix())
	return docstore.DirectoryAttestation{At: at.Unix(), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))}, nil
}

// AttestSSH is Attest for an unencrypted OpenSSH ed25519 key.
func AttestSSH(pem []byte, email string, at time.Time) (publicKey string, att docstore.DirectoryAttestation, err error) {
	raw, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		return "", att, fmt.Errorf("encrypt: %w", err)
	}
	var priv ed25519.PrivateKey
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		priv = *k
	case ed25519.PrivateKey:
		priv = k
	default:
		return "", att, errors.New("encrypt: only an ssh-ed25519 key can attest")
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return "", att, err
	}
	att, err = Attest(priv, email, at)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), att, err
}

func sshEd25519Public(text string) (ed25519.PublicKey, bool) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(text)))
	if err != nil || pub.Type() != "ssh-ed25519" {
		return nil, false
	}
	cp, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return nil, false
	}
	k, ok := cp.CryptoPublicKey().(ed25519.PublicKey)
	return k, ok
}

// VerifyAttestation says att is a valid signature by the key.
func VerifyAttestation(keyText, email string, att docstore.DirectoryAttestation) bool {
	pub, ok := sshEd25519Public(keyText)
	if !ok {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(att.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	r, err := ParseSSHRecipient(keyText)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, AttestationMessage(email, r.Fingerprint, att.At), sig)
}
