package piper

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/voice/read"
)

// Defaults of the catalogue Piper's voices are published in (the rhasspy/piper-voices repository). An operator can
// point a page elsewhere (a mirror) with ParseConfig.
const (
	DefaultCatalogue = "https://huggingface.co/rhasspy/piper-voices/resolve/main/voices.json"
	DefaultBase      = "https://huggingface.co/rhasspy/piper-voices/resolve/main/"
)

// CatalogueVoice is one voice of the catalogue.
type CatalogueVoice struct {
	// ID is the catalogue's key ("en_US-lessac-medium").
	ID string
	// Name is the voice's own name ("lessac"); Lang the BCP 47 tag ("en-US"); Quality "low", "medium" or "high".
	Name, Lang, Quality string
	Speakers            int
	// Model and Config are the paths of the two files below the base URL, with their sizes and MD5 checksums.
	Model, Config         string
	ModelSize, ConfigSize int64
	ModelMD5, ConfigMD5   string
	// Card is the path of the model card, which says whose voice it is and under what terms its data is used.
	Card string

	// Base is the address the paths are below ("" is the engine's Config.Base): a voice of another repository has its own.
	Base string
	// Source says which source the voice is listed by ("hub", a repository's id), "" for the engine's own catalogue.
	Source string
	// Licence is the name of the terms of the voice's data ("CC0 1.0"), LicenceURL where they are written, Credit what the
	// terms ask to be said, Dataset where the recordings come from; all "" for a voice whose source does not say (the
	// model card then does). Gender and Accent are for a person to choose by when the name does not say.
	Licence, LicenceURL, Credit, Dataset string
	Gender                               string
}

// Size is what installing the voice downloads.
func (c CatalogueVoice) Size() int64 { return c.ModelSize + c.ConfigSize }

type rawVoice struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Language struct {
		Code string `json:"code"`
	} `json:"language"`
	Quality     string `json:"quality"`
	NumSpeakers int    `json:"num_speakers"`
	Files       map[string]struct {
		Size int64  `json:"size_bytes"`
		MD5  string `json:"md5_digest"`
	} `json:"files"`
}

// ParseCatalogue reads the catalogue's voices.json. A voice without both its files is left out.
func ParseCatalogue(data []byte) ([]CatalogueVoice, error) {
	var raw map[string]rawVoice
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []CatalogueVoice
	for key, r := range raw {
		v := CatalogueVoice{ID: key, Name: r.Name, Quality: r.Quality, Speakers: max(r.NumSpeakers, 1),
			Lang: strings.ReplaceAll(r.Language.Code, "_", "-")}
		if r.Key != "" {
			v.ID = r.Key
		}
		for p, f := range r.Files {
			switch {
			case strings.HasSuffix(p, ".onnx.json"):
				v.Config, v.ConfigSize, v.ConfigMD5 = p, f.Size, f.MD5
			case strings.HasSuffix(p, ".onnx"):
				v.Model, v.ModelSize, v.ModelMD5 = p, f.Size, f.MD5
			case path.Base(p) == "MODEL_CARD":
				v.Card = p
			}
		}
		if v.Model == "" || v.Config == "" || v.Lang == "" {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ForLanguage lists the voices of a language tag: the same base language ("de" for "de-AT"), the exact tag first,
// then by quality (high first), then by name.
func ForLanguage(all []CatalogueVoice, lang string) []CatalogueVoice {
	if lang == "*" { // every language
		out := append([]CatalogueVoice(nil), all...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out
	}
	lang = strings.ToLower(strings.ReplaceAll(lang, "_", "-"))
	base, _, _ := strings.Cut(lang, "-")
	var out []CatalogueVoice
	for _, v := range all {
		vl := strings.ToLower(v.Lang)
		if vb, _, _ := strings.Cut(vl, "-"); vb == base && base != "" {
			out = append(out, v)
		}
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2, "x_low": 3}
	sort.SliceStable(out, func(i, j int) bool {
		ei, ej := strings.EqualFold(out[i].Lang, lang), strings.EqualFold(out[j].Lang, lang)
		if ei != ej {
			return ei
		}
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		if ri, rj := rank[out[i].Quality], rank[out[j].Quality]; ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// asVoice describes a catalogue voice for the player's lists.
func (c CatalogueVoice) asVoice() read.Voice {
	return read.Voice{ID: c.ID, Name: strings.ToUpper(c.Name[:min(1, len(c.Name))]) + c.Name[min(1, len(c.Name)):], Lang: c.Lang, Local: true}
}

// base is the address the voice's paths are below, the given default when it has none of its own.
func (c CatalogueVoice) base(def string) string {
	if c.Base != "" {
		return c.Base
	}
	return def
}
