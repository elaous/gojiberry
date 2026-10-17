// Package addons is the neutral side of the page's add-on registry (window.addons, see docs/addons.md): the names of
// the add-ons the page knows, the method each must offer before the page counts it as present, and the shape of the
// models an add-on may keep. The registry itself is JavaScript and lives in the page; this package holds what is
// decided without a browser, so it can be tested.
package addons

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Spec names an add-on and what it has to offer.
type Spec struct {
	// Name is the property of window.addons.
	Name string
	// Method is the method the page needs: an add-on without it counts as absent.
	Method string
	// Purpose is a sentence in English about what it is for.
	Purpose string
	// Models says the add-on keeps models that are downloaded on request (it offers models, install and remove).
	Models bool
}

// Known lists the add-ons the page has a use for, in the order a list shows them.
var Known = []Spec{
	{Name: "piper", Method: "synthesize", Purpose: "Reader voices that run in the page", Models: false},
	{Name: "whisper", Method: "transcribe", Purpose: "Dictation on this device", Models: true},
	{Name: "describe", Method: "describe", Purpose: "A description of what a picture shows, drafted on this device", Models: true},
	{Name: "ocr", Method: "recognize", Purpose: "Text from scanned pages and pictures", Models: true},
	{Name: "translate", Method: "translate", Purpose: "Translation on this device", Models: true},
	{Name: "llm", Method: "generate", Purpose: "Rewrite, summarise and explain, on this device", Models: true},
	{Name: "diagram", Method: "render", Purpose: "Flowcharts and maps drawn from a description", Models: false},
	{Name: "spell", Method: "check", Purpose: "Spelling dictionaries that work offline", Models: true},
}

// Find is the Spec of a name.
func Find(name string) (Spec, bool) {
	for _, s := range Known {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// Model is something an add-on keeps and the person may download or remove: a speech model, a dictionary, a language
// pair.
type Model struct {
	// ID is what install and remove are told; Label is how a person is told about it.
	ID, Label string
	// Lang is the BCP 47 tag it serves, "" for any (or none).
	Lang string
	// Size is what installing downloads, in bytes (0 when not known).
	Size int64
	// Licence is where its terms are written: an https address or a short name ("MIT"); "" when not said.
	Licence string
	// Installed says it is on the device.
	Installed bool
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,119}$`)

// ParseModels reads what an add-on's models() returned, as JSON: [{"id","label","lang","size","licence","installed"}].
// A model without a usable id is left out; a label defaults to the id; a licence that looks like an address must be
// https (anything else is shown as text, never as a link).
func ParseModels(data []byte) ([]Model, error) {
	var raw []struct {
		ID        string  `json:"id"`
		Label     string  `json:"label"`
		Lang      string  `json:"lang"`
		Size      float64 `json:"size"`
		Licence   string  `json:"licence"`
		Installed bool    `json:"installed"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []Model
	seen := map[string]bool{}
	for _, r := range raw {
		if !idPattern.MatchString(r.ID) || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		m := Model{ID: r.ID, Label: strings.TrimSpace(r.Label), Lang: strings.TrimSpace(r.Lang), Licence: strings.TrimSpace(r.Licence), Installed: r.Installed}
		if m.Label == "" {
			m.Label = m.ID
		}
		if len(m.Label) > 200 {
			m.Label = m.Label[:200]
		}
		if r.Size > 0 && r.Size < 1<<50 {
			m.Size = int64(r.Size)
		}
		out = append(out, m)
	}
	return out, nil
}

// LicenceLink is the address to link a licence to: only an https address is one.
func (m Model) LicenceLink() (string, bool) {
	u, err := url.Parse(m.Licence)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	return m.Licence, true
}

// ErrNoAddon: the page has no such add-on, or it lacks the method the page needs.
var ErrNoAddon = errors.New("addons: the page has no such add-on")

// Size writes a size in bytes in megabytes or gigabytes.
func Size(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
