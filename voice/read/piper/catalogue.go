package piper

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/voice/read"
)

const (
	DefaultCatalogue = "https://huggingface.co/rhasspy/piper-voices/resolve/main/voices.json"
	DefaultBase      = "https://huggingface.co/rhasspy/piper-voices/resolve/main/"
)

// CatalogueVoice is one voice of the catalogue.
type CatalogueVoice struct {
	// ID is the catalogue's key ("en_US-lessac-medium").
	ID                    string
	Name, Lang, Quality   string
	Speakers              int
	Model, Config         string
	ModelSize, ConfigSize int64
	ModelMD5, ConfigMD5   string
	Card                  string

	Base                                 string
	Source                               string
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

// ParseCatalogue reads the catalogue's voices.json.
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

func (c CatalogueVoice) asVoice() read.Voice {
	return read.Voice{ID: c.ID, Name: strings.ToUpper(c.Name[:min(1, len(c.Name))]) + c.Name[min(1, len(c.Name)):], Lang: c.Lang, Local: true}
}

func (c CatalogueVoice) base(def string) string {
	if c.Base != "" {
		return c.Base
	}
	return def
}
