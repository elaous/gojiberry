package imagerepo

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type Config struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "openverse" or "mediawiki"
	Base string `json:"base"` // https address of the API's site
}

// ParseConfig reads the operator's list.
func ParseConfig(data []byte) ([]Repository, error) {
	var cs []Config
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("imagerepo: %w", err)
	}
	seen := map[string]bool{}
	var out []Repository
	for i, c := range cs {
		name := strings.TrimSpace(c.Name)
		if name == "" || seen[name] {
			return nil, fmt.Errorf("imagerepo: entry %d needs a name that is not used twice", i+1)
		}
		seen[name] = true
		u, err := url.Parse(c.Base)
		local := u != nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
		if err != nil || u.Host == "" || (u.Scheme != "https" && !local) {
			return nil, fmt.Errorf("imagerepo: %q needs an https address", name)
		}
		switch c.Kind {
		case "openverse":
			out = append(out, Openverse{Base: c.Base, Label: name})
		case "mediawiki":
			out = append(out, Commons{Base: c.Base, Label: name})
		default:
			return nil, fmt.Errorf("imagerepo: %q has an unknown kind %q (openverse or mediawiki)", name, c.Kind)
		}
	}
	return out, nil
}

func (l *Library) RegisterAll(rs []Repository) {
	for _, r := range rs {
		l.Register(r)
	}
}
