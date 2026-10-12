// Package speechcoach is the recording and personalisation side of opt-in atypical-speech support
// (roadmap 56), in the style of the data-collection flow of Google's Project Euphonia and the
// phrase-repeating setup of Project Relate. It is a clean-room implementation of the flow only;
// docs/euphonia-port.md says what the original contains, under which licence, and what a Go port cannot
// cover (a personal speech model needs training, which this package does not do).
//
// Everything here is plain Go with no network and no clock of its own (a caller passes the time), so
// it builds for the server and for the browser alike:
//
//   - wav.go, quality.go: 16 kHz mono PCM, WAV files, resampling, and simple signal checks that give
//     the speaker a quiet hint (too quiet, clipping, too short), never a refusal.
//   - phrases.go, session.go: phrase sets and a forgiving recording session (record, retake, skip, go
//     back, no time limits), with a progress record.
//   - dataset.go: the person's own recordings and transcripts, a manifest, export as a zip and import.
//   - bias.go, corrections.go: personalisation that needs no training: a vocabulary that becomes the
//     recogniser's initial prompt, and a memory of corrections that is offered as suggestions.
//   - consent.go: the optional, per-session, revocable contribution of recordings to a research endpoint
//     the operator configures; it shows exactly what would leave the device and sends nothing otherwise.
//   - recognise.go: a client for a recogniser endpoint of the voiceserver shape (bring your own).
//   - seal.go: sealing the dataset with a key the caller holds (the encrypted store's, when unlocked).
package speechcoach

import (
	"encoding/binary"
	"errors"
	"math"
)

// SampleRate is the rate of every recording kept: 16 kHz, the rate speech recognisers (whisper.cpp
// among them) take.
const SampleRate = 16000

// MaxSeconds is the longest take kept. It is far more than a phrase needs; it is a limit of memory, not
// a time limit on the person, and reaching it keeps what was said.
const MaxSeconds = 600

// MaxSamples is MaxSeconds of samples.
const MaxSamples = MaxSeconds * SampleRate

// ErrFormat is returned for a WAV that is not 16-bit PCM mono at 16 kHz.
var ErrFormat = errors.New("speechcoach: not a 16-bit mono 16 kHz PCM WAV")

// EncodeWAV writes samples as a canonical 44-byte-header WAV (PCM, 16 bit, mono, 16 kHz).
func EncodeWAV(pcm []int16) []byte {
	n := len(pcm) * 2
	b := make([]byte, 44+n)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(s))
	}
	return b
}

// DecodeWAV reads a WAV written by EncodeWAV, or any 16-bit mono 16 kHz PCM WAV (other chunks before
// "data" are skipped). Anything else is ErrFormat; it never reads past the end of b.
func DecodeWAV(b []byte) ([]int16, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, ErrFormat
	}
	ok := false
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := p + 8
		if size < 0 || body+size > len(b) {
			if id != "data" { // a data chunk may be cut short (a stream); others may not
				return nil, ErrFormat
			}
			size = len(b) - body
		}
		switch id {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(b[body:]) != 1 || binary.LittleEndian.Uint16(b[body+2:]) != 1 ||
				binary.LittleEndian.Uint32(b[body+4:]) != SampleRate || binary.LittleEndian.Uint16(b[body+14:]) != 16 {
				return nil, ErrFormat
			}
			ok = true
		case "data":
			if !ok {
				return nil, ErrFormat
			}
			n := size / 2
			if n > MaxSamples {
				return nil, ErrFormat
			}
			pcm := make([]int16, n)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(b[body+2*i:]))
			}
			return pcm, nil
		}
		p = body + size + size%2
	}
	return nil, ErrFormat
}

// Resample converts mono float samples in [-1, 1] at rate Hz to 16-bit PCM at SampleRate. Going down it
// averages the input samples that fall into each output sample (a box filter, enough to keep a speech
// band clean of aliasing for checks and recognisers); going up it interpolates. Values outside [-1, 1]
// are clipped. The result is at most MaxSamples long.
func Resample(in []float32, rate int) []int16 {
	if rate <= 0 || len(in) == 0 {
		return nil
	}
	n := int(int64(len(in)) * SampleRate / int64(rate))
	if n > MaxSamples {
		n = MaxSamples
	}
	out := make([]int16, n)
	ratio := float64(rate) / SampleRate
	for i := range out {
		var v float64
		if ratio > 1 {
			lo, hi := int(float64(i)*ratio), int(float64(i+1)*ratio)
			if hi > len(in) {
				hi = len(in)
			}
			if hi <= lo {
				hi = lo + 1
			}
			var sum float64
			for _, s := range in[lo:hi] {
				sum += finite(s)
			}
			v = sum / float64(hi-lo)
		} else {
			pos := float64(i) * ratio
			j := int(pos)
			frac := pos - float64(j)
			a := finite(in[j])
			bv := a
			if j+1 < len(in) {
				bv = finite(in[j+1])
			}
			v = a + (bv-a)*frac
		}
		out[i] = toInt16(v)
	}
	return out
}

func toInt16(v float64) int16 {
	if math.IsNaN(v) {
		return 0
	}
	v = math.Max(-1, math.Min(1, v))
	return int16(math.Round(v * 32767))
}

// finite is v as a float64, 0 for NaN and infinities.
func finite(v float32) float64 {
	f := float64(v)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}
