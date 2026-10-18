package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// browser makes the keys a browser would: a subscription for an address and the private half.
func browser(t *testing.T, endpoint string) (Subscription, *ecdh.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	var sub Subscription
	sub.Endpoint = endpoint
	sub.Keys.P256dh = b64.EncodeToString(priv.PublicKey().Bytes())
	sub.Keys.Auth = b64.EncodeToString(auth)
	return sub, priv, auth
}

func TestEncryptThenDecryptGivesTheMessageBack(t *testing.T) {
	sub, priv, auth := browser(t, "https://push.example/send/abc")
	for _, msg := range []string{"x", `{"title":"Ana mentioned you","body":"in Plan"}`, strings.Repeat("é", 900)} {
		body, err := Encrypt(sub, []byte(msg))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decrypt(body, priv, auth)
		if err != nil || string(got) != msg {
			t.Fatalf("%q: got %q, %v", msg, got, err)
		}
	}
	a, _ := Encrypt(sub, []byte("same"))
	b, _ := Encrypt(sub, []byte("same"))
	if bytes.Equal(a, b) {
		t.Error("two messages must differ (fresh key and salt)")
	}
	// the header layout of RFC 8188: salt, record size, key id length, key id
	if len(a) < 21+65 || a[20] != 65 || a[16] != 0 || a[17] != 0 || a[18] != 0x10 || a[19] != 0 {
		t.Errorf("header: % x", a[:24])
	}
	if _, err := Encrypt(sub, make([]byte, MaxPayload+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	// somebody else's keys, and a changed byte, do not open it
	_, other, _ := browser(t, "https://push.example/x")
	if _, err := Decrypt(a, other, auth); err == nil {
		t.Error("another key opened it")
	}
	a[len(a)-1] ^= 1
	if _, err := Decrypt(a, priv, auth); err == nil {
		t.Error("a changed message opened")
	}
}

func TestValidateRefusesWhatIsNotASubscription(t *testing.T) {
	good, _, _ := browser(t, "https://fcm.googleapis.com/fcm/send/abc")
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*Subscription){
		"http":     func(s *Subscription) { s.Endpoint = "http://push.example/x" },
		"loopback": func(s *Subscription) { s.Endpoint = "https://127.0.0.1/x" },
		"private":  func(s *Subscription) { s.Endpoint = "https://10.0.0.5/x" },
		"link":     func(s *Subscription) { s.Endpoint = "https://169.254.169.254/latest" },
		"cgnat":    func(s *Subscription) { s.Endpoint = "https://100.64.1.1/x" },
		"user":     func(s *Subscription) { s.Endpoint = "https://u:p@push.example/x" },
		"empty":    func(s *Subscription) { s.Endpoint = "" },
		"long":     func(s *Subscription) { s.Endpoint = "https://push.example/" + strings.Repeat("a", MaxEndpoint) },
		"key":      func(s *Subscription) { s.Keys.P256dh = "AAAA" },
		"notpoint": func(s *Subscription) { s.Keys.P256dh = b64.EncodeToString(append([]byte{4}, make([]byte, 64)...)) },
		"auth":     func(s *Subscription) { s.Keys.Auth = "AAAA" },
	} {
		s := good
		mod(&s)
		if err := s.Validate(); !errors.Is(err, ErrBadSubscription) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVAPIDKeyRoundTripsAndTheTokenVerifies(t *testing.T) {
	v, err := GenerateVAPID()
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadVAPID(v.Private())
	if err != nil || again.Public() != v.Public() {
		t.Fatalf("load: %v", err)
	}
	if _, err := LoadVAPID("short"); err == nil {
		t.Error("a short key loaded")
	}
	now := time.Unix(1_700_000_000, 0)
	h, err := v.authorization("https://push.example:8443/send/abc", "mailto:ops@example.org", now)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(h, "vapid t=")
	if !ok {
		t.Fatal(h)
	}
	jwt, k, _ := strings.Cut(rest, ", k=")
	if k != v.Public() {
		t.Error("the key in the header is not the public key")
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal(jwt)
	}
	claims, _ := decodeB64(parts[1])
	var c struct {
		Aud string
		Exp int64
		Sub string
	}
	if err := json.Unmarshal(claims, &c); err != nil || c.Aud != "https://push.example:8443" || c.Sub != "mailto:ops@example.org" || c.Exp != now.Add(12*time.Hour).Unix() {
		t.Errorf("claims %s, %v", claims, err)
	}
	sig, _ := decodeB64(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&v.key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the signature does not verify")
	}
}

func TestSendPostsAnEncryptedMessageWithTheHeadersAPushServiceNeeds(t *testing.T) {
	v, _ := GenerateVAPID()
	var got struct {
		auth, enc, ttl, typ string
		body                []byte
	}
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.enc, got.ttl, got.typ = r.Header.Get("Authorization"), r.Header.Get("Content-Encoding"), r.Header.Get("TTL"), r.Header.Get("Content-Type")
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	sub, priv, auth := browser(t, srv.URL+"/send/abc")
	s := &Sender{VAPID: v, Subject: "mailto:ops@example.org", TTL: time.Hour, allowLocal: true}
	if err := s.Send(context.Background(), sub, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(got.body, priv, auth)
	if err != nil || string(plain) != "hello" {
		t.Fatalf("the service got %q, %v", plain, err)
	}
	if got.enc != "aes128gcm" || got.ttl != "3600" || got.typ != "application/octet-stream" || !strings.HasPrefix(got.auth, "vapid t=") || !strings.HasSuffix(got.auth, ", k="+v.Public()) {
		t.Errorf("headers: %+v", got)
	}
	status = http.StatusGone
	if err := s.Send(context.Background(), sub, []byte("x")); !errors.Is(err, ErrGone) {
		t.Errorf("gone: %v", err)
	}
	status = http.StatusTooManyRequests
	if err := s.Send(context.Background(), sub, []byte("x")); err == nil || errors.Is(err, ErrGone) {
		t.Errorf("a refusal other than gone: %v", err)
	}
	if err := (&Sender{VAPID: v, Subject: "ops"}).Send(context.Background(), sub, []byte("x")); err == nil {
		t.Error("a subject that is not an address was accepted")
	}
}

func TestTheDefaultClientRefusesAPrivateAddress(t *testing.T) {
	v, _ := GenerateVAPID()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	defer srv.Close()
	sub, _, _ := browser(t, srv.URL+"/x") // https on 127.0.0.1
	s := &Sender{VAPID: v, Subject: "mailto:ops@example.org"}
	if err := s.Send(context.Background(), sub, []byte("x")); !errors.Is(err, ErrBadSubscription) {
		t.Errorf("a loopback address: %v", err)
	}
	// the dialer itself refuses too (a name that resolves to a private address gets this far)
	c := defaultClient(false)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	if _, err := c.Do(req); err == nil || !strings.Contains(err.Error(), "private address") {
		t.Errorf("the dialer: %v", err)
	}
}

func TestHandlerKeySubscribeUnsubscribe(t *testing.T) {
	v, _ := GenerateVAPID()
	st := &MemStore{}
	allowed := "ana"
	h, err := Handler(Config{VAPID: v, Store: st, Authorize: func(_ *http.Request, handle string) bool { return handle == allowed }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(Config{VAPID: v, Store: st}); err == nil {
		t.Error("a handler without Authorize was made")
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := do("GET", "/key", ""); w.Code != 200 || !strings.Contains(w.Body.String(), v.Public()) || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("key: %d %s", w.Code, w.Body)
	}
	sub, _, _ := browser(t, "https://push.example/a")
	enc, _ := json.Marshal(map[string]any{"handle": "ana", "subscription": sub})
	if w := do("POST", "/subscribe", string(enc)); w.Code != 204 {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body)
	}
	if l, _ := st.For(context.Background(), "ana"); len(l) != 1 {
		t.Fatal("not kept")
	}
	other, _ := json.Marshal(map[string]any{"handle": "bo", "subscription": sub})
	if w := do("POST", "/subscribe", string(other)); w.Code != 403 {
		t.Errorf("another handle: %d", w.Code)
	}
	for name, body := range map[string]string{"junk": "not json", "unknown field": `{"handle":"ana","subscription":{},"x":1}`, "bad handle": `{"handle":"a b","subscription":{}}`, "empty sub": `{"handle":"ana","subscription":{}}`} {
		if w := do("POST", "/subscribe", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// the sixth subscription of a handle is refused
	for i := 0; i < MaxPerHandle; i++ {
		s, _, _ := browser(t, "https://push.example/n"+strings.Repeat("x", i+1))
		e, _ := json.Marshal(map[string]any{"handle": "ana", "subscription": s})
		do("POST", "/subscribe", string(e))
	}
	if w := do("POST", "/subscribe", mustJSON(t, map[string]any{"handle": "ana", "subscription": func() Subscription { s, _, _ := browser(t, "https://push.example/last"); return s }()})); w.Code != 429 {
		t.Errorf("too many: %d", w.Code)
	}
	// unsubscribing: only for the handle's own subscription
	un, _ := json.Marshal(map[string]any{"handle": "ana", "endpoint": sub.Endpoint})
	if w := do("POST", "/unsubscribe", string(un)); w.Code != 204 {
		t.Errorf("unsubscribe: %d", w.Code)
	}
	if l, _ := st.For(context.Background(), "ana"); len(l) != MaxPerHandle-1 {
		t.Errorf("after unsubscribe: %d", len(l))
	}
	if w := do("POST", "/unsubscribe", `{"handle":"bo","endpoint":"https://push.example/nx"}`); w.Code != 403 {
		t.Errorf("unsubscribe as another: %d", w.Code)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
