package speechcoach

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

func tone(secs, amp, noise float64) []int16 {
	r := rand.New(rand.NewSource(1))
	n := int(secs * SampleRate)
	out := make([]int16, n)
	for i := range out {
		env := math.Max(0, math.Sin(2*math.Pi*1.5*float64(i)/SampleRate)) * 1.4
		if env > 1 {
			env = 1
		}
		v := amp*env*math.Sin(2*math.Pi*220*float64(i)/SampleRate) + noise*(r.Float64()*2-1)
		out[i] = toInt16(v)
	}
	return out
}

func TestWAVRoundTripAndRefusals(t *testing.T) {
	pcm := tone(0.5, 0.5, 0)
	b := EncodeWAV(pcm)
	if len(b) != 44+2*len(pcm) || string(b[:4]) != "RIFF" {
		t.Fatalf("header: %d bytes", len(b))
	}
	got, err := DecodeWAV(b)
	if err != nil || !slices.Equal(got, pcm) {
		t.Fatalf("round trip: %v", err)
	}
	extra := append(append(append([]byte{}, b[:36]...), 'L', 'I', 'S', 'T', 2, 0, 0, 0, 'x', 'y'), b[36:]...)
	if got, err := DecodeWAV(extra); err != nil || len(got) != len(pcm) {
		t.Fatalf("with a LIST chunk: %v", err)
	}
	for name, bad := range map[string][]byte{
		"empty":  nil,
		"text":   []byte("not a wav at all, just text"),
		"rate":   append(append([]byte{}, b[:24]...), append([]byte{0x44, 0xac, 0, 0}, b[28:]...)...), // 44100
		"stereo": append(append([]byte{}, b[:22]...), append([]byte{2, 0}, b[24:]...)...),
		"cut":    b[:30],
	} {
		if _, err := DecodeWAV(bad); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if got, err := DecodeWAV(b[:44+10]); err != nil || len(got) != 5 {
		t.Errorf("stream cut: %d, %v", len(got), err)
	}
}

func TestResample(t *testing.T) {
	in := make([]float32, 48000)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(2*math.Pi*200*float64(i)/48000))
	}
	out := Resample(in, 48000)
	if len(out) != SampleRate {
		t.Fatalf("len %d", len(out))
	}
	if q := Analyze(out); q.PeakDB < -6.5 || q.PeakDB > -5.5 {
		t.Errorf("peak %.1f dB, want about -6", q.PeakDB)
	}
	if got := Resample(make([]float32, 8000), 8000); len(got) != SampleRate {
		t.Errorf("up: %d", len(got))
	}
	if got := Resample([]float32{5, -5, float32(math.NaN())}, 16000); got[0] != 32767 || got[1] != -32767 || got[2] != 0 {
		t.Errorf("clip and NaN: %v", got)
	}
	if Resample(nil, 16000) != nil || Resample([]float32{1}, 0) != nil {
		t.Error("nothing to resample")
	}
}

func TestAnalyzeAndHints(t *testing.T) {
	cases := []struct {
		name string
		pcm  []int16
		want []string
	}{
		{"fine", tone(2, 0.4, 0.002), nil},
		{"silent", make([]int16, SampleRate), []string{HintSilent}},
		{"empty", nil, []string{HintSilent}},
		{"quiet", tone(2, 0.01, 0.0001), []string{HintTooQuiet}},
		{"clipping", tone(2, 3, 0.002), []string{HintClipping}},
		{"short", tone(0.3, 0.4, 0.002), []string{HintTooShort}},
		{"noisy", tone(2, 0.3, 0.12), []string{HintNoisy}},
	}
	for _, c := range cases {
		got := Analyze(c.pcm).Hints()
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: hints %v, want %v (%+v)", c.name, got, c.want, Analyze(c.pcm))
		}
	}
}

func TestLongPauseIsFoundBetweenSounds(t *testing.T) {
	pcm := append(append(tone(1, 0.4, 0.001), make([]int16, 5*SampleRate)...), tone(1, 0.4, 0.001)...)
	q := Analyze(pcm)
	if q.LongestPause < 4.5 || q.LongestPause > 5.5 {
		t.Fatalf("pause %.2f", q.LongestPause)
	}
	if !slices.Contains(q.Hints(), HintLongPause) {
		t.Errorf("hints %v", q.Hints())
	}
	if q := Analyze(append(make([]int16, 6*SampleRate), tone(0.3, 0.4, 0.001)...)); q.LongestPause != 0 {
		t.Errorf("leading silence counted: %.2f", q.LongestPause)
	}
}
