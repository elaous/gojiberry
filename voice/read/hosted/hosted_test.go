package hosted

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/voice/read/piper"
)

func wav16(rate int, samples ...int16) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint32(b, uint32(rate))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate*2))
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(samples)*2))
	for _, s := range samples {
		b = binary.LittleEndian.AppendUint16(b, uint16(s))
	}
	return b
}

type client struct {
	list      string
	wav       []byte
	err       error
	gotVoice  string
	gotRate   float64
	cancelled bool
}

func (c *client) Voices(_ context.Context, done func([]byte, error)) { done([]byte(c.list), c.err) }
func (c *client) Speech(_ context.Context, voice, _ string, rate float64, done func([]byte, error)) func() {
	c.gotVoice, c.gotRate = voice, rate
	done(c.wav, c.err)
	return func() { c.cancelled = true }
}

type out struct {
	played piper.Audio
	end    func()
	stops  int
}

func (o *out) Play(a piper.Audio, onTime func(time.Duration), onEnd func()) piper.Playback {
	o.played, o.end = a, onEnd
	onTime(a.Duration() / 2)
	return o
}
func (o *out) Stop()   { o.stops++ }
func (o *out) Pause()  {}
func (o *out) Resume() {}

func TestDecodeWAVReadsPCMAndRefusesTheRest(t *testing.T) {
	a, err := DecodeWAV(wav16(22050, 0, 16384, -32768))
	if err != nil || a.SampleRate != 22050 || len(a.Samples) != 3 || a.Samples[1] != 0.5 || a.Samples[2] != -1 {
		t.Fatalf("decoded %+v, %v", a, err)
	}
	for name, b := range map[string][]byte{"empty": nil, "not riff": []byte("OggS0000000000000000"), "no data": wav16(8000)[:36], "no samples": wav16(8000)} {
		if _, err := DecodeWAV(b); !errors.Is(err, ErrBadSound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEngineListsServerVoicesAsNotLocalAndSpeaksWithTheOnePicked(t *testing.T) {
	c := &client{list: `[{"id":"a","name":"Ada","lang":"en-GB","licence":"https://example.org/t"},{"id":"b","lang":"de-DE","licence":"http://bad"},{"name":"no id"}]`, wav: wav16(8000, 1, 2, 3, 4)}
	o := &out{}
	e := New(c, o)
	var loadErr error
	e.Load(context.Background(), func(err error) { loadErr = err })
	vs := e.Voices()
	if loadErr != nil || len(vs) != 2 || vs[0].Local || vs[1].Name != "b" || e.Licence("a") != "https://example.org/t" || e.Licence("b") != "" {
		t.Fatalf("voices %+v err %v", vs, loadErr)
	}
	var kind string
	var words, ended int
	e.Speak(read.Utterance{Text: "hello big world", Lang: "de", Rate: 1.25}, read.Handler{
		OnStart: func(k string) { kind = k }, OnWord: func(int, int) { words++ }, OnEnd: func() { ended++ }})
	if c.gotVoice != "b" || c.gotRate != 1.25 || kind != read.KindNetwork || words == 0 {
		t.Errorf("voice %q rate %v kind %q words %d", c.gotVoice, c.gotRate, kind, words)
	}
	o.end()
	if ended != 1 {
		t.Error("end")
	}
}

func TestSpeakReportsAFailureAndCancelStopsEverything(t *testing.T) {
	c := &client{err: errors.New("503")}
	e := New(c, &out{})
	var got error
	e.Speak(read.Utterance{Text: "x"}, read.Handler{OnError: func(err error) { got = err }})
	if got == nil {
		t.Error("an error from the server is reported")
	}
	c2 := &client{wav: wav16(8000, 1, 2)}
	o := &out{}
	called := false
	sp := New(c2, o).Speak(read.Utterance{Text: "x"}, read.Handler{OnEnd: func() { called = true }})
	sp.Cancel()
	o.end()
	if !c2.cancelled || o.stops != 1 || called {
		t.Errorf("cancel: request %v stops %d end %v", c2.cancelled, o.stops, called)
	}
	if New(nil, o).Available() {
		t.Error("no client, not available")
	}
}
