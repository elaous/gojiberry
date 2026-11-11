package pow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/bits"
	"strconv"
	"strings"
)

// Header carries a challenge the server wants solved.
const (
	Header       = "X-Gojiberry-Challenge"
	AnswerHeader = "X-Gojiberry-Challenge-Answer"
)

// MaxDifficulty is the hardest challenge a client will solve.
const MaxDifficulty = 26

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

// Solve finds a nonce for challenge at the difficulty.
func Solve(challenge string, difficulty int, limit uint64) (string, bool) {
	if limit == 0 {
		limit = 1 << 28
	}
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

// Answer reads a server's Header value and returns the answer.
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
