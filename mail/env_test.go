package mail

import (
	"errors"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("no such file")
	}
}

func TestFromEnvBuildsEachProvider(t *testing.T) {
	s, err := fromEnv(env(map[string]string{
		"GOJIBERRY_MAIL_PROVIDER": "SMTP", "GOJIBERRY_MAIL_FROM": "noreply@example.com", "GOJIBERRY_MAIL_SMTP_HOST": "smtp.example.com",
		"GOJIBERRY_MAIL_SMTP_PORT": "2587", "GOJIBERRY_MAIL_SMTP_TLS": "StartTLS", "GOJIBERRY_MAIL_SMTP_USERNAME": "mailer",
		"GOJIBERRY_MAIL_SMTP_PASSWORD_FILE": "/run/secrets/smtp",
	}), files(map[string]string{"/run/secrets/smtp": "from-a-file\n"}))
	if err != nil {
		t.Fatal(err)
	}
	smtp, ok := s.(SMTP)
	if !ok || smtp.Host != "smtp.example.com" || smtp.Port != 2587 || smtp.TLS != "starttls" || smtp.Username != "mailer" || smtp.Password != "from-a-file" || smtp.From != "noreply@example.com" {
		t.Fatalf("%+v", s)
	}
	r, err := fromEnv(env(map[string]string{"GOJIBERRY_MAIL_PROVIDER": "resend", "GOJIBERRY_MAIL_FROM": "noreply@example.com", "GOJIBERRY_MAIL_RESEND_API_KEY": "re_abc"}), files(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rs, ok := r.(Resend); !ok || rs.APIKey != "re_abc" || rs.From != "noreply@example.com" {
		t.Fatalf("%+v", r)
	}
	rf, err := fromEnv(env(map[string]string{"GOJIBERRY_MAIL_PROVIDER": "resend", "GOJIBERRY_MAIL_FROM": "noreply@example.com", "GOJIBERRY_MAIL_RESEND_API_KEY_FILE": "/k"}), files(map[string]string{"/k": "re_file\r\n"}))
	if err != nil || rf.(Resend).APIKey != "re_file" {
		t.Fatalf("%v %v", rf, err)
	}
	if _, err := FromEnv(func(string) string { return "" }); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no provider: %v", err)
	}
}

func TestFromEnvErrorsNameTheVariableAndNeverTheSecret(t *testing.T) {
	base := map[string]string{"GOJIBERRY_MAIL_FROM": "noreply@example.com"}
	with := func(kv ...string) func(string) string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return env(m)
	}
	cases := map[string]struct {
		get  func(string) string
		want string
	}{
		"unknown provider":       {with("GOJIBERRY_MAIL_PROVIDER", "sendgrid"), "PROVIDER"},
		"both forms of password": {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_PASSWORD", "topsecret", "GOJIBERRY_MAIL_SMTP_PASSWORD_FILE", "/x"), "not both"},
		"unreadable file":        {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_PASSWORD_FILE", "/missing"), "PASSWORD_FILE"},
		"oversized secret file":  {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_PASSWORD_FILE", "/big"), "too large"},
		"port not a number":      {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_PORT", "five"), "SMTP_PORT"},
		"no host":                {with("GOJIBERRY_MAIL_PROVIDER", "smtp"), "host"},
		"password without user":  {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_PASSWORD", "topsecret"), "both the SMTP user name and password"},
		"resend without a key":   {with("GOJIBERRY_MAIL_PROVIDER", "resend"), "RESEND_API_KEY"},
		"resend bad sender":      {env(map[string]string{"GOJIBERRY_MAIL_PROVIDER": "resend", "GOJIBERRY_MAIL_RESEND_API_KEY": "re_x", "GOJIBERRY_MAIL_FROM": "Ana <a@example.com>"}), "FROM"},
		"remote without TLS":     {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "smtp.example.com", "GOJIBERRY_MAIL_SMTP_TLS", "none"), "loopback"},
		"unknown TLS mode":       {with("GOJIBERRY_MAIL_PROVIDER", "smtp", "GOJIBERRY_MAIL_SMTP_HOST", "h", "GOJIBERRY_MAIL_SMTP_TLS", "ssl"), "TLS mode"},
	}
	read := files(map[string]string{"/big": strings.Repeat("x", 5000)})
	for name, c := range cases {
		_, err := fromEnv(c.get, read)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "topsecret") {
			t.Errorf("%s: the error repeats a secret: %v", name, err)
		}
	}
}
