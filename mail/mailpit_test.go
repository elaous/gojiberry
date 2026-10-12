package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests send through a real SMTP server, Mailpit (https://mailpit.axllent.org, the image
// axllent/mailpit), and read what it received back through its HTTP API. They skip unless
// GOJIBERRY_TEST_MAILPIT_SMTP and GOJIBERRY_TEST_MAILPIT_API are set (docs/testing-workflow.md says
// how to start one, and CI runs them with the image and fails on a skip):
//
//	GOJIBERRY_TEST_MAILPIT_SMTP  host:port of its SMTP listener, e.g. 127.0.0.1:1025
//	GOJIBERRY_TEST_MAILPIT_API   its web address, e.g. http://127.0.0.1:8025
//	GOJIBERRY_TEST_MAILPIT_CA    the PEM certificate its STARTTLS uses (self-signed), if it has one
//	GOJIBERRY_TEST_MAILPIT_USER / _PASS   the sign-in it requires, if it requires one
type mailpit struct {
	t         *testing.T
	host      string
	port      int
	api       string
	user      string
	pass      string
	tlsConfig *tls.Config
}

func newMailpit(t *testing.T) *mailpit {
	t.Helper()
	addr, api := os.Getenv("GOJIBERRY_TEST_MAILPIT_SMTP"), strings.TrimRight(os.Getenv("GOJIBERRY_TEST_MAILPIT_API"), "/")
	if addr == "" || api == "" {
		t.Skip("set GOJIBERRY_TEST_MAILPIT_SMTP and GOJIBERRY_TEST_MAILPIT_API to test against Mailpit")
	}
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(ps)
	m := &mailpit{t: t, host: host, port: port, api: api, user: os.Getenv("GOJIBERRY_TEST_MAILPIT_USER"), pass: os.Getenv("GOJIBERRY_TEST_MAILPIT_PASS")}
	if ca := os.Getenv("GOJIBERRY_TEST_MAILPIT_CA"); ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			t.Fatal("the Mailpit CA file has no certificate")
		}
		m.tlsConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	m.clear()
	t.Cleanup(m.clear)
	return m
}

func (m *mailpit) sender() SMTP {
	s := SMTP{Host: m.host, Port: m.port, From: "noreply@example.com", Username: m.user, Password: m.pass, TLSConfig: m.tlsConfig, Timeout: 10 * time.Second}
	if m.tlsConfig == nil {
		s.TLS = NoTLS // a plain Mailpit: loopback only
	}
	return s
}

func (m *mailpit) call(method, path string, out any) {
	m.t.Helper()
	req, _ := http.NewRequest(method, m.api+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatalf("Mailpit API: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		m.t.Fatalf("Mailpit API %s %s: %d %s", method, path, resp.StatusCode, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			m.t.Fatalf("Mailpit API %s: %v", path, err)
		}
	}
}

func (m *mailpit) clear() { m.t.Helper(); m.call("DELETE", "/api/v1/messages", nil) }

type mpAddr struct{ Address string }

type mpMessage struct {
	ID      string
	Subject string
	From    mpAddr
	To      []mpAddr
	Text    string
	HTML    string
}

