package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeServer struct {
	ln       net.Listener
	cert     tls.Certificate
	pool     *x509.CertPool
	startTLS bool // offer STARTTLS
	implicit bool // TLS from the first byte
	auth     bool // offer AUTH PLAIN, with the credentials below
	user     string
	pass     string

	mu       sync.Mutex
	commands []string
	data     string
	authOK   bool
	sawTLS   bool
}

func newFakeServer(t *testing.T, f func(*fakeServer)) *fakeServer {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	leaf, _ := x509.ParseCertificate(der)
	s := &fakeServer{cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool: x509.NewCertPool()}
	s.pool.AddCert(leaf)
	f(s)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if s.implicit {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{s.cert}})
	}
	s.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve()
	return s
}

func (s *fakeServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeServer) sender(mode string) SMTP {
	return SMTP{Host: "127.0.0.1", Port: s.port(), TLS: mode, From: "noreply@example.com", TLSConfig: &tls.Config{RootCAs: s.pool}, Timeout: 5 * time.Second}
}

func (s *fakeServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *fakeServer) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r, w := bufio.NewReader(c), func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	secure := s.implicit
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.commands = append(s.commands, strings.SplitN(line, " ", 2)[0])
		s.mu.Unlock()
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			w("250-fake")
			if s.startTLS && !secure {
				w("250-STARTTLS")
			}
			if s.auth {
				w("250-AUTH PLAIN")
			}
			w("250 8BITMIME")
		case up == "STARTTLS":
			w("220 go ahead")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if tc.Handshake() != nil {
				return
			}
			c, r, secure = tc, bufio.NewReader(tc), true
			w = func(line string) { _, _ = tc.Write([]byte(line + "\r\n")) }
		case strings.HasPrefix(up, "AUTH PLAIN "):
			raw, _ := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			parts := strings.Split(string(raw), "\x00")
			ok := len(parts) == 3 && parts[1] == s.user && parts[2] == s.pass
			s.mu.Lock()
			s.authOK, s.sawTLS = ok, secure
			s.mu.Unlock()
			if ok {
				w("235 ok")
			} else {
				w("535 bad credentials")
			}
		case strings.HasPrefix(up, "MAIL FROM"), strings.HasPrefix(up, "RCPT TO"):
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			w("250 queued")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (s *fakeServer) got() (cmds []string, data string, authOK, sawTLS bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...), s.data, s.authOK, s.sawTLS
}

var msg = Message{To: []string{"ana@example.com", "bo@example.com"}, Subject: "Verify: café", Text: "Open the link.\n.\nA line with a lone dot above.", HTML: "<p>Open the link.</p>"}

func TestSMTPStartTLSWithSignInSendsTheMessage(t *testing.T) {
	s := newFakeServer(t, func(f *fakeServer) { f.startTLS, f.auth, f.user, f.pass = true, true, "mailer", "s3cret" })
	c := s.sender(StartTLS)
	c.Username, c.Password = "mailer", "s3cret"
	if err := c.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	cmds, data, authOK, sawTLS := s.got()
	if !authOK || !sawTLS {
		t.Fatalf("signed in %v over TLS %v: credentials only after the upgrade", authOK, sawTLS)
	}
	if strings.Join(cmds, ",") != "EHLO,STARTTLS,EHLO,AUTH,MAIL,RCPT,RCPT,DATA,QUIT" {
		t.Fatalf("%v", cmds)
	}
	for _, want := range []string{"From: noreply@example.com\r\n", "To: ana@example.com, bo@example.com\r\n", "Subject: =?utf-8?q?Verify:_caf=C3=A9?=\r\n", "MIME-Version: 1.0\r\n",
		"multipart/alternative", "text/plain; charset=utf-8", "text/html; charset=utf-8", "<p>Open the link.</p>", "Message-ID: <"} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
	if strings.Count(data, "\n") != strings.Count(data, "\r\n") {
		t.Error("every line ends in CRLF")
	}
}

func TestSMTPImplicitTLSAndPlainText(t *testing.T) {
	s := newFakeServer(t, func(f *fakeServer) { f.implicit, f.auth, f.user, f.pass = true, true, "u", "p" })
	c := s.sender(ImplicitTLS)
	c.Username, c.Password = "u", "p"
	if err := c.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "Plain", Text: "Just text."}); err != nil {
		t.Fatal(err)
	}
	_, data, authOK, sawTLS := s.got()
	if !authOK || !sawTLS || strings.Contains(data, "multipart") || !strings.Contains(data, "Content-Type: text/plain; charset=utf-8") {
		t.Fatalf("%v %v\n%s", authOK, sawTLS, data)
	}
}

