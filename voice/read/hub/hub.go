// Package hub is the project's short list of reader voices whose terms have been read: four Piper voices to begin with
// (American and British, a woman's voice and a man's), each with the licence of its recordings shown before anything is
// downloaded. The files stay where Piper's voices are published (rhasspy/piper-voices); the list pins each file's size and
// checksum, so a changed file is refused.
package hub

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/elaous/gojiberry/voice/read/piper"
)

//go:embed hub.json
var raw []byte

// ID is the id of the hub as a source of voices.
const ID = "hub"

// List is the hub as the file gives it.
type List struct {
	About  string
	Read   string // the day the terms were read, YYYY-MM-DD
	Base   string
	Voices []piper.CatalogueVoice
}

// Load reads the embedded list.
func Load() (List, error) {
	var f struct {
		About  string `json:"about"`
		Read   string `json:"read"`
		Base   string `json:"base"`
		Voices []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Lang       string `json:"lang"`
			Gender     string `json:"gender"`
			Quality    string `json:"quality"`
			Model      string `json:"model"`
			ModelSize  int64  `json:"modelSize"`
			ModelMD5   string `json:"modelMD5"`
			Config     string `json:"config"`
			ConfigSize int64  `json:"configSize"`
			ConfigMD5  string `json:"configMD5"`
			Card       string `json:"card"`
			Licence    string `json:"licence"`
			LicenceURL string `json:"licenceURL"`
			Credit     string `json:"credit"`
			Dataset    string `json:"dataset"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return List{}, err
	}
	out := List{About: f.About, Read: f.Read, Base: f.Base}
	for _, v := range f.Voices {
		out.Voices = append(out.Voices, piper.CatalogueVoice{
			ID: v.ID, Name: v.Name, Lang: v.Lang, Quality: v.Quality, Speakers: 1,
			Model: v.Model, ModelSize: v.ModelSize, ModelMD5: v.ModelMD5, Config: v.Config, ConfigSize: v.ConfigSize, ConfigMD5: v.ConfigMD5,
			Card: v.Card, Base: f.Base, Source: ID, Licence: v.Licence, LicenceURL: v.LicenceURL, Credit: v.Credit, Dataset: v.Dataset, Gender: strings.ToLower(v.Gender),
		})
	}
	return out, nil
}

// Voices is the hub's voices, ready for piper.Engine.AddVoices.
func Voices() []piper.CatalogueVoice {
	l, err := Load()
	if err != nil {
		return nil
	}
	return l.Voices
}
