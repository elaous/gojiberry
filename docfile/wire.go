package docfile

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docfile/internal/wire"
)

func marshalWire(m *message, v reflect.Value) []byte {
	var b []byte
	for i := range m.fields {
		f := &m.fields[i]
		fv := v.Field(f.index)
		if f.repeated {
			b = appendList(b, f, fv)
			continue
		}
		if f.kind == kMsg {
			if f.pointer {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			} else if fv.IsZero() {
				continue
			}
			b = wire.AppendTag(b, wire.Number(f.num), wire.BytesType)
			b = wire.AppendBytes(b, marshalWire(f.msg, fv))
			continue
		}
		if fv.IsZero() {
			continue
		}
		b = wire.AppendTag(b, wire.Number(f.num), scalarWire(f.kind))
		b = appendScalar(b, f.kind, fv)
	}
	return b
}

func scalarWire(k kind) wire.Type {
	switch k {
	case kFloat:
		return wire.Fixed64Type
	case kString, kMsg:
		return wire.BytesType
	}
	return wire.VarintType
}

func appendScalar(b []byte, k kind, v reflect.Value) []byte {
	switch k {
	case kString:
		return wire.AppendString(b, v.String())
	case kBool:
		if v.Bool() {
			return wire.AppendVarint(b, 1)
		}
		return wire.AppendVarint(b, 0)
	case kFloat:
		return wire.AppendFixed64(b, math.Float64bits(v.Float()))
	}
	return wire.AppendVarint(b, uint64(v.Int()))
}

func appendList(b []byte, f *field, fv reflect.Value) []byte {
	n := fv.Len()
	if n == 0 {
		return b
	}
	switch f.kind {
	case kMsg:
		for i := range n {
			e := fv.Index(i)
			if e.Kind() == reflect.Pointer {
				e = e.Elem()
			}
			b = wire.AppendTag(b, wire.Number(f.num), wire.BytesType)
			b = wire.AppendBytes(b, marshalWire(f.msg, e))
		}
	case kString:
		for i := range n {
			b = wire.AppendTag(b, wire.Number(f.num), wire.BytesType)
			b = wire.AppendString(b, fv.Index(i).String())
		}
	default: // numbers and bools are packed
		var payload []byte
		for i := range n {
			payload = appendScalar(payload, f.kind, fv.Index(i))
		}
		b = wire.AppendTag(b, wire.Number(f.num), wire.BytesType)
		b = wire.AppendBytes(b, payload)
	}
	return b
}

var errWire = errors.New("docfile: malformed protobuf data")

func unmarshalWire(m *message, b []byte, v reflect.Value) error {
	for len(b) > 0 {
		num, typ, n := wire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("%w: %w", errWire, wire.ParseError(n))
		}
		b = b[n:]
		f := m.byNum[int32(num)]
		var used int
		var err error
		switch {
		case f == nil:
			used = wire.ConsumeFieldValue(num, typ, b)
		case f.kind == kMsg && typ == wire.BytesType:
			var sub []byte
			if sub, used = wire.ConsumeBytes(b); used >= 0 {
				err = readMessage(f, sub, v.Field(f.index))
			}
		case f.repeated && f.kind != kMsg && f.kind != kString && typ == wire.BytesType:
			var payload []byte
			if payload, used = wire.ConsumeBytes(b); used >= 0 {
				for len(payload) > 0 {
					c, e := readScalar(f.kind, scalarWire(f.kind), payload, v.Field(f.index), true)
					if e != nil {
						return e
					}
					payload = payload[c:]
				}
			}
		case f.kind != kMsg && typ == scalarWire(f.kind):
			used, err = readScalar(f.kind, typ, b, v.Field(f.index), f.repeated)
		default:
			used = wire.ConsumeFieldValue(num, typ, b)
		}
		if err != nil {
			return err
		}
		if used < 0 {
			return fmt.Errorf("%w: %w", errWire, wire.ParseError(used))
		}
		b = b[used:]
	}
	return nil
}

func readMessage(f *field, sub []byte, fv reflect.Value) error {
	if f.repeated {
		fv.Set(reflect.Append(fv, reflect.New(fv.Type().Elem()).Elem()))
		fv = fv.Index(fv.Len() - 1)
	}
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}
	return unmarshalWire(f.msg, sub, fv)
}

func readScalar(k kind, typ wire.Type, b []byte, fv reflect.Value, list bool) (int, error) {
	dst := fv
	if list {
		fv.Set(reflect.Append(fv, reflect.New(fv.Type().Elem()).Elem()))
		dst = fv.Index(fv.Len() - 1)
	}
	var n int
	switch k {
	case kString:
		var s string
		s, n = wire.ConsumeString(b)
		if n >= 0 && !utf8.ValidString(s) {
			return 0, fmt.Errorf("%w: a string that is not UTF-8", errWire)
		}
		dst.SetString(s)
	case kBool:
		var x uint64
		x, n = wire.ConsumeVarint(b)
		dst.SetBool(x != 0)
	case kFloat:
		var x uint64
		x, n = wire.ConsumeFixed64(b)
		dst.SetFloat(math.Float64frombits(x))
	default:
		var x uint64
		x, n = wire.ConsumeVarint(b)
		dst.SetInt(int64(x))
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %w", errWire, wire.ParseError(n))
	}
	_ = typ
	return n, nil
}
