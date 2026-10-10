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

// Aliases keep the entries of the log matchable to their address after a directory key is removed.
//
// A log entry names its address by a pseudonym keyed by the directory key that made it, so the
// pseudonym of an address changes when the key rotates. While the old key is listed
// (GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV) the directory can compute the old pseudonym of every
// address it knows; once the key is removed it cannot, and the old entries could no longer be tied
// to the address (their records would have to be republished, and an owner could not read them).
// So that nothing depends on remembering to wait: at every start-up, while the retired keys are
// still listed, the directory records for each address it knows an alias record, signed by the
// current key, from the address's current pseudonym to the older ones. After the key is removed,
// LogAddressesFor still returns them. An address the store holds no record or key for cannot be
// aliased (nothing names it), and stays *unmapped* as before.
//
// The record holds pseudonyms only, never an e-mail address.

const aliasPrefix = docstore.DirectoryLogPrefix + "alias/"

const aliasDomain = "gojiberry-directory-alias|"

// aliasRecord is the stored form: the current pseudonym, the older ones, and the key that signed it.
type aliasRecord struct {
	Current  string   `json:"current"`
	Previous []string `json:"previous"`
	Kid      string   `json:"kid"`
	Sig      string   `json:"sig"`
}

func aliasMessage(current string, previous []string) []byte {
	return []byte(aliasDomain + current + "|" + strings.Join(previous, ","))
}

// loadAliases reads the alias records of the store. A record that does not verify (a key this
// directory does not have, or one somebody with store access wrote) is ignored and logged.
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
		// Expected after a rotation: the record of an earlier current key, whose signing key is gone
		// (its aliases were carried into the next record while it was listed).
		log.Printf("encrypt: %d directory alias records were ignored: signed by a key that is no longer listed, or altered", ignored)
	}
	d.aliasMu.Lock()
	d.aliases = loaded
	d.aliasMu.Unlock()
	return nil
}

// recordAliases records, for every address the store knows, its pseudonyms under the retired keys
// still listed, merged with what is recorded (an alias is never dropped).
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
		// The retired keys still listed, and what was recorded for them while they were current:
		// the pseudonyms under keys removed since, carried into the record of the new current key.
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
