package speechmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
)

// An Adapter is the small set of weights that makes a shared base model recognise one person: low-rank
// (LoRA-style) matrices per adapted layer. It is the only thing that is trained on a device and the only
// thing that can leave it, as an Update.
type Adapter struct {
	Base   string // SHA-256 (hex) of the base model file the adapter was made for
	Rank   int
	Layers []Tensor
}

// Tensor is one named block of weights.
type Tensor struct {
	Name  string
	Shape []int
	Data  []float32
}

// Limits on what is read back.
const (
	maxLayers      = 4096
	maxTensorElems = 1 << 26 // 64M floats in one tensor
	maxAdapterElem = 1 << 27
	maxDims        = 8
)

var adapterMagic = []byte("GJAD1")

// ErrBadAdapter is returned for bytes that are not an adapter this package wrote.
var ErrBadAdapter = errors.New("speechmodel: the adapter cannot be read")

func elems(shape []int) (int, bool) {
	n := 1
	for _, d := range shape {
		if d <= 0 || d > maxTensorElems {
			return 0, false
		}
		n *= d
		if n > maxTensorElems {
			return 0, false
		}
	}
	return n, len(shape) > 0
}

// Marshal writes the adapter: a magic, the base digest, the rank, then each tensor, and a SHA-256 over
// all of it, so a changed byte is refused when it is read.
func (a *Adapter) Marshal() ([]byte, error) {
	if len(a.Layers) > maxLayers {
		return nil, fmt.Errorf("%w: too many layers", ErrBadAdapter)
	}
	var b bytes.Buffer
	b.Write(adapterMagic)
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	base, err := hex.DecodeString(a.Base)
	if err != nil || len(base) != 32 {
		return nil, fmt.Errorf("%w: the base digest is not a SHA-256", ErrBadAdapter)
	}
	b.Write(base)
	w(uint32(a.Rank))
	w(uint32(len(a.Layers)))
	for _, t := range a.Layers {
		n, ok := elems(t.Shape)
		if !ok || n != len(t.Data) || len(t.Shape) > maxDims || len(t.Name) > 255 {
			return nil, fmt.Errorf("%w: tensor %q", ErrBadAdapter, t.Name)
		}
		w(uint8(len(t.Name)))
		b.WriteString(t.Name)
		w(uint8(len(t.Shape)))
		for _, d := range t.Shape {
			w(uint32(d))
		}
		w(t.Data)
	}
	sum := sha256.Sum256(b.Bytes())
	b.Write(sum[:])
	return b.Bytes(), nil
}

// UnmarshalAdapter reads what Marshal wrote.
func UnmarshalAdapter(data []byte) (*Adapter, error) {
	if len(data) < len(adapterMagic)+32+8+32 || !bytes.HasPrefix(data, adapterMagic) {
		return nil, ErrBadAdapter
	}
	body, sum := data[:len(data)-32], data[len(data)-32:]
	if got := sha256.Sum256(body); !bytes.Equal(got[:], sum) {
		return nil, ErrBadAdapter
	}
	r := bytes.NewReader(body[len(adapterMagic):])
	a := &Adapter{}
	base := make([]byte, 32)
	if _, err := r.Read(base); err != nil {
		return nil, ErrBadAdapter
	}
	a.Base = hex.EncodeToString(base)
	var rank, n uint32
	if binary.Read(r, binary.LittleEndian, &rank) != nil || binary.Read(r, binary.LittleEndian, &n) != nil || n > maxLayers {
		return nil, ErrBadAdapter
	}
	a.Rank = int(rank)
	total := 0
	for range n {
		var nl, nd uint8
		if binary.Read(r, binary.LittleEndian, &nl) != nil {
			return nil, ErrBadAdapter
		}
		name := make([]byte, nl)
		if _, err := r.Read(name); err != nil && nl > 0 {
			return nil, ErrBadAdapter
		}
		if binary.Read(r, binary.LittleEndian, &nd) != nil || nd == 0 || int(nd) > maxDims {
			return nil, ErrBadAdapter
		}
		shape := make([]int, nd)
		for i := range shape {
			var d uint32
			if binary.Read(r, binary.LittleEndian, &d) != nil {
				return nil, ErrBadAdapter
			}
			shape[i] = int(d)
		}
		cnt, ok := elems(shape)
		if !ok || int64(cnt)*4 > int64(r.Len()) {
			return nil, ErrBadAdapter
		}
		total += cnt
		if total > maxAdapterElem {
			return nil, ErrBadAdapter
		}
		data := make([]float32, cnt)
		if binary.Read(r, binary.LittleEndian, data) != nil {
			return nil, ErrBadAdapter
		}
		a.Layers = append(a.Layers, Tensor{Name: string(name), Shape: shape, Data: data})
	}
	if r.Len() != 0 {
		return nil, ErrBadAdapter
	}
	return a, nil
}

