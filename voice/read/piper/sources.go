package piper

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

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

var ErrBadVoiceFiles = errors.New("piper: those files are not a Piper voice: choose the .onnx model and its .onnx.json configuration")

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
