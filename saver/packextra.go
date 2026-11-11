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

const DefaultAttachmentSize = 64 << 10

func blurTime(ns int64) int64 {
	if ns <= 0 {
		return ns
	}
	day := int64(24 * time.Hour)
	return ns - ns%day
}

// a field no reader knows: padding of a blurred record
const padField = 1000

func blurPlain(plain []byte) []byte {
	bucket := 1 << 10
	for bucket < len(plain)+3 {
		bucket <<= 1
	}
	// the field costs tag (2), length (1 to 4) and zeros
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
