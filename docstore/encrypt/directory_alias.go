package encrypt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

const aliasPrefix = docstore.DirectoryLogPrefix + "alias/"

const aliasDomain = "gojiberry-directory-alias|"

type aliasRecord struct {
	Current  string   `json:"current"`
	Previous []string `json:"previous"`
	Kid      string   `json:"kid"`
	Sig      string   `json:"sig"`
}

func aliasMessage(current string, previous []string) []byte {
	return []byte(aliasDomain + current + "|" + strings.Join(previous, ","))
}

func (d *AdapterDirectory) loadAliases(ctx context.Context) error {
	if d.Signer == nil {
		return nil
	}
	metas, err := docstore.ListPrefix(ctx, d.Store, aliasPrefix)
	if err != nil {
		return err
	}
	loaded := map[string][]string{}
	ignored := 0
	for _, m := range metas {
		doc, err := d.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var rec aliasRecord
		if err := json.Unmarshal(doc.Data, &rec); err != nil || rec.Current == "" {
			log.Printf("encrypt: ignoring an unreadable directory alias record %s", m.ID)
			continue
		}
		sig, _ := base64.StdEncoding.DecodeString(rec.Sig)
		if m.ID != aliasPrefix+rec.Current || !d.Signer.Verify(rec.Kid, aliasMessage(rec.Current, rec.Previous), sig) {
			ignored++
			continue
		}
		loaded[rec.Current] = rec.Previous
	}
	if ignored > 0 {
		log.Printf("encrypt: %d directory alias records were ignored: signed by a key that is no longer listed, or altered", ignored)
	}
	d.aliasMu.Lock()
	d.aliases = loaded
	d.aliasMu.Unlock()
	return nil
}

// an alias is never dropped
func (d *AdapterDirectory) recordAliases(ctx context.Context) error {
	if d.Signer == nil {
		return nil
	}
	emails, err := d.knownEmails(ctx)
	if err != nil {
		return err
	}
	for _, email := range emails {
		all := d.Signer.Pseudonyms(logAddressDomain + normalizeEmail(email))
		if len(all) < 2 {
			continue
		}
		cur := all[0]
		d.aliasMu.RLock()
		have := slices.Clone(d.aliases[cur])
		d.aliasMu.RUnlock()
		merged := slices.Clone(have)
		d.aliasMu.RLock()
		var carried []string
		for _, p := range all[1:] {
			carried = append(carried, p)
			carried = append(carried, d.aliases[p]...)
		}
		d.aliasMu.RUnlock()
		for _, a := range carried {
			if !slices.Contains(merged, a) {
				merged = append(merged, a)
			}
		}
		if len(merged) == len(have) {
			continue
		}
		slices.Sort(merged)
		rec := aliasRecord{Current: cur, Previous: merged, Kid: d.Signer.KeyID()}
		rec.Sig = base64.StdEncoding.EncodeToString(d.Signer.Sign(aliasMessage(cur, merged)))
		data, _ := json.Marshal(rec)
		if err := d.Store.Save(ctx, aliasPrefix+cur, data); err != nil {
			return err
		}
		d.aliasMu.Lock()
		if d.aliases == nil {
			d.aliases = map[string][]string{}
		}
		d.aliases[cur] = merged
		d.aliasMu.Unlock()
	}
	return nil
}
