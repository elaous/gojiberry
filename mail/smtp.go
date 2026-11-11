package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// TLS modes for SMTP.
const (
	// no STARTTLS offered: the send fails, no silent downgrade
	StartTLS = "starttls"
	// ImplicitTLS is TLS from the first byte (usually port 465).
	ImplicitTLS = "tls"
	NoTLS       = "none"
)

// SMTP sends through an SMTP server.
type SMTP struct {
	Host               string
	Port               int // 0: 587 for StartTLS, 465 for ImplicitTLS, 25 for NoTLS
	Username, Password string
	// From is the envelope and header sender address.
	From string
	// TLS is StartTLS (the default when empty), ImplicitTLS or NoTLS.
	TLS       string
	Timeout   time.Duration
	TLSConfig *tls.Config
	Hostname  string
}

// String never shows the password.
func (s SMTP) String() string {
	return fmt.Sprintf("smtp(%s:%d, tls=%s, user=%q, password=%s, from=%s)", s.Host, s.port(), s.mode(), s.Username, redacted(s.Password), s.From)
}

func (s SMTP) mode() string {
	if s.TLS == "" {
		return StartTLS
	}
	return s.TLS
}

func (s SMTP) port() int {
	if s.Port != 0 {
		return s.Port
	}
	switch s.mode() {
	case ImplicitTLS:
		return 465
	case NoTLS:
		return 25
	}
	return 587
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// rejects a configuration that would send secrets unsafely
func (s SMTP) Validate() error {
	if strings.TrimSpace(s.Host) == "" || strings.ContainsAny(s.Host, " /\r\n") {
		return errors.New("mail: the SMTP host is not set or not a host name")
	}
	switch s.mode() {
	case StartTLS, ImplicitTLS:
	case NoTLS:
		if !isLoopback(s.Host) {
			return errors.New("mail: SMTP without TLS is only allowed to a loopback address")
		}
	default:
		return fmt.Errorf("mail: the SMTP TLS mode %q is not starttls, tls or none", s.TLS)
	}
	if s.port() < 1 || s.port() > 65535 {
		return errors.New("mail: the SMTP port is out of range")
	}
	if _, err := address(s.From); err != nil {
		return errors.New("mail: the sender address is not a plain address")
	}
	if (s.Username == "") != (s.Password == "") {
		return errors.New("mail: set both the SMTP user name and password, or neither")
	}
	if hasLineBreak(s.Username) || hasLineBreak(s.Password) {
		return errors.New("mail: the SMTP credentials have a line break")
	}
	return nil
}

// Send implements Sender.
func (s SMTP) Send(ctx context.Context, m Message) error {
	to, err := check(m)
	if err != nil {
		return err
	}
	if err := s.Validate(); err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := build(s.From, to, m)
	if err != nil {
		return err
	}

	cfg := s.TLSConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = s.Host
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.port()))
	var conn net.Conn
	if s.mode() == ImplicitTLS {
		conn, err = (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: cannot reach the SMTP server: %s", briefError(err))
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("mail: the SMTP server did not greet: %s", briefError(err))
	}
	defer func() { _ = c.Close() }()
	hello := s.Hostname
	if hello == "" {
		hello = "localhost"
	}
	if err := c.Hello(hello); err != nil {
		return fmt.Errorf("mail: SMTP hello was refused: %s", briefError(err))
	}
	if s.mode() == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mail: the SMTP server does not offer STARTTLS; nothing was sent")
		}
		if err := c.StartTLS(cfg); err != nil {
			return fmt.Errorf("mail: STARTTLS failed: %s", briefError(err))
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("mail: the SMTP server does not offer sign-in")
		}
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail: SMTP sign-in failed: %s", briefError(err)) // net/smtp never puts the password in it
		}
	}
	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("mail: the SMTP server refused the sender: %s", briefError(err))
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("mail: the SMTP server refused a recipient: %s", briefError(err))
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: the SMTP server refused the message: %s", briefError(err))
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mail: sending the message failed: %s", briefError(err))
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: the SMTP server did not accept the message: %s", briefError(err))
	}
	_ = c.Quit()
	return nil
}

// CRLF lines, RFC 2047 subject, quoted-printable body
func build(from string, to []string, m Message) ([]byte, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	domain := from[strings.LastIndex(from, "@")+1:]
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", strings.Join(to, ", "))
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	part := func(w *bytes.Buffer, ctype, text string) error {
		w.WriteString("Content-Type: " + ctype + "; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
			return err
		}
		if err := qp.Close(); err != nil {
			return err
		}
		w.WriteString("\r\n")
		return nil
	}
	if m.HTML == "" {
		if err := part(&b, "text/plain", m.Text); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	boundary := "gj-" + hex.EncodeToString(id[:8])
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, p := range []struct{ ctype, text string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		if err := part(&b, p.ctype, p.text); err != nil {
			return nil, err
		}
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes(), nil
}
