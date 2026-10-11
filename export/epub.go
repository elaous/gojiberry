package export

import (
	"archive/zip"
	"bytes"
	"time"
)

// EPUB renders a Snapshot to a minimal, valid EPUB3 package -- the
// first genuinely binary format: an EPUB file is a zip archive with a
// required internal structure (the OCF container plus the OPF package
// format), not plain text the way Markdown/PlainText/HTML/RTF all
// are. It still needs no third-party dependency, though -- EPUB's own
// content is just a handful of small XML files plus one XHTML
// chapter, and archive/zip already covers the container itself. The
// document becomes a single-chapter book: nav.xhtml (the
// EPUB3-required table of contents) links to content.xhtml, which
// reuses HTML's own renderHTMLDocument (xhtml=true) rather than a
// second parallel block-walking loop.
type EPUB struct{}

func (EPUB) Export(snap Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype must be the archive's first entry, stored uncompressed
	// (zip.Store) -- EPUB readers identify the container by reading
	// these exact bytes at a fixed offset, before parsing anything
	// else as XML, so this can't just be Create()'d like the other
	// entries (which zip.Writer would otherwise deflate by default).
	mimetypeWriter, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, err
	}
	if _, err := mimetypeWriter.Write([]byte("application/epub+zip")); err != nil {
		return nil, err
	}

	files := []struct{ name, content string }{
		{"META-INF/container.xml", epubContainerXML},
		{"OEBPS/content.opf", epubContentOPF(snap)},
		{"OEBPS/nav.xhtml", epubNavXHTML(snap.Title, snap.Lang)},
		{"OEBPS/content.xhtml", renderHTMLDocument(snap, true)},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.content)); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// epubContainerXML is OCF's own fixed entry point: every EPUB reader
// opens this first to find the real package document (content.opf) --
// its content never depends on the document being exported, unlike
// every other EPUB file here.
const epubContainerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// epubContentOPF renders the OPF package document: the metadata
// (identifier/title/language/modified -- all four required by the
// EPUB3 spec) plus the manifest and spine, EPUB's own name for "which
// files exist" and "in what reading order." snap.ID (document.replica
// -- already a unique-per-document-instance identity) doubles as
// dc:identifier's value; it doesn't need to stay stable across
// repeated exports of the same document the way a library catalog's
// own identifier might, since each export is simply a point-in-time
// snapshot, not a synced or re-imported EPUB.
func epubContentOPF(snap Snapshot) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">urn:gojiberry:` + escapeHTMLText(snap.ID) + `</dc:identifier>
    <dc:title>` + escapeHTMLText(snap.Title) + `</dc:title>
    <dc:language>` + epubLanguage(snap.Lang) + `</dc:language>
    <meta property="dcterms:modified">` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="content" href="content.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="content"/>
  </spine>
</package>
`
}

// epubNavXHTML renders EPUB3's required nav document -- the table of
// contents every reader's own UI is built from. A single-chapter book
// only ever needs a single entry, pointing at content.xhtml.
func epubNavXHTML(title, lang string) string {
	escaped := escapeHTMLText(title)
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"` + langAttrs(lang, true) + `>
<head><title>` + escaped + `</title></head>
<body>
<nav epub:type="toc" id="toc">
<h1>` + escaped + `</h1>
<ol>
<li><a href="content.xhtml">` + escaped + `</a></li>
</ol>
</nav>
</body>
</html>
`
}

// epubLanguage is the dc:language of the package: the document's, else English as before.
func epubLanguage(lang string) string {
	if lang == "" {
		return "en"
	}
	return escapeHTMLText(lang)
}
