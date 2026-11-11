// Package wire implements the protobuf wire format subset docfile uses.
package wire

import (
	"errors"
	"io"
)

// Number is a field number.
type Number int32

const (
	MinValidNumber Number = 1
	MaxValidNumber Number = 1<<29 - 1
)

// Type is a wire type.
type Type int8

const (
	VarintType     Type = 0
	Fixed64Type    Type = 1
	BytesType      Type = 2
	StartGroupType Type = 3
	EndGroupType   Type = 4
	Fixed32Type    Type = 5
)

// MaxDepth is the deepest allowed group nesting.
const MaxDepth = 10000

// Error codes returned, negated, by the consume functions.
const (
	_ = -iota
	errCodeTruncated
	errCodeFieldNumber
	errCodeOverflow
	errCodeReserved
	errCodeEndGroup
	errCodeDepth
)

var (
	errFieldNumber = errors.New("invalid field number")
	errOverflow    = errors.New("variable length integer overflow")
	errReserved    = errors.New("cannot parse reserved wire type")
	errEndGroup    = errors.New("mismatching end group marker")
	errDepth       = errors.New("exceeded maximum group nesting depth")
	errParse       = errors.New("parse error")
)

// ParseError turns a negative code into an error.
func ParseError(n int) error {
	if n >= 0 {
		return nil
	}
	switch n {
	case errCodeTruncated:
		return io.ErrUnexpectedEOF
	case errCodeFieldNumber:
		return errFieldNumber
	case errCodeOverflow:
		return errOverflow
	case errCodeReserved:
		return errReserved
	case errCodeEndGroup:
		return errEndGroup
	case errCodeDepth:
		return errDepth
	}
	return errParse
}

// AppendVarint appends v as a varint.
func AppendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ConsumeVarint reads a varint at the start of b.
func ConsumeVarint(b []byte) (v uint64, n int) {
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, errCodeTruncated
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, errCodeOverflow
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return v, i + 1
		}
	}
	return 0, errCodeOverflow // the tenth byte continues
}

// AppendFixed32 appends v little-endian.
func AppendFixed32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// ConsumeFixed32 reads a little-endian uint32.
func ConsumeFixed32(b []byte) (v uint32, n int) {
	if len(b) < 4 {
		return 0, errCodeTruncated
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, 4
}

// AppendFixed64 appends v little-endian.
func AppendFixed64(b []byte, v uint64) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// ConsumeFixed64 reads a little-endian uint64.
func ConsumeFixed64(b []byte) (v uint64, n int) {
	if len(b) < 8 {
		return 0, errCodeTruncated
	}
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, 8
}

// AppendBytes appends v with its length prefix.
func AppendBytes(b []byte, v []byte) []byte {
	return append(AppendVarint(b, uint64(len(v))), v...)
}

// AppendString appends s with its length prefix.
func AppendString(b []byte, s string) []byte {
	return append(AppendVarint(b, uint64(len(s))), s...)
}

// ConsumeBytes reads a length-prefixed byte slice.
func ConsumeBytes(b []byte) (v []byte, n int) {
	l, n := ConsumeVarint(b)
	if n < 0 {
		return nil, n
	}
	if l > uint64(len(b)-n) {
		return nil, errCodeTruncated
	}
	return b[n : n+int(l)], n + int(l)
}

// ConsumeString reads a length-prefixed string.
func ConsumeString(b []byte) (s string, n int) {
	v, n := ConsumeBytes(b)
	return string(v), n
}

// EncodeTag packs a field number and wire type.
func EncodeTag(num Number, typ Type) uint64 {
	return uint64(num)<<3 | uint64(typ&7)
}

// DecodeTag unpacks a tag into number and type.
func DecodeTag(x uint64) (Number, Type) {
	if x>>3 > 1<<31-1 {
		return -1, 0
	}
	return Number(x >> 3), Type(x & 7)
}

// AppendTag appends a field's tag.
func AppendTag(b []byte, num Number, typ Type) []byte {
	return AppendVarint(b, EncodeTag(num, typ))
}

// ConsumeTag reads a tag.
func ConsumeTag(b []byte) (Number, Type, int) {
	v, n := ConsumeVarint(b)
	if n < 0 {
		return 0, 0, n
	}
	num, typ := DecodeTag(v)
	if num < MinValidNumber {
		return 0, 0, errCodeFieldNumber
	}
	return num, typ, n
}

// ConsumeFieldValue skips the value of a field.
func ConsumeFieldValue(num Number, typ Type, b []byte) int {
	return consumeFieldValue(num, typ, b, MaxDepth)
}

func consumeFieldValue(num Number, typ Type, b []byte, depth int) int {
	switch typ {
	case VarintType:
		_, n := ConsumeVarint(b)
		return n
	case Fixed32Type:
		_, n := ConsumeFixed32(b)
		return n
	case Fixed64Type:
		_, n := ConsumeFixed64(b)
		return n
	case BytesType:
		_, n := ConsumeBytes(b)
		return n
	case StartGroupType:
		if depth < 0 {
			return errCodeDepth
		}
		n0 := len(b)
		for {
			num2, typ2, n := ConsumeTag(b)
			if n < 0 {
				return n
			}
			b = b[n:]
			if typ2 == EndGroupType {
				if num != num2 {
					return errCodeEndGroup
				}
				return n0 - len(b)
			}
			n = consumeFieldValue(num2, typ2, b, depth-1)
			if n < 0 {
				return n
			}
			b = b[n:]
		}
	case EndGroupType:
		return errCodeEndGroup
	}
	return errCodeReserved
}
