package piper

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Config says where the voices come from and what runs them. An operator writes it as reader-voices.json next to the
// page; the page reads it with ParseConfig. Every address is https or relative to the page (a mirror of the voices on
// the operator's own server is the usual reason to set it); a plain http address or any other scheme is refused.
type Config struct {
	// Catalogue is the address of voices.json, Base the address the paths in it are below.
	Catalogue, Base string
	// Runtime is the address of the script that offers the Piper runtime to the page (window.gjPiper); "" for a page
	// that carries it already, or has none.
	Runtime string
}

// DefaultConfig points at the published voices and names no runtime.
func DefaultConfig() Config { return Config{Catalogue: DefaultCatalogue, Base: DefaultBase} }

// ParseConfig reads the operator's reader-voices.json: {"catalogue": "...", "base": "...", "runtime": "..."}. Missing
// entries keep the defaults (a base is taken from the catalogue's folder when only the catalogue is given).
func ParseConfig(data []byte) (Config, error) {
	var raw struct {
		Catalogue string `json:"catalogue"`
		Base      string `json:"base"`
		Runtime   string `json:"runtime"`
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
	for _, e := range [][2]string{{"catalogue", raw.Catalogue}, {"base", raw.Base}, {"runtime", raw.Runtime}} {
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
	return c, nil
}
