package docstorecmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func envMap(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestWithAuditReadsItsSwitchAndKey(t *testing.T) {
	srv := docstore.NewServer(docstore.NewMemoryAdapter(), "memory")
	var buf bytes.Buffer
	for _, off := range []string{"", "off", " OFF "} {
		if l, err := withAudit(srv, envMap("GOJIBERRY_DOCSTORE_AUDIT", off), os.ReadFile, &buf); l != nil || err != nil {
			t.Errorf("%q: %v %v", off, l, err)
		}
	}
	if l, err := withAudit(srv, envMap("GOJIBERRY_DOCSTORE_AUDIT", "on"), os.ReadFile, &buf); l == nil || err != nil {
		t.Errorf("on without a key: %v %v", l, err)
	}
	if l, err := withAudit(srv, envMap("GOJIBERRY_DOCSTORE_AUDIT", "on", "GOJIBERRY_DOCSTORE_AUDIT_KEY", "0123456789abcdef0123"), os.ReadFile, &buf); l == nil || err != nil {
		t.Errorf("on with a key: %v %v", l, err)
	}
	// The key may come from a file.
	read := func(p string) ([]byte, error) {
		if p == "/run/secrets/audit" {
			return []byte("0123456789abcdef0123\n"), nil
		}
		return nil, errors.New("no such file")
	}
	if l, err := withAudit(srv, envMap("GOJIBERRY_DOCSTORE_AUDIT", "on", "GOJIBERRY_DOCSTORE_AUDIT_KEY_FILE", "/run/secrets/audit"), read, &buf); l == nil || err != nil {
		t.Errorf("key file: %v %v", l, err)
	}
	for name, env := range map[string]func(string) string{
		"other value": envMap("GOJIBERRY_DOCSTORE_AUDIT", "maybe"),
		"short key":   envMap("GOJIBERRY_DOCSTORE_AUDIT", "on", "GOJIBERRY_DOCSTORE_AUDIT_KEY", "short-secret"),
		"both forms":  envMap("GOJIBERRY_DOCSTORE_AUDIT", "on", "GOJIBERRY_DOCSTORE_AUDIT_KEY", "0123456789abcdef0123", "GOJIBERRY_DOCSTORE_AUDIT_KEY_FILE", "/x"),
	} {
		l, err := withAudit(srv, env, read, &buf)
		if err == nil || l != nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "short-secret") || strings.Contains(err.Error(), "0123456789") {
			t.Errorf("%s: the error repeats the secret: %v", name, err)
		}
	}
}
