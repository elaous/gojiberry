package encrypt

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// Recipient is a key a document is encrypted to.
type Recipient struct {
	Label       string
	Type        string // "ssh-ed25519", "ssh-rsa" or "age-x25519"
	Fingerprint string
	// "SHA256:..." for an SSH key, the key itself for an age key
	Key age.Recipient
	// Text is the public key as it was read.
	Text string
	// Hint is, for a passkey key, the credential ID.
	Hint string
}

// ParseSSHRecipient reads an SSH public key as a recipient.
func ParseSSHRecipient(line string) (Recipient, error) {
	line = strings.TrimSpace(line)
	if strings.ContainsAny(line, "\r\n") {
		return Recipient{}, errors.New("encrypt: an SSH public key is one line")
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return Recipient{}, fmt.Errorf("encrypt: not an SSH public key: %w", err)
	}
	r, err := agessh.ParseRecipient(line)
	if err != nil {
		return Recipient{}, fmt.Errorf("encrypt: %w", err)
	}
	fp := ssh.FingerprintSHA256(pub)
	return Recipient{Label: comment, Type: pub.Type(), Fingerprint: fp, Key: sshKeyed{Recipient: r, fp: fp}, Text: line}, nil
}

type sshKeyed struct {
	age.Recipient
	fp string
}

func (s sshKeyed) keyFingerprint() string { return s.fp }

type keyFingerprinter interface{ keyFingerprint() string }

// ParseAgeRecipient reads an age public key ("age1...").
func ParseAgeRecipient(s string) (Recipient, error) {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, "\r\n") {
		return Recipient{}, errors.New("encrypt: an age public key is one line")
	}
	r, err := age.ParseX25519Recipient(s)
	if err != nil {
		return Recipient{}, fmt.Errorf("encrypt: %w", err)
	}
	sum := sha256.Sum256([]byte(r.String()))
	return Recipient{Type: "age-x25519", Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), Key: r, Text: r.String()}, nil
}

// ParseSSHIdentity reads an unencrypted SSH private key.
func ParseSSHIdentity(pem []byte) (age.Identity, error) {
	id, err := agessh.ParseIdentity(pem)
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, errors.New("encrypt: the SSH key is protected by a passphrase")
		}
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return id, nil
}

// GenerateRecoveryKey makes an age key pair for recovery.
func GenerateRecoveryKey() (public string, secret string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.Recipient().String(), id.String(), nil
}

// ParseAgeIdentity reads an age secret key ("AGE-SECRET-KEY-1...").
func ParseAgeIdentity(s string) (age.Identity, error) {
	id, err := age.ParseX25519Identity(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return id, nil
}