func TestSMTPRefusesToDowngradeOrSendSecretsInTheClear(t *testing.T) {
	s := newFakeServer(t, func(f *fakeServer) { f.auth, f.user, f.pass = true, "u", "p" })
	c := s.sender(StartTLS)
	c.Username, c.Password = "u", "p"
	err := c.Send(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
		t.Fatalf("%v", err)
	}
	if _, _, authOK, _ := s.got(); authOK {
		t.Fatal("credentials reached a server without TLS")
	}
	s2 := newFakeServer(t, func(f *fakeServer) { f.startTLS = true })
	c2 := s2.sender(StartTLS)
	c2.TLSConfig = &tls.Config{} // system roots only
	if err := c2.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "STARTTLS failed") {
		t.Fatalf("%v", err)
	}
	s3 := newFakeServer(t, func(f *fakeServer) { f.startTLS, f.auth, f.user, f.pass = true, true, "u", "right" })
	c3 := s3.sender(StartTLS)
	c3.Username, c3.Password = "u", "wrong-secret-password"
	if err := c3.Send(context.Background(), msg); err == nil || strings.Contains(err.Error(), "wrong-secret-password") || !strings.Contains(err.Error(), "sign-in failed") {
		t.Fatalf("%v", err)
	}
	s4 := newFakeServer(t, func(f *fakeServer) { f.startTLS = true })
	c4 := s4.sender(StartTLS)
	c4.Username, c4.Password = "u", "p"
	if err := c4.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "does not offer sign-in") {
		t.Fatalf("%v", err)
	}
}

func TestSMTPNoTLSToLoopbackOnlyAndOpenRelay(t *testing.T) {
	s := newFakeServer(t, func(f *fakeServer) {})
	c := s.sender(NoTLS)
	if err := c.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if cmds, data, _, _ := s.got(); len(cmds) < 5 || !strings.Contains(data, "Subject:") {
		t.Fatalf("%v", cmds)
	}
	remote := SMTP{Host: "smtp.example.com", TLS: NoTLS, From: "a@example.com"}
	if err := remote.Validate(); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("%v", err)
	}
}

func TestSMTPValidate(t *testing.T) {
	ok := SMTP{Host: "smtp.example.com", From: "noreply@example.com", Username: "u", Password: "p"}
	if err := ok.Validate(); err != nil || ok.port() != 587 || (SMTP{TLS: ImplicitTLS}).port() != 465 || (SMTP{TLS: NoTLS}).port() != 25 || (SMTP{Port: 2525}).port() != 2525 {
		t.Fatalf("%v", err)
	}
	for name, s := range map[string]SMTP{
		"no host":          {From: "a@example.com"},
		"host with a path": {Host: "smtp.example.com/x", From: "a@example.com"},
		"unknown mode":     {Host: "h", TLS: "ssl", From: "a@example.com"},
		"bad port":         {Host: "h", Port: 70000, From: "a@example.com"},
		"bad sender":       {Host: "h", From: "Ana <a@example.com>"},
		"user only":        {Host: "h", From: "a@example.com", Username: "u"},
		"password only":    {Host: "h", From: "a@example.com", Password: "p"},
		"line break":       {Host: "h", From: "a@example.com", Username: "u\r\nQUIT", Password: "p"},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: valid", name)
		}
	}
	if err := (SMTP{Host: "h", From: "a@example.com"}).Send(context.Background(), Message{}); err == nil {
		t.Error("a bad message is refused before connecting")
	}
	if err := (SMTP{}).Send(context.Background(), msg); err == nil {
		t.Error("an unconfigured sender is refused")
	}
}

func TestSMTPUnreachableAndCancelled(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing listens there now
	c := SMTP{Host: "127.0.0.1", Port: port, TLS: NoTLS, From: "a@example.com", Timeout: 2 * time.Second}
	if err := c.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("%v", err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Send(cctx, msg); err == nil {
		t.Fatal("cancelled")
	}
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer func() { _ = silent.Close() }()
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()
	slow := SMTP{Host: "127.0.0.1", Port: silent.Addr().(*net.TCPAddr).Port, TLS: NoTLS, From: "a@example.com", Timeout: 300 * time.Millisecond}
	start := time.Now()
	if err := slow.Send(context.Background(), msg); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
