package hosted

import (
	"encoding/json"
	"net/url"
)

// maxVoices bounds the list taken from a server.
const maxVoices = 500

// parseVoices reads the server's list. An entry without an ID is left out, a name defaults to the ID, and a licence
// address that is not https is dropped (a page must not link to anything else from here).
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
