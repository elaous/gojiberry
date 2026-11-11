package hosted

import (
	"encoding/json"
	"net/url"
)

const maxVoices = 500

func parseVoices(data []byte) ([]Entry, error) {
	var raw []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Lang    string `json:"lang"`
		Licence string `json:"licence"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []Entry
	for _, r := range raw {
		if r.ID == "" || len(out) >= maxVoices {
			continue
		}
		if r.Name == "" {
			r.Name = r.ID
		}
		if u, err := url.Parse(r.Licence); err != nil || u.Scheme != "https" || u.Host == "" {
			r.Licence = ""
		}
		out = append(out, Entry{ID: r.ID, Name: r.Name, Lang: r.Lang, Licence: r.Licence})
	}
	return out, nil
}
