package export

import (
	"archive/zip"
	"bytes"
	"time"
)

// EPUB renders a Snapshot to a minimal EPUB3 package.
type EPUB struct{}

func (EPUB) Export(snap Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype first entry, stored uncompressed (reader reads fixed offset)
	if err := zipMimetype(zw, "application/epub+zip"); err != nil {
		return nil, err
	}

	files := []struct{ name, content string }{
		{"META-INF/container.xml", epubContainerXML},
		{"OEBPS/content.opf", epubContentOPF(snap)},
		{"OEBPS/nav.xhtml", epubNavXHTML(snap.Title, snap.Lang)},
		{"OEBPS/content.xhtml", renderHTMLDocument(snap, true, safeSnapTheme(snap))},
	}
	for _, f := range files {
		w, err := zipEntry(zw, f.name, zip.Deflate)
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

const epubContainerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

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

func epubLanguage(lang string) string {
	if lang == "" {
		return "en"
	}
	return escapeHTMLText(lang)
}
