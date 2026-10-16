// Package pow is the client's half of the public offer's proof-of-work challenge (public.ProofOfWork):
// reading what the server asks for and finding a nonce. It is a package of its own so that a page that only
// needs to answer a challenge does not take in the server.
package pow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/bits"
	"strconv"
	"strings"
)

// Header carries a challenge the server wants solved (on the 403 that asks for it, as "<challenge> <bits>") and
// AnswerHeader the solution (on the retry, as "<challenge> <nonce>").
const (
	Header       = "X-Gojiberry-Challenge"
	AnswerHeader = "X-Gojiberry-Challenge-Answer"
)

// MaxDifficulty is the hardest challenge a client agrees to solve; a server asking for more is refused, not obeyed.
const MaxDifficulty = 26

// Solves reports whether SHA-256(challenge ":" nonce) starts with difficulty zero bits.
func Solves(challenge, nonce string, difficulty int) bool {
	h := sha256.Sum256([]byte(challenge + ":" + nonce))
	n := 0
	for _, b := range h {
		if b == 0 {
			n += 8
			continue
		}
		n += bits.LeadingZeros8(b)
		break
	}
	return n >= difficulty
}

// Solve finds a nonce for challenge at the difficulty, trying a random prefix and 0, 1, 2 ... up to limit tries (0: 1<<28).
func Solve(challenge string, difficulty int, limit uint64) (string, bool) {
	if limit == 0 {
		limit = 1 << 28
	}
	// A random start, so two answers to one challenge differ (a server takes each answer once).
	var r [4]byte
	_, _ = rand.Read(r[:])
	prefix := hex.EncodeToString(r[:]) + "-"
	for i := uint64(0); i < limit; i++ {
		n := prefix + strconv.FormatUint(i, 36)
		if Solves(challenge, n, difficulty) {
			return n, true
		}
	}
	return "", false
}

// Answer reads a server's Header value and returns the AnswerHeader value that solves it. ok is false for a value
// it cannot read, a difficulty over MaxDifficulty, or no solution within the limit.
func Answer(header string) (answer string, ok bool) {
	ch, bitsText, found := strings.Cut(strings.TrimSpace(header), " ")
	d, err := strconv.Atoi(bitsText)
	if !found || err != nil || d < 1 || d > MaxDifficulty || len(ch) > 100 {
		return "", false
	}
	nonce, ok := Solve(ch, d, 0)
	if !ok {
		return "", false
	}
	return ch + " " + nonce, true
}
