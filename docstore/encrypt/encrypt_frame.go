package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"log"
)

var header = []byte("age-encryption.org/v1\n")

// The frame is the plaintext inside the encryption. Version 1 (written before the protection
// level was bound in): magic "GJE\x01", the id as a uvarint length and bytes, the data.
// Version 2: magic "GJE\x02", the id, then a level byte (0 server, 1 end to end), the seq of the
// signed record as a uvarint, the 32-byte hash of its recipient list, the data (unsigned).
// Version 3: magic "GJE\x03", the same header as v2, then the 32-byte SHA-256 of the data and a
// 64-byte Ed25519 signature over everything before it (frameSigMessage), then the data. The
// signature covers the content hash, so a signed header cannot be put on other content, and a
// reader that only needs the header (the checks that scan a document's files) can verify it
// without reading the rest. A v1 file is read as before and carries no level to cross-check;
// the first Save or re-key of it writes v3 (v2 when the adapter has no Signer and the
// document is not end to end), which pins the level from then on.
var (
	frameMagicV1 = []byte("GJE\x01")
	frameMagicV2 = []byte("GJE\x02")
	frameMagic   = []byte("GJE\x03")
)

const frameSigContext = "gojiberry/frame/v3"

const (
	levelServer byte = 0
	levelE2EE   byte = 1
)

// ErrDowngrade is returned (wrapped, with the reason) when a file proves a document's
// protection was lowered behind the owner's back: the file was sealed under record seq N or at
// the end-to-end level, and the keys:<ID> record is now missing, older than N (a replayed
// record), or (Load only) says a level the file is not wrapped for with no switch in progress.
// Save and re-key write nothing and Load returns no data. It is docstore.ErrDowngrade.
// Sharing.Recover is the owner's way forward when the record was restored on purpose.
var ErrDowngrade = docstore.ErrDowngrade

func downgrade(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrDowngrade, id, fmt.Sprintf(format, args...))
}

// Hints appended to the findings, so the message says how the owner goes on: Recover when the
// record was lost or replaced (and the owner knows what it should say), Repair when the record is
// right and some file was left wrapped below its level by a switch that was cut short.
const (
	recoverHint  = " (if the record was restored on purpose, the owner can run Sharing.Recover to write a new one)"
	unsignedHint = " (if the owner knows this file is theirs, left by a client without the signing key or by an older build, Sharing.Repair with AcceptUnsigned seals it again, signed; a file that is not theirs must not be accepted: restore or delete it. Sharing.Recover keeps the record's claim and takes the same option)"
	repairHint   = " (if a switch of level was cut short, the owner can run Sharing.Repair to re-key the files; if the record was replaced on purpose, Sharing.Recover)"
)

func levelByte(l string) byte {
	if l == ProtectionE2EE {
		return levelE2EE
	}
	return levelServer
}

func levelString(b byte) (string, bool) {
	switch b {
	case levelServer:
		return "", true
	case levelE2EE:
		return ProtectionE2EE, true
	}
	return "", false
}

func rank(level string) int {
	if level == ProtectionE2EE {
		return 1
	}
	return 0
}

// frameInfo says which frame version a file had and, from v2, the binding in it; for v3 also the
// content hash and signature it carries.
type frameInfo struct {
	version int
	b       Binding
	content [sha256.Size]byte
	sig     []byte
}

// frameSigMessage is what a v3 frame's signature covers.
func frameSigMessage(id string, level byte, seq uint64, recipients, content [sha256.Size]byte) []byte {
	out := append([]byte(frameSigContext), 0)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, level)
	out = binary.AppendUvarint(out, seq)
	out = append(out, recipients[:]...)
	return append(out, content[:]...)
}

