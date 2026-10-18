package docfile

import "strings"

// AttachmentScheme starts the URL of an image kept apart in a version 2 pack (docfile/pack): the SHA-256 of its bytes, in
// hexadecimal, follows it. Such a URL is only ever in a document inside a pack; it is replaced by the data: URL when the
// pack is imported.
const AttachmentScheme = "opendoc-att:"

// AttachmentURL is the reference to the attachment with the given hash (64 hexadecimal characters).
func AttachmentURL(hexSum string) string { return AttachmentScheme + hexSum }

// AttachmentHash returns the hash in a reference made by AttachmentURL.
func AttachmentHash(url string) (string, bool) {
	h, ok := strings.CutPrefix(url, AttachmentScheme)
	if !ok || len(h) != 64 {
		return "", false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return h, true
}

// MapImages returns a copy of the document in which each image's URL is replaced by what fn returns for it, when fn says
// to (the second result), in the earlier versions of the document too. The document is not changed.
func (d Document) MapImages(fn func(url string) (string, bool)) Document {
	content := make([]Item, len(d.Content))
	copy(content, d.Content)
	for i, it := range content {
		if it.Image == nil {
			continue
		}
		if u, ok := fn(it.Image.URL); ok {
			img := *it.Image
			img.URL = u
			content[i].Image = &img
		}
	}
	d.Content = content
	if len(d.History) > 0 {
		hist := make([]Revision, len(d.History))
		copy(hist, d.History)
		for i, r := range hist {
			if r.Doc != nil {
				m := r.Doc.MapImages(fn)
				hist[i].Doc = &m
			}
		}
		d.History = hist
	}
	return d
}
