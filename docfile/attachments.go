package docfile

import "strings"

const AttachmentScheme = "opendoc-att:"

// AttachmentURL is the reference to an attachment.
func AttachmentURL(hexSum string) string { return AttachmentScheme + hexSum }

// AttachmentHash returns the hash in an attachment reference.
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

// MapImages returns a copy of the document with images changed.
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