// frame writes the frame: v3, signed, when there is a signer, else v2.
func frame(id string, b Binding, data []byte, signer *Signer) []byte {
	out := make([]byte, 0, len(frameMagic)+binary.MaxVarintLen64*2+len(id)+1+2*sha256.Size+ed25519.SignatureSize+len(data))
	if signer == nil {
		out = append(out, frameMagicV2...)
	} else {
		out = append(out, frameMagic...)
	}
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, levelByte(b.Level))
	out = binary.AppendUvarint(out, b.Seq)
	out = append(out, b.Hash[:]...)
	if signer != nil {
		content := sha256.Sum256(data)
		out = append(out, content[:]...)
		out = append(out, ed25519.Sign(signer.key, frameSigMessage(id, levelByte(b.Level), b.Seq, b.Hash, content))...)
	}
	return append(out, data...)
}

// unframe checks the frame of the decrypted b against id and returns the data and what the
// frame said. It does not verify a v3 signature (Adapter.verifyFrame does). A prefix of a frame
// (the header alone) parses too: the data is then short.
func unframe(id string, b []byte) ([]byte, frameInfo, error) {
	var fi frameInfo
	switch {
	case bytes.HasPrefix(b, frameMagic):
		fi.version = 3
		b = b[len(frameMagic):]
	case bytes.HasPrefix(b, frameMagicV2):
		fi.version = 2
		b = b[len(frameMagicV2):]
	case bytes.HasPrefix(b, frameMagicV1):
		fi.version = 1
		b = b[len(frameMagicV1):]
	default:
		return nil, fi, ErrFormat
	}
	n, k := binary.Uvarint(b)
	if k <= 0 || n > uint64(len(b)-k) {
		return nil, fi, ErrFormat
	}
	b = b[k:]
	if string(b[:n]) != id {
		return nil, fi, ErrWrongDocument
	}
	b = b[n:]
	if fi.version == 1 {
		return b, fi, nil
	}
	if len(b) < 1 {
		return nil, fi, ErrFormat
	}
	level, ok := levelString(b[0])
	if !ok {
		return nil, fi, ErrFormat
	}
	fi.b.Level = level
	b = b[1:]
	seq, k := binary.Uvarint(b)
	if k <= 0 || len(b)-k < sha256.Size {
		return nil, fi, ErrFormat
	}
	fi.b.Seq = seq
	b = b[k:]
	copy(fi.b.Hash[:], b[:sha256.Size])
	b = b[sha256.Size:]
	if fi.version == 2 {
		return b, fi, nil
	}
	if len(b) < sha256.Size+ed25519.SignatureSize {
		return nil, fi, ErrFormat
	}
	copy(fi.content[:], b[:sha256.Size])
	fi.sig = append([]byte(nil), b[sha256.Size:sha256.Size+ed25519.SignatureSize]...)
	return b[sha256.Size+ed25519.SignatureSize:], fi, nil
}

// ErrForgedFrame is returned when a file's frame is not what an owner signer wrote: a v3 frame
// whose signature is missing, is by none of the owners' signing keys, or does not match the
// content it came with (the header put on other content), or, with Adapter.RequireSigned, an
// unsigned frame. age gives no sender authentication, so a holder of a recipient's public key
// can mint a file that opens: this is what tells it from the owner's. It is an ErrDowngrade-class
// failure (errors.Is(err, docstore.ErrDowngrade)): nothing is read from the file or written
// over it.
var ErrForgedFrame error = forgedError{}

type forgedError struct{}

func (forgedError) Error() string {
	return "encrypt: the file's frame was not written by an owner of this document"
}

func (forgedError) Is(target error) bool { return target == docstore.ErrDowngrade }

func forged(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrForgedFrame, id, fmt.Sprintf(format, args...))
}

// ErrNoFrameSigner is returned by Save and the re-keys when a document would be sealed at the
// end-to-end level and the Adapter has no Signer: the frame of a confidential document must be
// signed (see ErrForgedFrame), so it is not saved unsigned.
var ErrNoFrameSigner = errors.New("encrypt: a confidential document is saved with the owner's signature: the Adapter has no Signer")

