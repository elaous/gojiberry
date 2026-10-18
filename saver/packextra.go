package saver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

// DefaultAttachmentSize is the size from which PackOptions.Attachments keeps an image apart.
const DefaultAttachmentSize = 64 << 10

// blurTime rounds a time (unix nanoseconds) down to the day, UTC; a zero time stays zero.
func blurTime(ns int64) int64 {
	if ns <= 0 {
		return ns
	}
	day := int64(24 * time.Hour)
	return ns - ns%day
}

// padField is the number of a field no reader knows (the document model stops far below it): the padding of a blurred
// record is one field of zero bytes, which every reader skips.
const padField = 1000

// blurPlain pads an .open message with a skipped field so that its length is a bucket: 1 KiB, doubling.
func blurPlain(plain []byte) []byte {
	bucket := 1 << 10
	for bucket < len(plain)+3 {
		bucket <<= 1
	}
	// The field costs its tag (2 bytes), its length (1 to 4 bytes) and the zeros.
	for lenBytes := 1; lenBytes <= 4; lenBytes++ {
		n := bucket - len(plain) - 2 - lenBytes
		if n >= 0 && varintLen(uint64(n)) == lenBytes {
			out := append(make([]byte, 0, bucket), plain...)
			out = appendVarint(out, padField<<3|2)
			out = appendVarint(out, uint64(n))
			return append(out, make([]byte, n)...)
		}
	}
	return plain
}

func varintLen(v uint64) int {
	n := 1
	for ; v >= 0x80; v >>= 7 {
		n++
	}
	return n
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// dataImage splits a base64 data: URL into its type and bytes.
func dataImage(url string) (mimeType string, data []byte, ok bool) {
	rest, found := strings.CutPrefix(url, "data:")
	if !found {
		return "", nil, false
	}
	head, body, found := strings.Cut(rest, ",")
	if !found {
		return "", nil, false
	}
	head, b64 := strings.CutSuffix(head, ";base64")
	if !b64 {
		return "", nil, false
	}
	mt, _, err := mime.ParseMediaType(head)
	if err != nil || !strings.HasPrefix(mt, "image/") {
		return "", nil, false
	}
	data, err = base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", nil, false
	}
	return mt, data, true
}

// anyLargeImage says whether any of the documents has an image the export would keep apart.
func (s *Saver) anyLargeImage(ctx context.Context, target string, ids []string, o PackOptions) (bool, error) {
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		doc, _, err := s.loadWithHistory(ctx, target, id, o.History)
		if err != nil {
			return false, nil // the export itself reports it, with the document's name
		}
		found := false
		doc.MapImages(func(u string) (string, bool) {
			if _, data, ok := dataImage(u); ok && len(data) >= o.Attachments {
				found = true
			}
			return u, false
		})
		if found {
			return true, nil
		}
	}
	return false, nil
}

// apartImages stores the document's large data: images in the pack as attachments and refers to them.
func apartImages(pw *pack.Writer, doc docfile.Document, min int) (docfile.Document, error) {
	var firstErr error
	out := doc.MapImages(func(u string) (string, bool) {
		mt, data, ok := dataImage(u)
		if !ok || len(data) < min || firstErr != nil {
			return u, false
		}
		sum, err := pw.AddAttachment(mt, data)
		if err != nil {
			firstErr = err
			return u, false
		}
		return docfile.AttachmentURL(hex.EncodeToString(sum[:])), true
	})
	return out, firstErr
}

// resolveImages puts back the data: URL of each attachment a document refers to, and counts the ones the pack lacks.
func resolveImages(rd *pack.Reader, doc docfile.Document) (docfile.Document, int) {
	missing := 0
	out := doc.MapImages(func(u string) (string, bool) {
		h, ok := docfile.AttachmentHash(u)
		if !ok {
			return u, false
		}
		raw, _ := hex.DecodeString(h)
		var sum [32]byte
		copy(sum[:], raw)
		data, mt, err := rd.Attachment(sum)
		if err != nil || sha256.Sum256(data) != sum {
			missing++
			return u, false
		}
		return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(data), true
	})
	return out, missing
}
