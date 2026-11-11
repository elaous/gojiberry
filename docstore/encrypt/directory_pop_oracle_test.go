package encrypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

func sealTo(t *testing.T, to age.Recipient, plaintext string) docstore.DirectoryChallenge {
	t.Helper()
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return docstore.DirectoryChallenge{Token: "t", Encrypted: base64.StdEncoding.EncodeToString(enc.Bytes())}
}

func TestOpenChallengeIsNotADecryptionOracle(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("ab12", 8)
	if p, err := OpenChallenge(sealTo(t, id.Recipient(), nonce), id); err != nil || p.Nonce != nonce || p.Token != "t" {
		t.Fatalf("a real nonce: %+v %v", p, err)
	}
	for name, plain := range map[string]string{
		"a document":       "the secret contract text, long enough to be read back by a server that asked",
		"a short secret":   "hunter2",
		"empty":            "",
		"a longer nonce":   nonce + "00",
		"upper case":       strings.ToUpper(nonce),
		"not hex":          strings.Repeat("zz", 16),
		"a nonce and more": nonce + "\nsecret",
	} {
		p, err := OpenChallenge(sealTo(t, id.Recipient(), plain), id)
		if !errors.Is(err, ErrNotAChallenge) || p.Nonce != "" {
			t.Errorf("%s: answered %+v, %v", name, p, err)
		}
	}
}
