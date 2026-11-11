package export

import (
	"archive/zip"
	"hash/crc32"
	"io"
	"time"
)

var zipTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

func zipEntry(zw *zip.Writer, name string, method uint16) (io.Writer, error) {
	return zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: zipTime})
}

func zipMimetype(zw *zip.Writer, mime string) error {
	fh := &zip.FileHeader{
		Name: "mimetype", Method: zip.Store, Modified: zipTime,
		CRC32: crc32.ChecksumIEEE([]byte(mime)), CompressedSize64: uint64(len(mime)), UncompressedSize64: uint64(len(mime)),
	}
	fh.ModifiedDate, fh.ModifiedTime = 1<<5|1, 0 //nolint:staticcheck // the only way to date a raw entry
	w, err := zw.CreateRaw(fh)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, mime)
	return err
}
