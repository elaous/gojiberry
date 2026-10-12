package speechcoach

import (
	"math"
	"sort"
)

// Hint codes: what the signal checks can say about a take. They are advice to the speaker, never a
// refusal: any take can be kept. The interface turns a code into a short, kind sentence.
const (
	HintSilent    = "silent"     // nothing but silence or a very low hum was heard
	HintTooShort  = "too_short"  // very little sound; a phrase usually takes a second or more
	HintTooQuiet  = "too_quiet"  // the voice is faint; move closer or speak up if that is comfortable
	HintClipping  = "clipping"   // the loudest parts were cut off (too close or too loud)
	HintNoisy     = "noisy"      // the background is loud against the voice
	HintLongPause = "long_pause" // a long quiet stretch in the middle; fine if it was needed
)

// Quality is what simple signal analysis found in a take. The levels are in dB relative to full scale
// (so 0 is the loudest possible and typical speech is around -30 to -15).
type Quality struct {
	Seconds        float64 `json:"seconds"`
	PeakDB         float64 `json:"peak_db"`
	SpeechRMSDB    float64 `json:"speech_rms_db"`   // mean level of the frames that hold sound; -120 when none
	NoiseFloorDB   float64 `json:"noise_floor_db"`  // level of the quietest tenth of the frames
	SpeechSeconds  float64 `json:"speech_seconds"`  // time in frames that hold sound
	ClippedPercent float64 `json:"clipped_percent"` // share of samples at or near full scale
	LongestPause   float64 `json:"longest_pause"`   // longest quiet stretch between sounds, seconds
}

const (
	frame      = SampleRate / 50 // 20 ms
	floorDB    = -120.0
	clipLevel  = 32000 // |sample| at or above this counts as clipped (about -0.2 dBFS)
	minDB      = -50.0 // frames quieter than this never count as sound
	activeOver = 9.0   // dB above the noise floor a frame must be to count as sound
)

func db(x float64) float64 {
	if x <= 1e-6 {
		return floorDB
	}
	return 20 * math.Log10(x)
}

// Analyze measures a take. It is linear in the length and allocates one slice per 20 ms frame.
func Analyze(pcm []int16) Quality {
	q := Quality{Seconds: float64(len(pcm)) / SampleRate, PeakDB: floorDB, SpeechRMSDB: floorDB, NoiseFloorDB: floorDB}
	if len(pcm) == 0 {
		return q
	}
	var peak, clipped int
	for _, s := range pcm {
		a := int(s)
		if a < 0 {
			a = -a
		}
		if a > peak {
			peak = a
		}
		if a >= clipLevel {
			clipped++
		}
	}
	q.PeakDB = db(float64(peak) / 32768)
	q.ClippedPercent = 100 * float64(clipped) / float64(len(pcm))

	var levels []float64 // per frame RMS in dB
	for i := 0; i < len(pcm); i += frame {
		end := i + frame
		if end > len(pcm) {
			end = len(pcm)
		}
		var sum float64
		for _, s := range pcm[i:end] {
			f := float64(s) / 32768
			sum += f * f
		}
		levels = append(levels, db(math.Sqrt(sum/float64(end-i))))
	}
	sorted := append([]float64(nil), levels...)
	sort.Float64s(sorted)
	q.NoiseFloorDB = sorted[len(sorted)/10]
	threshold := math.Max(minDB, q.NoiseFloorDB+activeOver)
	if threshold >= sorted[len(sorted)-1] {
		// no quiet stretch to compare with (a held sound, or a take that is all sound): the floor is
		// unknown, so every frame above the absolute minimum counts as sound
		q.NoiseFloorDB, threshold = floorDB, minDB
	}

	var sum float64
	var active int
	pause, longest, seen := 0, 0, false
	for _, l := range levels {
		if l > threshold {
			sum += math.Pow(10, l/10)
			active++
			if seen && pause > longest {
				longest = pause
			}
			pause, seen = 0, true
		} else if seen {
			pause++
		}
	}
	q.SpeechSeconds = float64(active) * frame / SampleRate
	q.LongestPause = float64(longest) * frame / SampleRate
	if active > 0 {
		q.SpeechRMSDB = 10 * math.Log10(sum/float64(active))
	}
	return q
}

// Hints are the gentle notes about a take, most useful first; none when it looks fine.
func (q Quality) Hints() []string {
	var h []string
	switch {
	case q.SpeechSeconds == 0 || q.PeakDB < -55:
		return []string{HintSilent}
	case q.Seconds < 0.6 || q.SpeechSeconds < 0.25:
		h = append(h, HintTooShort)
	}
	if q.SpeechRMSDB < -38 || q.PeakDB < -24 {
		h = append(h, HintTooQuiet)
	}
	if q.ClippedPercent >= 0.5 {
		h = append(h, HintClipping)
	}
	if q.NoiseFloorDB > -38 && q.SpeechRMSDB-q.NoiseFloorDB < 12 {
		h = append(h, HintNoisy)
	}
	if q.LongestPause >= 4 {
		h = append(h, HintLongPause)
	}
	return h
}