// An Update is the change a device makes to the shared adapter: tuned minus the global one, layer by layer.
// It is what a device may contribute (with consent), after Clip and AddNoise.
type Update struct {
	Base   string
	Layers []Tensor
	// Weight is how much this update counts in Aggregate (the number of examples it was trained on, or 1).
	Weight float64
}

// Delta is the update that turns global into tuned. The two must be for the same base model and have the
// same layers.
func Delta(global, tuned *Adapter) (*Update, error) {
	if global.Base != tuned.Base {
		return nil, ErrWrongBase
	}
	if len(global.Layers) != len(tuned.Layers) {
		return nil, fmt.Errorf("speechmodel: the adapters have different layers")
	}
	u := &Update{Base: global.Base, Weight: 1}
	for i, g := range global.Layers {
		t := tuned.Layers[i]
		if g.Name != t.Name || len(g.Data) != len(t.Data) {
			return nil, fmt.Errorf("speechmodel: layer %q does not match", g.Name)
		}
		d := make([]float32, len(g.Data))
		for j := range d {
			d[j] = t.Data[j] - g.Data[j]
		}
		u.Layers = append(u.Layers, Tensor{Name: g.Name, Shape: append([]int(nil), g.Shape...), Data: d})
	}
	return u, nil
}

// Norm is the L2 norm of the whole update.
func (u *Update) Norm() float64 {
	var s float64
	for _, l := range u.Layers {
		for _, v := range l.Data {
			s += float64(v) * float64(v)
		}
	}
	return math.Sqrt(s)
}

// Clip scales the update down so its norm is at most max. One person's update can then move the shared
// adapter only so far, which bounds what any update can reveal or break.
func (u *Update) Clip(max float64) {
	n := u.Norm()
	if n <= max || n == 0 || max <= 0 {
		return
	}
	k := float32(max / n)
	for _, l := range u.Layers {
		for i := range l.Data {
			l.Data[i] *= k
		}
	}
}

// AddNoise adds Gaussian noise of standard deviation sigma to every weight. With Clip(max) and sigma
// proportional to max this is the Gaussian mechanism; this package does not compute the resulting
// (epsilon, delta), and a deployment that claims one must.
func (u *Update) AddNoise(sigma float64, rng *rand.Rand) {
	if sigma <= 0 {
		return
	}
	for _, l := range u.Layers {
		for i := range l.Data {
			l.Data[i] += float32(rng.NormFloat64() * sigma)
		}
	}
}

// Aggregate is the weighted mean of updates for the same base model and layers (federated averaging).
func Aggregate(updates []*Update) (*Update, error) {
	if len(updates) == 0 {
		return nil, errors.New("speechmodel: no updates")
	}
	first := updates[0]
	out := &Update{Base: first.Base, Weight: 0}
	for _, l := range first.Layers {
		out.Layers = append(out.Layers, Tensor{Name: l.Name, Shape: append([]int(nil), l.Shape...), Data: make([]float32, len(l.Data))})
	}
	var total float64
	for _, u := range updates {
		if u.Base != first.Base {
			return nil, ErrWrongBase
		}
		if len(u.Layers) != len(first.Layers) {
			return nil, errors.New("speechmodel: the updates have different layers")
		}
		w := u.Weight
		if w <= 0 {
			w = 1
		}
		total += w
		for i, l := range u.Layers {
			if l.Name != first.Layers[i].Name || len(l.Data) != len(first.Layers[i].Data) {
				return nil, fmt.Errorf("speechmodel: layer %q does not match", l.Name)
			}
			for j, v := range l.Data {
				out.Layers[i].Data[j] += float32(w) * v
			}
		}
	}
	for _, l := range out.Layers {
		for j := range l.Data {
			l.Data[j] /= float32(total)
		}
	}
	out.Weight = total
	return out, nil
}

// ApplyUpdate moves the global adapter by lr times the update (lr 1 adds it whole) and returns the new one.
func ApplyUpdate(global *Adapter, u *Update, lr float64) (*Adapter, error) {
	if global.Base != u.Base {
		return nil, ErrWrongBase
	}
	if len(global.Layers) != len(u.Layers) {
		return nil, errors.New("speechmodel: the update has different layers")
	}
	out := &Adapter{Base: global.Base, Rank: global.Rank}
	for i, g := range global.Layers {
		if g.Name != u.Layers[i].Name || len(g.Data) != len(u.Layers[i].Data) {
			return nil, fmt.Errorf("speechmodel: layer %q does not match", g.Name)
		}
		d := make([]float32, len(g.Data))
		for j := range d {
			d[j] = g.Data[j] + float32(lr)*u.Layers[i].Data[j]
		}
		out.Layers = append(out.Layers, Tensor{Name: g.Name, Shape: append([]int(nil), g.Shape...), Data: d})
	}
	return out, nil
}
