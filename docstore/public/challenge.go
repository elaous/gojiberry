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

// ChallengeHeader carries a challenge the server wants solved (on the 403 that asks for it) and
// ChallengeAnswerHeader the solution (on the retry).
const (
	ChallengeHeader       = pow.Header
	ChallengeAnswerHeader = pow.AnswerHeader
)

// ChallengeIssuer is a Challenger that can say what it wants: Challenge returns the text to solve and
// the difficulty, or "" when nothing is asked of this request. The service sends it in ChallengeHeader on
// the 403 that refuses an unsolved request, so a client can solve it and retry once (the client package
// does).
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

// ProofOfWork is a Challenger that asks a client for a small computation before it takes a session or
// a document, only while Busy says the offer is under load (nil: always). The challenge is stateless
// (a time slot and its keyed hash), valid for Window; the answer is a nonce such that
// SHA-256(challenge ":" nonce) starts with Difficulty zero bits. Each answer is accepted once. It
// raises the cost of making many sessions or documents from one machine; it does not stop an attacker
// with many machines, and a person with a slow phone pays too, so the difficulty is for the operator to
// set (20 bits is about a second on a laptop).
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

// ErrChallengeNeeded is what Verify returns for a request that has not solved the challenge.
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
	if cur := p.now().Unix() / win; slot > cur || cur-slot > 1 { // this slot or the one before it
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

// BusyThreshold is the share of the byte budget past which Busy says the offer is under load.
const BusyThreshold = 0.7

// Busy reports whether the offer is filling up (the live bytes are past BusyThreshold of the budget). It is the
// usual Busy of a ProofOfWork: the challenge is asked for only while there is a reason to.
func (s *Service) Busy() bool {
	used, _ := s.bytes.get()
	return s.cfg.MaxTotalBytes > 0 && float64(used) >= BusyThreshold*float64(s.cfg.MaxTotalBytes)
}
