package fontserver

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A name record whose offset+length passes 65535 must be skipped, not
// wrapped around by uint16 arithmetic into a slice-bounds panic.
func TestParseNameTableOffsetOverflow(t *testing.T) {
	const recSize = 12
	storage := make([]byte, 70000)
	table := make([]byte, 6+recSize+len(storage))
	binary.BigEndian.PutUint16(table[2:4], 1)                 // one record
	binary.BigEndian.PutUint16(table[4:6], uint16(6+recSize)) // string storage
	rec := table[6 : 6+recSize]
	binary.BigEndian.PutUint16(rec[0:2], 3)     // platform
	binary.BigEndian.PutUint16(rec[2:4], 1)     // encoding
	binary.BigEndian.PutUint16(rec[4:6], 0x409) // language
	binary.BigEndian.PutUint16(rec[6:8], 1)     // name id
	binary.BigEndian.PutUint16(rec[8:10], 2000) // length
	binary.BigEndian.PutUint16(rec[10:12], 65000)
	if _, err := parseNameTable(table); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleFetchErrorDoesNotLeakTheAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused
	_, err := fetchGoogleFontsFrom(srv.URL+"/webfonts?key=SECRETKEY", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("the error carries the API key: %v", err)
	}
	var _ = errors.Is
}
