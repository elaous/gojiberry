package fontserver

import (
	"encoding/binary"
	"errors"
	"github.com/elaous/gojiberry/guard"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestGuardTokenProtectsDownloads(t *testing.T) {
	srv := NewServer(Config{FontsDir: t.TempDir(), Guard: guard.Token("tok", guard.Options{Public: []string{"/health"}})})
	h := srv.Handler()
	do := func(method, path, auth string) int {
		req := httptest.NewRequest(method, path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if do("POST", "/local/download?family=Roboto", "") != http.StatusUnauthorized || do("POST", "/local/rescan", "Bearer wrong") != http.StatusUnauthorized {
		t.Fatal("unauthenticated callers must be refused")
	}
	if do("GET", "/health", "") != http.StatusOK || do("POST", "/local/rescan", "Bearer tok") != http.StatusOK {
		t.Fatal("health is public and the token opens the rest")
	}
}
