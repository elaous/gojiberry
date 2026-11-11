package public

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/docstore/public/pow"
)

// ChallengeHeader carries a challenge the server wants answered.
const (
	ChallengeHeader       = pow.Header
	ChallengeAnswerHeader = pow.AnswerHeader
)

// ChallengeIssuer is a Challenger that can issue challenges.
type ChallengeIssuer interface {
	Challenger
	Challenge(r *http.Request) (challenge string, difficulty int)
}

func (s *Service) refuseChallenge(w http.ResponseWriter, err error) {
	if iss, ok := s.challenge.(ChallengeIssuer); ok {
		if ch, bits := iss.Challenge(nil); ch != "" {
			w.Header().Set(ChallengeHeader, ch+" "+strconv.Itoa(bits))
		}
	}
	http.Error(w, err.Error(), http.StatusForbidden)
}

type ProofOfWork struct {
	Secret     []byte        // keys the challenges; at least 16 bytes
	Difficulty int           // leading zero bits, 1 to 32
	Window     time.Duration // how long a challenge lives; default 2 minutes
	Busy       func() bool   // nil: always asked
	Now        func() time.Time

	mu   sync.Mutex
	used map[string]time.Time
}

func (p *ProofOfWork) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *ProofOfWork) window() time.Duration {
	if p.Window > 0 {
		return p.Window
	}
	return 2 * time.Minute
}

func (p *ProofOfWork) mac(slot int64) string {
	m := hmac.New(sha256.New, p.Secret)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(slot))
	m.Write(b[:])
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

// Challenge implements ChallengeIssuer.
func (p *ProofOfWork) Challenge(*http.Request) (string, int) {
	if len(p.Secret) < 16 || p.Difficulty < 1 {
		return "", 0
	}
	if p.Busy != nil && !p.Busy() {
		return "", 0
	}
	slot := p.now().Unix() / int64(p.window().Seconds())
	return strconv.FormatInt(slot, 10) + "." + p.mac(slot), min(p.Difficulty, 32)
}

var ErrChallengeNeeded = errors.New("this server is busy: solve the challenge and try again")

// Verify implements Challenger.
func (p *ProofOfWork) Verify(r *http.Request) error {
	if len(p.Secret) < 16 || p.Difficulty < 1 {
		return errors.New("public: the proof of work is not configured")
	}
	if p.Busy != nil && !p.Busy() {
		return nil
	}
	ans := r.Header.Get(ChallengeAnswerHeader)
	ch, nonce, ok := strings.Cut(ans, " ")
	if !ok || len(ans) > 200 {
		return ErrChallengeNeeded
	}
	slotText, mac, ok := strings.Cut(ch, ".")
	slot, err := strconv.ParseInt(slotText, 10, 64)
	if !ok || err != nil || !hmac.Equal([]byte(mac), []byte(p.mac(slot))) {
		return ErrChallengeNeeded
	}
	win := int64(p.window().Seconds())
	if cur := p.now().Unix() / win; slot > cur || cur-slot > 1 {
		return ErrChallengeNeeded
	}
	if !pow.Solves(ch, nonce, min(p.Difficulty, 32)) {
		return ErrChallengeNeeded
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if p.used == nil {
		p.used = map[string]time.Time{}
	}
	for k, t := range p.used {
		if now.Sub(t) > 3*p.window() {
			delete(p.used, k)
		}
	}
	if _, again := p.used[ans]; again || len(p.used) > 100000 {
		return ErrChallengeNeeded
	}
	p.used[ans] = now
	return nil
}

const BusyThreshold = 0.7

// Busy reports whether the offer is filling up.
func (s *Service) Busy() bool {
	used, _ := s.bytes.get()
	return s.cfg.MaxTotalBytes > 0 && float64(used) >= BusyThreshold*float64(s.cfg.MaxTotalBytes)
}
