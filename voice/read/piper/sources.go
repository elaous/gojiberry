package piper

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// Repositories the person adds, and voices they bring themselves. Neither is checked for its terms by the project: each
// voice of a repository carries the model card its publisher wrote, and the page shows it before anything is
// downloaded; a voice from the person's own files has the terms the person says it has.

// CheckAddress says whether text is an address of a repository's voices.json: https, with a host, and no credentials,
// spaces or fragment.
func CheckAddress(raw string) error {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, " \t\r\n\"'<>") || len(raw) > 500 {
		return errors.New("piper: a repository's address is https://host/path/voices.json, with no spaces")
	}
	if !strings.HasSuffix(strings.ToLower(u.Path), ".json") {
		return errors.New("piper: a repository's address ends in the name of its catalogue, such as voices.json")
	}
	return nil
}

// ParseRepository reads a repository's catalogue (the format of Piper's voices.json) and marks each voice as the
// repository's: its files are below the folder of the catalogue, and its terms are the ones on its model card.
func ParseRepository(address, id string, data []byte) ([]CatalogueVoice, error) {
	if err := CheckAddress(address); err != nil {
		return nil, err
	}
	list, err := ParseCatalogue(data)
	if err != nil {
		return nil, errors.New("piper: that is not a catalogue of voices")
	}
	base := address[:strings.LastIndex(address, "/")+1]
	for i := range list {
		list[i].ID = id + ":" + list[i].ID
		list[i].Base, list[i].Source = base, id
	}
	return list, nil
}

// Limits of the files a person brings.
const (
	MaxOwnModel  = 300 << 20
	MaxOwnConfig = 1 << 20
)

// ErrBadVoiceFiles is returned for files that are not a Piper voice.
var ErrBadVoiceFiles = errors.New("piper: those files are not a Piper voice: choose the .onnx model and its .onnx.json configuration")

// OwnVoiceID is the id of a voice the person brought: the language of its configuration, a name made of letters and
// digits, and "own" (the Piper way of naming, so the voice is listed under its language).
func OwnVoiceID(lang, name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			sb.WriteRune(r)
		} else if sb.Len() > 0 && !strings.HasSuffix(sb.String(), "_") {
			sb.WriteByte('_')
		}
	}
	slug := strings.Trim(sb.String(), "_")
	if slug == "" {
		slug = "voice"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if lang == "" {
		lang = "en"
	}
	return strings.ReplaceAll(lang, "-", "_") + "-" + slug + "-own"
}

// InstallFiles keeps a voice the person brought: the model and its configuration, as they are. Nothing is sent
// anywhere. It checks only that the configuration is a Piper one (it has the sample rate and the phoneme map) and that the
// sizes are within limits; whether the model sounds right is for the person to hear. name is what they call it; the
// language is the one in the configuration. It returns the voice's id.
func (e *Engine) InstallFiles(ctx context.Context, name string, config, model []byte, done func(id string, err error)) {
	var c struct {
		Audio struct {
			SampleRate int `json:"sample_rate"`
		} `json:"audio"`
		Language struct {
			Code string `json:"code"`
		} `json:"language"`
		Espeak struct {
			Voice string `json:"voice"`
		} `json:"espeak"`
		IDs map[string][]int `json:"phoneme_id_map"`
	}
	if len(config) == 0 || len(config) > MaxOwnConfig || len(model) == 0 || len(model) > MaxOwnModel ||
		json.Unmarshal(config, &c) != nil || c.Audio.SampleRate <= 0 || len(c.IDs) == 0 {
		done("", ErrBadVoiceFiles)
		return
	}
	lang := strings.ReplaceAll(c.Language.Code, "_", "-")
	if lang == "" {
		lang = c.Espeak.Voice
	}
	id := OwnVoiceID(lang, name)
	e.Store.Put(configKey(id), config, func(err error) {
		if err != nil {
			done("", err)
			return
		}
		e.Store.Put(modelKey(id), model, func(err error) {
			if err != nil {
				e.Store.Delete(configKey(id), func(error) { done("", err) })
				return
			}
			e.installed[id] = true
			done(id, nil)
		})
	})
}