// crossCheck compares what a file says (f) with the record as read now (r). The rules, with
// Fs and Rs the two seqs:
//
//   - no record, but the file was sealed under record Fs > 0 or at the end-to-end level: the
//     record was deleted.
//   - Fs > Rs: the record is older than one the file was sealed under: a replay.
//   - Fs == Rs: the level and recipient hash must agree (a signed record cannot differ at one
//     seq; this is a corrupt or crafted state).
//   - Fs < Rs is normal (every change to the record, including clearing pending, raises it
//     while files are re-keyed in between), and is accepted, except, with strict (Load only,
//     where data would be returned under the wrong level), a file wrapped below the record's
//     level when the record is not pending: a server-level ciphertext put back into an
//     end-to-end document. While a switch is pending the stale files are the ones the
//     re-key has not reached yet. A file wrapped above the record's level (people only, in a
//     document the record calls server) is the safe direction and accepted.
//
// A v1 frame has nothing to compare and passes, except in a document whose record says every
// file is signed (Binding.Signed), where any unsigned frame is ErrForgedFrame (the owner's way out is
// Sharing.Repair with AcceptUnsigned).
func crossCheck(id string, f frameInfo, r Binding, strict bool) error {
	if r.Exists && r.Signed && f.version < 3 {
		return forged(id, "its frame is not signed (v%d), but the document's record says every file of it is: it was not written by an owner"+unsignedHint, f.version)
	}
	if f.version < 2 {
		return nil
	}
	fb := f.b
	switch {
	case !r.Exists:
		if fb.Seq > 0 || fb.Level == ProtectionE2EE {
			return downgrade(id, "the file was sealed under recipient record %d (%s) and that record is missing: it was deleted"+recoverHint, fb.Seq, levelName(fb.Level))
		}
	case fb.Seq > r.Seq:
		return downgrade(id, "the file was sealed under recipient record %d (%s) but the record is %d (%s): an older signed record was put back"+recoverHint, fb.Seq, levelName(fb.Level), r.Seq, levelName(r.Level))
	case fb.Seq == r.Seq:
		if fb.Level != r.Level || fb.Hash != r.Hash {
			return downgrade(id, "the file and the record disagree about level or recipients at record %d"+recoverHint, r.Seq)
		}
	case strict && rank(fb.Level) < rank(r.Level) && !r.Pending:
		return downgrade(id, "the file is wrapped at the %s level but the document is %s and no switch is in progress"+repairHint, levelName(fb.Level), levelName(r.Level))
	}
	return nil
}

func levelName(l string) string {
	if l == ProtectionE2EE {
		return "end-to-end"
	}
	return "server"
}

// frameVerifiers are the public keys a v3 frame may be signed by.
func (a *Adapter) frameVerifiers() []ed25519.PublicKey {
	out := append([]ed25519.PublicKey(nil), a.OwnerSigners...)
	if sk, ok := a.Ring.(signerKeys); ok {
		out = append(out, sk.ownerSignerKeys()...)
	}
	if a.Signer != nil {
		out = append(out, a.Signer.Public())
	}
	return out
}

// frameSigner is the public key, among the ones the Adapter knows (frameVerifiers), that signed
// the v3 frame fi, or nil when none did (or the frame is not v3). A frame carries no key id, so
// each listed key is tried.
func (a *Adapter) frameSigner(id string, fi frameInfo) ed25519.PublicKey {
	if fi.version != 3 {
		return nil
	}
	msg := frameSigMessage(id, levelByte(fi.b.Level), fi.b.Seq, fi.b.Hash, fi.content)
	for _, k := range a.frameVerifiers() {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, msg, fi.sig) {
			return k
		}
	}
	return nil
}

