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

// Recipient is a key a document is encrypted to, as the people lists show it: a label (an
// e-mail or a name; it says who the key is meant for, and is not what makes it trusted) and a
// fingerprint, which is what the owner confirms when adding it.
type Recipient struct {
	Label       string
	Type        string // "ssh-ed25519", "ssh-rsa" or "age-x25519"
	Fingerprint string // "SHA256:..." for an SSH key, the key itself for an age key
	Key         age.Recipient
	// Text is the public key as it was read ("ssh-ed25519 AAAA..." or "age1..."), what is kept
	// in a document's recipient list.
	Text string
}

// ParseSSHRecipient reads an SSH public key as an authorized_keys or .pub file holds it
// ("ssh-ed25519 AAAA... label"); the comment becomes the label. Only ssh-ed25519 and ssh-rsa
// are supported (the keys age can encrypt to).
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
	return Recipient{Label: comment, Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub), Key: r, Text: line}, nil
}

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

// ParseSSHIdentity reads an unencrypted SSH private key (the PEM of an ed25519 or RSA key).
// A key protected by a passphrase must be opened by the caller first.
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

// GenerateRecoveryKey makes an age key pair for a recovery recipient: show the secret once,
// to be kept offline; the public part goes in the keyring.
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
