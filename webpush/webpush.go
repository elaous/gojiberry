package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Subscription is what a browser's PushManager.subscribe gives (its toJSON form).
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"` // the browser's public key, base64url
		Auth   string `json:"auth"`   // 16 random bytes, base64url
	} `json:"keys"`
}

// Limits.
const (
	// MaxPayload is the largest message sent.
	MaxPayload = 2048
	// MaxEndpoint is the longest subscription address accepted.
	MaxEndpoint = 1024
	recordSize  = 4096
)

// Errors.
var (
	ErrGone = errors.New("webpush: the subscription is gone")
	// ErrBadSubscription: the address or the keys are not usable.
	ErrBadSubscription = errors.New("webpush: the subscription is not valid")
	// ErrTooLarge: the message is over MaxPayload.
	ErrTooLarge = errors.New("webpush: the message is too large")
)

var b64 = base64.RawURLEncoding

func decodeB64(s string) ([]byte, error) { return b64.DecodeString(strings.TrimRight(s, "=")) }

func (s Subscription) Validate() error {
	if len(s.Endpoint) == 0 || len(s.Endpoint) > MaxEndpoint {
		return ErrBadSubscription
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ErrBadSubscription
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return ErrBadSubscription
	}
	return s.validKeys()
}

func (s Subscription) validKeys() error {
	pub, err := decodeB64(s.Keys.P256dh)
	if err != nil || len(pub) != 65 {
		return ErrBadSubscription
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return ErrBadSubscription
	}
	if a, err := decodeB64(s.Keys.Auth); err != nil || len(a) != 16 {
		return ErrBadSubscription
	}
	return nil
}

func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	v4 := ip.To4()
	return v4 == nil || v4[0] != 100 || v4[1]&0xc0 != 64 // 100.64.0.0/10 is carrier-grade NAT
}

func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, ErrTooLarge
	}
	if err := sub.validKeys(); err != nil {
		return nil, err
	}
	uaPub, _ := decodeB64(sub.Keys.P256dh)
	auth, _ := decodeB64(sub.Keys.Auth)
	uaKey, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		return nil, ErrBadSubscription
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(payload, uaKey, auth, asPriv, salt)
}

func encrypt(payload []byte, uaKey *ecdh.PublicKey, auth []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	shared, err := asPriv.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPub := asPriv.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), uaKey.Bytes()...), asPub...)
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(cek))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain := append(append([]byte(nil), payload...), 0x02) // 0x02 ends the last (here only) record
	if len(plain)+16 > recordSize {
		return nil, ErrTooLarge
	}
	sealed := gcm.Seal(nil, []byte(nonce), plain, nil)
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(byte(len(asPub)))
	out.Write(asPub)
	out.Write(sealed)
	return out.Bytes(), nil
}

func Decrypt(body []byte, uaPriv *ecdh.PrivateKey, auth []byte) ([]byte, error) {
	if len(body) < 16+4+1 {
		return nil, errors.New("webpush: the body is too short")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if len(body) < 21+idlen || rs < 18 {
		return nil, errors.New("webpush: the header is not valid")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	record := body[21+idlen:]
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	prkKey, _ := hkdf.Extract(sha256.New, shared, auth)
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, err := aes.NewCipher([]byte(cek))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, []byte(nonce), record, nil)
	if err != nil {
		return nil, err
	}
	i := len(plain)
	for i > 0 && plain[i-1] == 0 {
		i--
	}
	if i == 0 || plain[i-1] != 0x02 {
		return nil, errors.New("webpush: the record has no delimiter")
	}
	return plain[:i-1], nil
}

type VAPID struct {
	key *ecdsa.PrivateKey
}

// GenerateVAPID makes a key.
func GenerateVAPID() (*VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &VAPID{key: k}, nil
}

func LoadVAPID(private string) (*VAPID, error) {
	raw, err := decodeB64(private)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("webpush: the VAPID private key is not 32 bytes of base64url")
	}
	d := new(big.Int).SetBytes(raw)
	k := &ecdsa.PrivateKey{D: d}
	k.Curve = elliptic.P256()
	k.X, k.Y = k.ScalarBaseMult(raw)
	if d.Sign() == 0 || d.Cmp(k.Curve.Params().N) >= 0 {
		return nil, errors.New("webpush: the VAPID private key is out of range")
	}
	return &VAPID{key: k}, nil
}

func (v *VAPID) Private() string {
	b := v.key.D.FillBytes(make([]byte, 32))
	return b64.EncodeToString(b)
}

func (v *VAPID) Public() string {
	e, _ := v.key.PublicKey.ECDH()
	return b64.EncodeToString(e.Bytes())
}

func (v *VAPID) authorization(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject})
	signing := b64.EncodeToString(header) + "." + b64.EncodeToString(claims)
	h := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, h[:])
	if err != nil {
		return "", err
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + v.Public(), nil
}

// Sender sends messages.
type Sender struct {
	VAPID   *VAPID
	Subject string
	Client  *http.Client
	TTL     time.Duration

	allowLocal bool // tests: the push service is on loopback and http
	now        func() time.Time
}

// Send sends the payload to the subscription.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte) error {
	if s.VAPID == nil {
		return errors.New("webpush: no VAPID key")
	}
	if !strings.HasPrefix(s.Subject, "mailto:") && !strings.HasPrefix(s.Subject, "https://") {
		return errors.New("webpush: the subject must be a mailto: or https: address")
	}
	if !s.allowLocal {
		if err := sub.Validate(); err != nil {
			return err
		}
	} else if sub.Endpoint == "" {
		return ErrBadSubscription
	}
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	auth, err := s.VAPID.authorization(sub.Endpoint, s.Subject, now())
	if err != nil {
		return err
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl/time.Second)))
	client := s.Client
	if client == nil {
		client = defaultClient(s.allowLocal)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("webpush: the push service answered %d", resp.StatusCode)
	}
	return nil
}

func defaultClient(allowLocal bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowLocal {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return errors.New("webpush: the push service is on a private address")
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       20 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, // a push service does not redirect
	}
}
