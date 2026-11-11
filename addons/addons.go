// Package addons lists the page's add-ons and their required methods.
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
	// Method is the method the page needs from the add-on.
	Method string
	// Purpose is a sentence in English about what it is for.
	Purpose string
	// Models reports whether the add-on has downloadable models.
	Models bool
}

// Known lists the add-ons the page uses, in display order.
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

// Model is a download an add-on keeps: a speech model, dictionary or language pair.
type Model struct {
	// ID is passed to install and remove; Label is shown to people.
	ID, Label string
	// Lang is the BCP 47 tag it serves, "" for any (or none).
	Lang string
	// Size is what installing downloads, in bytes (0 when not known).
	Size int64
	// Licence is an https address or a short name such as "MIT".
	Licence string
	// Installed says it is on the device.
	Installed bool
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:\-]{0,119}$`)

// ParseModels reads an add-on's models() JSON; it drops models without an id.
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

// LicenceLink returns the licence as a link, only for https addresses.
func (m Model) LicenceLink() (string, bool) {
	u, err := url.Parse(m.Licence)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	return m.Licence, true
}

// ErrNoAddon: the page has no such add-on or method.
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
