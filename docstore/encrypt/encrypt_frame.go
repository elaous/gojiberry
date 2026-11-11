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

// The frame is the plaintext inside the encryption layer.
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

// ErrDowngrade is returned, wrapped, when a frame would downgrade.
var ErrDowngrade = docstore.ErrDowngrade

func downgrade(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrDowngrade, id, fmt.Sprintf(format, args...))
}

// Hints appended to findings say what to do.
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

type frameInfo struct {
	version int
	b       Binding
	content [sha256.Size]byte
	sig     []byte
}

func frameSigMessage(id string, level byte, seq uint64, recipients, content [sha256.Size]byte) []byte {
	out := append([]byte(frameSigContext), 0)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	out = append(out, level)
	out = binary.AppendUvarint(out, seq)
	out = append(out, recipients[:]...)
	return append(out, content[:]...)
}

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

// ErrForgedFrame is returned when a file's frame is forged.
var ErrForgedFrame error = forgedError{}

type forgedError struct{}

func (forgedError) Error() string {
	return "encrypt: the file's frame was not written by an owner of this document"
}

func (forgedError) Is(target error) bool { return target == docstore.ErrDowngrade }

func forged(id, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrForgedFrame, id, fmt.Sprintf(format, args...))
}

// ErrNoFrameSigner is returned by Save when no signer is set.
var ErrNoFrameSigner = errors.New("encrypt: a confidential document is saved with the owner's signature: the Adapter has no Signer")

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

// FrameHeader is what a stored file's frame says.
type FrameHeader struct {
	Version int
	Level   string // "" (server) or ProtectionE2EE
	Seq     uint64
	Hash    [sha256.Size]byte
	Signed  bool
}

// PeekFrame reads the frame header of a stored file.
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