// verifyFrame checks the signature of a frame of a document that has a record (a Binder keyring):
// a v3 frame must verify against the owners' signing keys, and, when full is set (data is the
// whole content, not just what followed the header), carry the hash of that content; an
// unsigned frame is read with a warning logged once, unless RequireSigned. Anything else is
// ErrForgedFrame.
//
// acceptUnsigned (the owner's explicit Repair option, see AcceptUnsigned) lets an unsigned frame
// pass without the warning and whatever RequireSigned says, so it can be read and sealed again;
// it never excuses a v3 frame whose signature is bad.
func (a *Adapter) verifyFrame(id string, fi frameInfo, data []byte, full, acceptUnsigned bool) error {
	if !a.binds(id) || a.TrustFrames {
		return nil
	}
	if fi.version < 3 {
		if acceptUnsigned {
			return nil
		}
		if a.RequireSigned {
			return forged(id, "the frame is not signed (v%d) and this adapter requires signed frames", fi.version)
		}
		a.warnOnce.Do(func() {
			log.Printf("encrypt: %s has a frame without the owner's signature (v%d): anyone who knows a recipient's public key could have written it; saving or re-keying the document (Sharing.Repair) writes a signed one", id, fi.version)
		})
		return nil
	}
	if a.frameSigner(id, fi) == nil {
		return forged(id, "its signature is not by any owner signing key")
	}
	if full && sha256.Sum256(data) != fi.content {
		return forged(id, "its content is not the content the owner signed")
	}
	return nil
}

// peekFrame reads only the frame header of a stored file: age authenticates the first chunk
// of the stream before releasing it, which is all that is read here. The header's signature is
// verified (it covers the content hash), the content is not: what the header says is what an
// owner signed even if the file around it has been given other content, and the readers of the
// content (open) check the hash.
func (a *Adapter) peekFrame(ctx context.Context, id string, stored []byte) (frameInfo, error) {
	return a.peekFrameWith(ctx, id, stored, false)
}

func (a *Adapter) peekFrameWith(ctx context.Context, id string, stored []byte, acceptUnsigned bool) (frameInfo, error) {
	r, enc, err := a.decrypt(ctx, id, stored)
	if err != nil {
		return frameInfo{}, err
	}
	if !enc {
		return frameInfo{}, ErrNotEncrypted
	}
	buf := make([]byte, 1024)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return frameInfo{}, fmt.Errorf("encrypt: %w", err)
	}
	_, fi, err := unframe(id, buf[:n])
	if err == nil {
		err = a.verifyFrame(id, fi, nil, false, acceptUnsigned)
	}
	return fi, err
}

// FrameHeader is what a stored file's frame says, for a host that audits its store (the showcase's
// key-list anchor): the version, and from v2 the level, the record seq and the recipient-list hash
// the file was sealed under. Signed is set only for a v3 frame whose signature verifies against one
// of the keys the Adapter knows (OwnerSigners, Signer, the keyring's owner signers): a frame that
// anyone who knows a recipient's public key could have written is never Signed.
type FrameHeader struct {
	Version int
	Level   string // "" (server) or ProtectionE2EE
	Seq     uint64
	Hash    [sha256.Size]byte
	Signed  bool
}

// PeekFrame reads the frame header of a stored file of the document id: the age layer is opened
// with the keyring's identities (ErrNoKey when none opens it, ErrNotEncrypted for a file in the
// clear) and only the header is read. It checks nothing against a record.
func (a *Adapter) PeekFrame(ctx context.Context, id string, stored []byte) (FrameHeader, error) {
	r, enc, err := a.decrypt(ctx, id, stored)
	if err != nil {
		return FrameHeader{}, err
	}
	if !enc {
		return FrameHeader{}, ErrNotEncrypted
	}
	buf := make([]byte, 1024)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return FrameHeader{}, fmt.Errorf("encrypt: %w", err)
	}
	_, fi, err := unframe(id, buf[:n])
	if err != nil {
		return FrameHeader{}, err
	}
	h := FrameHeader{Version: fi.version, Level: fi.b.Level, Seq: fi.b.Seq, Hash: fi.b.Hash}
	if fi.version == 3 {
		h.Signed = a.frameSigner(id, fi) != nil
	}
	return h, nil
}