// received waits for n messages and returns them, newest data included.
func (m *mailpit) received(n int) []mpMessage {
	m.t.Helper()
	var list struct {
		Messages []struct{ ID string } `json:"messages"`
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		m.call("GET", "/api/v1/messages", &list)
		if len(list.Messages) >= n || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(list.Messages) != n {
		m.t.Fatalf("Mailpit holds %d messages, want %d", len(list.Messages), n)
	}
	out := make([]mpMessage, 0, n)
	for _, e := range list.Messages {
		var full mpMessage
		m.call("GET", "/api/v1/message/"+e.ID, &full)
		out = append(out, full)
	}
	return out
}

func TestMailpitReceivesAMultipartMessageIntact(t *testing.T) {
	m := newMailpit(t)
	long := strings.Repeat("a long line of words ", 120) // far over 76 columns: quoted-printable must wrap it and Mailpit must join it
	msg := Message{
		To:      []string{"ana@example.com", "bo@example.com"},
		Subject: "Verify your address: café ✓",
		Text:    "Hello Ana,\n\nOpen this link: https://example.com/verify?token=abc123&x=1\n.\nA line that is only a dot is above.\n" + long + "\nGrüße, 你好\n",
		HTML:    `<p>Hello <b>Ana</b>, <a href="https://example.com/verify?token=abc123&amp;x=1">verify</a></p>`,
	}
	if err := m.sender().Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	got := m.received(1)[0]
	if got.Subject != msg.Subject {
		t.Errorf("subject %q", got.Subject)
	}
	if got.From.Address != "noreply@example.com" || len(got.To) != 2 || got.To[0].Address != "ana@example.com" || got.To[1].Address != "bo@example.com" {
		t.Errorf("addresses %+v -> %+v", got.From, got.To)
	}
	norm := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n") }
	if norm(got.Text) != norm(msg.Text) {
		t.Errorf("text differs:\n got %q\nwant %q", norm(got.Text), norm(msg.Text))
	}
	if norm(got.HTML) != norm(msg.HTML) {
		t.Errorf("html differs: %q", got.HTML)
	}
}

func TestMailpitReceivesAPlainTextMessage(t *testing.T) {
	m := newMailpit(t)
	if err := m.sender().Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "Plain", Text: "Just text."}); err != nil {
		t.Fatal(err)
	}
	got := m.received(1)[0]
	if strings.TrimSpace(got.Text) != "Just text." || got.HTML != "" {
		t.Errorf("%+v", got)
	}
}

func TestMailpitRefusesWhatIsNotAllowedAndNothingIsDelivered(t *testing.T) {
	m := newMailpit(t)
	if m.user != "" { // the server requires sign-in
		s := m.sender()
		s.Username, s.Password = "", ""
		if err := s.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "No sign-in", Text: "t"}); err == nil {
			t.Error("a server that requires sign-in accepted a message without it")
		}
		bad := m.sender()
		bad.Password = "definitely-not-the-password"
		err := bad.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "Wrong password", Text: "t"})
		if err == nil || strings.Contains(err.Error(), bad.Password) {
			t.Errorf("wrong password: %v", err)
		}
	}
	// a subject or recipient that would inject a header never reaches the wire
	for _, msg := range []Message{
		{To: []string{"ana@example.com"}, Subject: "hi\r\nBcc: x@example.com", Text: "t"},
		{To: []string{"ana@example.com\r\nBcc: x@example.com"}, Subject: "hi", Text: "t"},
	} {
		if err := m.sender().Send(context.Background(), msg); err == nil {
			t.Errorf("sent %+v", msg)
		}
	}
	m.received(0)
}

func TestMailpitReceivesFromTheEnvironmentConfiguration(t *testing.T) {
	m := newMailpit(t)
	env := map[string]string{"GOJIBERRY_MAIL_PROVIDER": "smtp", "GOJIBERRY_MAIL_FROM": "noreply@example.com", "GOJIBERRY_MAIL_SMTP_HOST": m.host,
		"GOJIBERRY_MAIL_SMTP_PORT": strconv.Itoa(m.port), "GOJIBERRY_MAIL_SMTP_USERNAME": m.user, "GOJIBERRY_MAIL_SMTP_PASSWORD": m.pass}
	if m.tlsConfig == nil {
		env["GOJIBERRY_MAIL_SMTP_TLS"] = "none"
	}
	s, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	smtp := s.(SMTP)
	smtp.TLSConfig, smtp.Timeout = m.tlsConfig, 10*time.Second // trust the test certificate
	if err := smtp.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: fmt.Sprintf("From env %d", time.Now().UnixNano()), Text: "t"}); err != nil {
		t.Fatal(err)
	}
	m.received(1)
}
