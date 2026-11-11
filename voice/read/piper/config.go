package piper

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type Config struct {
	Catalogue, Base string
	Runtime         string
	Server          string
}

// DefaultConfig points at the published voices and names no runtime.
func DefaultConfig() Config { return Config{Catalogue: DefaultCatalogue, Base: DefaultBase} }

func ParseConfig(data []byte) (Config, error) {
	var raw struct {
		Catalogue string `json:"catalogue"`
		Base      string `json:"base"`
		Runtime   string `json:"runtime"`
		Server    string `json:"server"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}
	c := DefaultConfig()
	check := func(what, u string) error {
		if u == "" {
			return nil
		}
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "" && p.Scheme != "https") || (p.Scheme == "" && p.Host != "") {
			return errors.New("piper: the " + what + " address must be https or relative to the page")
		}
		return nil
	}
	for _, e := range [][2]string{{"catalogue", raw.Catalogue}, {"base", raw.Base}, {"runtime", raw.Runtime}, {"server", raw.Server}} {
		if err := check(e[0], e[1]); err != nil {
			return Config{}, err
		}
	}
	if raw.Catalogue != "" {
		c.Catalogue = raw.Catalogue
		if raw.Base == "" {
			c.Base = raw.Catalogue[:strings.LastIndex(raw.Catalogue, "/")+1]
		}
	}
	if raw.Base != "" {
		c.Base = raw.Base
		if !strings.HasSuffix(c.Base, "/") {
			c.Base += "/"
		}
	}
	c.Runtime = raw.Runtime
	c.Server = strings.TrimSuffix(raw.Server, "/")
	return c, nil
}
