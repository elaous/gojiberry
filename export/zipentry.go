package export

import (
	"archive/zip"
	"hash/crc32"
	"io"
	"time"
)

// zipTime is the time every entry of a file the exporters write carries: the same on every export, so the
// same document gives the same bytes. (The zero time is written as a date with month 0 and day 0, which
// some readers refuse: LibreOffice would not open the ODT.)
var zipTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// zipEntry starts an entry of the archive.
func zipEntry(zw *zip.Writer, name string, method uint16) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: zipTime})
}

// zipMimetype writes the first entry of an ODF or EPUB package: stored, with its size and checksum in the
// header (no data descriptor after it), so that a reader that identifies the type from the first bytes of the
// file can (LibreOffice refuses the package otherwise).
func zipMimetype(zw *zip.Writer, mime string) error {
	fh := &zip.FileHeader{
		Name: "mimetype", Method: zip.Store, Modified: zipTime,
		CRC32: crc32.ChecksumIEEE([]byte(mime)), CompressedSize64: uint64(len(mime)), UncompressedSize64: uint64(len(mime)),
	}
	// CreateRaw writes the DOS date and time fields as they are (Modified is not looked at): 1980-01-01 00:00.
	fh.ModifiedDate, fh.ModifiedTime = 1<<5|1, 0 //nolint:staticcheck // the only way to date a raw entry
	w, err := zw.CreateRaw(fh)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, mime)
	return err
}
