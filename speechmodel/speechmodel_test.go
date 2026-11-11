package speechmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestAModelFileIsPinnedByDigest(t *testing.T) {
	file := filepath.Join(t.TempDir(), "m.bin")
	data := []byte("model bytes")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := Open(ctx, Spec{Backend: "stub", Path: file}); !errors.Is(err, ErrNoDigest) {
		t.Errorf("no digest: %v", err)
	}
	if _, err := Open(ctx, Spec{Backend: "stub", Path: file, SHA256: strings.Repeat("0", 64)}); !errors.Is(err, ErrBadDigest) {
		t.Errorf("wrong digest: %v", err)
	}
	if _, err := Open(ctx, Spec{Backend: "stub", Path: file, SHA256: digestOf(data), MaxBytes: 3}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if _, err := Open(ctx, Spec{Backend: "nope"}); !errors.Is(err, ErrNoBackend) {
		t.Errorf("no backend: %v", err)
	}
	m, err := Open(ctx, Spec{Backend: "stub", Path: file, SHA256: strings.ToUpper(digestOf(data))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if m.Info().SHA256 != digestOf(data) || !m.Info().Adaptable {
		t.Errorf("info: %+v", m.Info())
	}
}

func adapterFor(base string, vals ...float32) *Adapter {
	return &Adapter{Base: base, Rank: 2, Layers: []Tensor{{Name: "enc.0.lora_a", Shape: []int{len(vals)}, Data: vals}}}
}

func TestAnAdapterIsRefusedOnAnotherBaseAndWornOnTheRightOne(t *testing.T) {
	base := digestOf([]byte("base"))
	m, _ := Open(context.Background(), Spec{Backend: "stub", SHA256: base, URL: "hello"})
	a := m.(Adaptable)
	if err := a.Apply(adapterFor(digestOf([]byte("other")), 1)); !errors.Is(err, ErrWrongBase) {
		t.Errorf("wrong base: %v", err)
	}
	mine := adapterFor(base, 1, 2)
	if err := a.Apply(mine); err != nil || m.(*Stub).Adapter() != mine {
		t.Errorf("apply: %v", err)
	}
	if err := a.Apply(nil); err != nil || m.(*Stub).Adapter() != nil {
		t.Errorf("take off: %v", err)
	}
	if r, err := m.Transcribe(context.Background(), []int16{1}, Options{}); err != nil || r.Text != "hello" {
		t.Errorf("transcribe: %v %v", r, err)
	}
	_ = m.Close()
	if _, err := m.Transcribe(context.Background(), []int16{1}, Options{}); err == nil {
		t.Error("a closed model refuses")
	}
}

func TestAnAdapterRoundTripsAndATamperedOneIsRefused(t *testing.T) {
	a := adapterFor(digestOf([]byte("base")), 0.5, -1.25, 3)
	a.Layers = append(a.Layers, Tensor{Name: "b", Shape: []int{2, 2}, Data: []float32{1, 2, 3, 4}})
	b, err := a.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnmarshalAdapter(b)
	if err != nil || back.Base != a.Base || back.Rank != 2 || len(back.Layers) != 2 || back.Layers[1].Data[3] != 4 || back.Layers[0].Name != "enc.0.lora_a" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	for i := range b {
		c := append([]byte(nil), b...)
		c[i] ^= 1
		if _, err := UnmarshalAdapter(c); err == nil {
			t.Fatalf("a changed byte %d was accepted", i)
		}
	}
	if _, err := UnmarshalAdapter(b[:len(b)-1]); err == nil {
		t.Error("a truncated adapter was accepted")
	}
	if _, err := UnmarshalAdapter([]byte("GJAD1")); err == nil {
		t.Error("a short adapter was accepted")
	}
	bad := &Adapter{Base: "zz", Layers: nil}
	if _, err := bad.Marshal(); err == nil {
		t.Error("a bad base digest is refused")
	}
	if _, err := (&Adapter{Base: a.Base, Layers: []Tensor{{Name: "x", Shape: []int{3}, Data: []float32{1}}}}).Marshal(); err == nil {
		t.Error("a tensor whose data does not fit its shape is refused")
	}
}

func TestFederatedAveragingWithClippingAndNoise(t *testing.T) {
	base := digestOf([]byte("base"))
	global := adapterFor(base, 0, 0)
	u1, err := Delta(global, adapterFor(base, 3, 4)) // norm 5
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := Delta(global, adapterFor(base, 1, 1))
	u1.Weight, u2.Weight = 1, 3

	u1.Clip(1)
	if n := u1.Norm(); math.Abs(n-1) > 1e-6 {
		t.Errorf("clipped norm %v", n)
	}
	small := *u2
	small.Layers = []Tensor{{Name: u2.Layers[0].Name, Shape: u2.Layers[0].Shape, Data: append([]float32(nil), u2.Layers[0].Data...)}}
	small.Clip(100)
	if small.Layers[0].Data[0] != 1 {
		t.Error("an update under the bound is not changed")
	}

	avg, err := Aggregate([]*Update{u1, u2})
	if err != nil {
		t.Fatal(err)
	}
	if d := avg.Layers[0].Data; math.Abs(float64(d[0])-0.9) > 1e-5 || math.Abs(float64(d[1])-0.95) > 1e-5 || avg.Weight != 4 {
		t.Errorf("aggregate: %v %v", d, avg.Weight)
	}
	next, err := ApplyUpdate(global, avg, 0.5)
	if err != nil || math.Abs(float64(next.Layers[0].Data[0])-0.45) > 1e-5 || global.Layers[0].Data[0] != 0 {
		t.Errorf("apply: %v %v (the global adapter must not change in place)", next, err)
	}

	z := &Update{Base: base, Layers: []Tensor{{Name: "n", Shape: []int{10000}, Data: make([]float32, 10000)}}}
	z.AddNoise(0, rand.New(rand.NewPCG(1, 2)))
	if z.Norm() != 0 {
		t.Error("sigma 0 adds nothing")
	}
	z.AddNoise(2, rand.New(rand.NewPCG(1, 2)))
	var sum, sq float64
	for _, v := range z.Layers[0].Data {
		sum += float64(v)
		sq += float64(v) * float64(v)
	}
	if mean, sd := sum/10000, math.Sqrt(sq/10000); math.Abs(mean) > 0.1 || math.Abs(sd-2) > 0.1 {
		t.Errorf("noise mean %v sd %v", mean, sd)
	}
}

func TestUpdatesForAnotherModelOrShapeAreRefused(t *testing.T) {
	a, b := digestOf([]byte("a")), digestOf([]byte("b"))
	if _, err := Delta(adapterFor(a, 1), adapterFor(b, 1)); !errors.Is(err, ErrWrongBase) {
		t.Errorf("delta: %v", err)
	}
	if _, err := Delta(adapterFor(a, 1), adapterFor(a, 1, 2)); err == nil {
		t.Error("different sizes")
	}
	ua, _ := Delta(adapterFor(a, 0), adapterFor(a, 1))
	ub, _ := Delta(adapterFor(b, 0), adapterFor(b, 1))
	if _, err := Aggregate([]*Update{ua, ub}); !errors.Is(err, ErrWrongBase) {
		t.Errorf("aggregate: %v", err)
	}
	if _, err := Aggregate(nil); err == nil {
		t.Error("no updates")
	}
	if _, err := ApplyUpdate(adapterFor(b, 0), ua, 1); !errors.Is(err, ErrWrongBase) {
		t.Errorf("apply: %v", err)
	}
}

func TestTheHTTPBackendSendsAudioOnlyToItsAddress(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		got = r.FormValue("prompt") + "|" + r.FormValue("language")
		_, _ = w.Write([]byte(`{"text":" good morning "}`))
	}))
	defer srv.Close()
	m, err := Open(context.Background(), Spec{Backend: "http", URL: srv.URL + "/transcribe"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Transcribe(context.Background(), make([]int16, 160), Options{Prompt: "names", Language: "en"})
	if err != nil || res.Text != "good morning" || got != "names|en" {
		t.Errorf("%v %v %q", res, err, got)
	}
	if _, ok := m.(Adaptable); ok {
		t.Error("the http model takes no adapter")
	}
	if _, err := Open(context.Background(), Spec{Backend: "http", URL: "http://example.com/x"}); err == nil {
		t.Error("a plain http address that is not local is refused")
	}
	if got := strings.Join(Backends(), ","); got != "http,stub" {
		t.Errorf("backends %s", got)
	}
}
