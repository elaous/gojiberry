package mail

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const envPrefix = "GOJIBERRY_MAIL_"

// FromEnv builds the sender the environment describes.
func FromEnv(getenv func(string) string) (Sender, error) {
	return fromEnv(getenv, os.ReadFile)
}

func fromEnv(getenv func(string) string, readFile func(string) ([]byte, error)) (Sender, error) {
	get := func(name string) string { return strings.TrimSpace(getenv(envPrefix + name)) }
	secret := func(name string) (string, error) {
		v, f := getenv(envPrefix+name), get(name+"_FILE")
		switch {
		case v != "" && f != "":
			return "", fmt.Errorf("mail: set %s%s or %s%s_FILE, not both", envPrefix, name, envPrefix, name)
		case f != "":
			b, err := readFile(f)
			if err != nil {
				return "", fmt.Errorf("mail: cannot read the file named by %s%s_FILE", envPrefix, name)
			}
			if len(b) > 4096 {
				return "", fmt.Errorf("mail: the file named by %s%s_FILE is too large for a secret", envPrefix, name)
			}
			return strings.TrimRight(string(b), "\r\n"), nil
		}
		return v, nil
	}
	switch p := strings.ToLower(get("PROVIDER")); p {
	case "":
		return nil, ErrNotConfigured
	case "smtp":
		pass, err := secret("SMTP_PASSWORD")
		if err != nil {
			return nil, err
		}
		s := SMTP{Host: get("SMTP_HOST"), TLS: strings.ToLower(get("SMTP_TLS")), Username: get("SMTP_USERNAME"), Password: pass, From: get("FROM")}
		if ps := get("SMTP_PORT"); ps != "" {
			n, err := strconv.Atoi(ps)
			if err != nil {
				return nil, fmt.Errorf("mail: %sSMTP_PORT is not a number", envPrefix)
			}
			s.Port = n
		}
		if err := s.Validate(); err != nil {
			return nil, err
		}
		return s, nil
	case "resend":
		key, err := secret("RESEND_API_KEY")
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, fmt.Errorf("mail: set %sRESEND_API_KEY or %sRESEND_API_KEY_FILE", envPrefix, envPrefix)
		}
		if _, err := address(get("FROM")); err != nil {
			return nil, fmt.Errorf("mail: %sFROM is not a plain address", envPrefix)
		}
		return Resend{APIKey: key, From: get("FROM")}, nil
	default:
		return nil, fmt.Errorf("mail: %sPROVIDER %q is not smtp or resend", envPrefix, p)
	}
}
