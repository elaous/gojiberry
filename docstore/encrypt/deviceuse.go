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

type DeviceUse struct {
	Fingerprint string    `json:"fp"`
	Device      string    `json:"device,omitempty"`
	At          time.Time `json:"at"`
}

// MaxDeviceLabel is the longest device label kept.
const MaxDeviceLabel = 60

func deviceUseID(fp string) (string, error) {
	if fp == "" || len(fp) > 256 || strings.TrimSpace(fp) != fp || strings.ContainsFunc(fp, unicode.IsControl) {
		return "", errors.New("encrypt: not a key fingerprint")
	}
	sum := sha256.Sum256([]byte(fp))
	return docstore.DeviceUsePrefix + hex.EncodeToString(sum[:16]), nil
}

// RecordDeviceUse writes that a key unlocked the store.
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

// DeviceUses reads every key's record.
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

func deviceUseNamed(fp, id string) bool {
	want, err := deviceUseID(fp)
	return err == nil && want == id
}

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
