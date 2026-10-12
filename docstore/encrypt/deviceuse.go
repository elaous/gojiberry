package encrypt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docstore"
)

// DeviceUse says when a key of the store last unlocked it, and from which device, as that device named
// itself (a label the person gave it, such as "work laptop"; "" when none). It is kept in the store, so
// every device sees the others' use: the keys list can say "last used on the phone, 3 days ago", which
// helps tell a key that is still in use from one that can be removed. It is kept for that and nothing
// else, and it is encrypted to the store's keys like a document, so the server learns only that a key
// has a record and when the record was written.
type DeviceUse struct {
	Fingerprint string    `json:"fp"`
	Device      string    `json:"device,omitempty"`
	At          time.Time `json:"at"`
}

// MaxDeviceLabel is the longest device label kept, in characters.
const MaxDeviceLabel = 60

// deviceUseID is the file of one key's record: named by a hash of the fingerprint, so whatever the
// fingerprint holds (colons, a long text) never becomes part of a file name.
func deviceUseID(fp string) (string, error) {
	if fp == "" || len(fp) > 256 || strings.TrimSpace(fp) != fp || strings.ContainsFunc(fp, unicode.IsControl) {
		return "", errors.New("encrypt: not a key fingerprint")
	}
	sum := sha256.Sum256([]byte(fp))
	return docstore.DeviceUsePrefix + hex.EncodeToString(sum[:16]), nil
}

// RecordDeviceUse writes that the key fp unlocked the store now, from the device named device (cut to
// MaxDeviceLabel characters, control characters dropped). The file is written through the adapter, so
// it is encrypted to the store's keys; the record of one key is replaced, never appended to.
func (a *Adapter) RecordDeviceUse(ctx context.Context, fp, device string, now time.Time) error {
	id, err := deviceUseID(fp)
	if err != nil {
		return err
	}
	data, err := json.Marshal(DeviceUse{Fingerprint: fp, Device: cleanDeviceLabel(device), At: now.UTC().Truncate(time.Second)})
	if err != nil {
		return err
	}
	return a.Save(ctx, id, data)
}

// DeviceUses reads every key's record. A record that cannot be opened or read is left out (it may be
// from a key that was removed and re-encrypted away); the list is by key, in no order.
func (a *Adapter) DeviceUses(ctx context.Context) ([]DeviceUse, error) {
	metas, err := a.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []DeviceUse
	for _, m := range metas {
		if !strings.HasPrefix(m.ID, docstore.DeviceUsePrefix) {
			continue
		}
		d, err := a.Load(ctx, m.ID)
		if err != nil {
			continue
		}
		var u DeviceUse
		if json.Unmarshal(d.Data, &u) != nil || !deviceUseNamed(u.Fingerprint, m.ID) {
			continue // not what this file is named for: ignore it
		}
		u.Device = cleanDeviceLabel(u.Device)
		out = append(out, u)
	}
	return out, nil
}

// deviceUseNamed says whether a file is the record of the key it claims to be for.
func deviceUseNamed(fp, id string) bool {
	want, err := deviceUseID(fp)
	return err == nil && want == id
}

// cleanDeviceLabel keeps a device label to printable text of at most MaxDeviceLabel characters.
func cleanDeviceLabel(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < ' ' || r == 0x7f || r == utf8.RuneError {
			continue
		}
		if n++; n > MaxDeviceLabel {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
