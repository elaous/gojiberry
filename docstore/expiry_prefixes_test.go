package docstore_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/docstore/public"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/saver"
)

// Every keyspace constant the program exports must be refused by IsDocumentKey, so the expiry
// sweeper can never adopt it.
func TestSystemPrefixesCoverExportedConstants(t *testing.T) {
	keys := map[string]string{
		"docstore.ExpiryPrefix":        docstore.ExpiryPrefix + "x",
		"docstore.DirectoryPrefix":     docstore.DirectoryPrefix + "x",
		"docstore.DirectoryLogPrefix":  docstore.DirectoryLogPrefix + "e/1",
		"docstore.RateLimitPrefix":     docstore.RateLimitPrefix + "ip/abc",
		"docstore.FolderSecretPrefix":  docstore.FolderSecretPrefix + "x",
		"docstore.PublicPrefix":        docstore.PublicPrefix + "doc/x",
		"docstore.ReportPrefix":        docstore.ReportPrefix + "x",
		"public.TombstonePrefix":       public.TombstonePrefix + "x",
		"docstore.UserCatalogPrefix":   docstore.UserCatalogPrefix("u") + "index",
		"encrypt.KeysPrefix":           encrypt.KeysPrefix + "doc",
		"fonts.KeyPrefix":              fonts.KeyPrefix + "Inter/400.ttf",
		"saver.FontPrefix":             saver.FontPrefix + "x",
		"saver.VersionPrefix":          saver.VersionPrefix + "doc/1.open",
		"saver.FolderRecordPrefix":     saver.FolderRecordPrefix + "f",
		"saver.UserCatalogKey":         saver.UserCatalogKey("u"),
		"hub.StoreKey":                 hub.StoreKey("doc"),
		"the acl record of a document": "acl:doc",
		"the shared catalog":           "catalog:index",
		"passkey accounts and handles": "passkey/account/x",
	}
	for name, k := range keys {
		if docstore.IsDocumentKey(k) {
			t.Errorf("%s: %q counts as a document", name, k)
		}
	}
}

// irrelevantPrefixConstants are string constants named like a prefix that are not store keys.
var irrelevantPrefixConstants = map[string]string{
	"cachePrefix":          "service-worker cache name",
	"LWWKindPrefix":        "CRDT op kind",
	"accountPrefix":        "first part of a passkey user ID (\"passkey:...\"), not a key",
	"passkeySessionPrefix": "user ID prefix, not a key",
	"docKeyPrefix":         "where docstore/public keeps public documents: ordinary expiring documents, hidden from /docs by Server.Hide instead",
}

var prefixConst = regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w*[Pp]refix\w*)\s*=\s*"([^"]*)"`)

// TestSystemPrefixesCoverEveryPrefixConstant scans the repository for string constants named
// "...Prefix" whose value ends in a keyspace separator (":" or "/"): each is either a keyspace IsDocumentKey rejects, or is listed above as not one.
// A new keyspace added anywhere fails this until it is added to systemPrefixes (docstore/expiry.go).
func TestSystemPrefixesCoverEveryPrefixConstant(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || n == "vendor" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range prefixConst.FindAllStringSubmatch(string(b), -1) {
			name, val := m[1], m[2]
			found++
			if _, skip := irrelevantPrefixConstants[name]; skip {
				continue
			}
			if (strings.HasSuffix(val, ":") || strings.HasSuffix(val, "/")) && docstore.IsDocumentKey(val+"x") {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s: %s = %q is a keyspace that IsDocumentKey accepts as a document: add it to systemPrefixes in docstore/expiry.go (or to irrelevantPrefixConstants if it is not a store key)", rel, name, val)
			}
		}
		return nil
	})
	if found < 8 {
		t.Fatalf("found only %d prefix constants: the scan is broken", found)
	}
}
