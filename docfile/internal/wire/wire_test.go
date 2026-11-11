package wire

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"
)

func TestVarintVectors(t *testing.T) {
	neg := int64(-1)
	for _, c := range []struct {
		v    uint64
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{1 << 63, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}},
		{uint64(neg), []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	} {
		got := AppendVarint(nil, c.v)
		if !bytes.Equal(got, c.want) {
			t.Errorf("AppendVarint(%d) = % x, want % x", c.v, got, c.want)
		}
		v, n := ConsumeVarint(append(got, 0xaa))
		if v != c.v || n != len(c.want) {
			t.Errorf("ConsumeVarint(% x) = %d, %d", got, v, n)
		}
	}
	// -2 as int64 is a 10-byte varint too.
	if got := AppendVarint(nil, uint64(neg-1)); len(got) != 10 || got[0] != 0xfe {
		t.Errorf("negative int64: % x", got)
	}
}

func TestVarintErrors(t *testing.T) {
	over := bytes.Repeat([]byte{0xff}, 10) // tenth byte continues
	for _, c := range []struct {
		name string
		in   []byte
		want error
	}{
		{"empty", nil, io.ErrUnexpectedEOF},
		{"truncated", []byte{0x80}, io.ErrUnexpectedEOF},
		{"truncated nine", bytes.Repeat([]byte{0xff}, 9), io.ErrUnexpectedEOF},
		{"overlong 11 bytes", append(bytes.Repeat([]byte{0x80}, 10), 0x00), errOverflow},
		{"tenth byte too big", append(bytes.Repeat([]byte{0xff}, 9), 0x02), errOverflow},
		{"tenth byte continues", over, errOverflow},
	} {
		if _, n := ConsumeVarint(c.in); !errors.Is(ParseError(n), c.want) {
			t.Errorf("%s: got %v, want %v", c.name, ParseError(n), c.want)
		}
	}
	if v, n := ConsumeVarint(append(bytes.Repeat([]byte{0x80}, 9), 0x00)); v != 0 || n != 10 {
		t.Errorf("padded zero = %d, %d", v, n)
	}
}

func TestFixedAndBytes(t *testing.T) {
	if got := AppendFixed32(nil, 0x01020304); !bytes.Equal(got, []byte{4, 3, 2, 1}) {
		t.Errorf("fixed32 % x", got)
	}
	if v, n := ConsumeFixed32([]byte{4, 3, 2, 1}); v != 0x01020304 || n != 4 {
		t.Errorf("fixed32 read %x %d", v, n)
	}
	got := AppendFixed64(nil, math.Float64bits(1))
	if !bytes.Equal(got, []byte{0, 0, 0, 0, 0, 0, 0xf0, 0x3f}) {
		t.Errorf("fixed64 % x", got)
	}
	if v, n := ConsumeFixed64(got); math.Float64frombits(v) != 1 || n != 8 {
		t.Errorf("fixed64 read %x %d", v, n)
	}
	if _, n := ConsumeFixed32([]byte{1, 2, 3}); ParseError(n) != io.ErrUnexpectedEOF {
		t.Error("short fixed32")
	}
	if _, n := ConsumeFixed64(make([]byte, 7)); ParseError(n) != io.ErrUnexpectedEOF {
		t.Error("short fixed64")
	}
	// The protobuf guide's example: field 2 holding "testing".
	b := AppendString(AppendTag(nil, 2, BytesType), "testing")
	if !bytes.Equal(b, []byte{0x12, 0x07, 't', 'e', 's', 't', 'i', 'n', 'g'}) {
		t.Errorf("string field % x", b)
	}
	num, typ, n := ConsumeTag(b)
	if num != 2 || typ != BytesType || n != 1 {
		t.Errorf("tag %d %d %d", num, typ, n)
	}
	s, m := ConsumeString(b[n:])
	if s != "testing" || m != 8 {
		t.Errorf("string %q %d", s, m)
	}
	// And field 1 holding varint 150 is 08 96 01.
	if b := AppendVarint(AppendTag(nil, 1, VarintType), 150); !bytes.Equal(b, []byte{0x08, 0x96, 0x01}) {
		t.Errorf("varint field % x", b)
	}
	// A length past the end.
	if _, n := ConsumeBytes([]byte{5, 1, 2}); ParseError(n) != io.ErrUnexpectedEOF {
		t.Error("length past end")
	}
	if _, n := ConsumeBytes(append(AppendVarint(nil, math.MaxUint64), 1)); n >= 0 {
		t.Error("huge length accepted")
	}
	if v, n := ConsumeBytes([]byte{0}); len(v) != 0 || n != 1 {
		t.Error("empty bytes")
	}
}

func TestTags(t *testing.T) {
	if _, _, n := ConsumeTag([]byte{0x00}); ParseError(n) != errFieldNumber {
		t.Error("field number 0")
	}
	if _, _, n := ConsumeTag(AppendVarint(nil, 1<<35)); ParseError(n) != errFieldNumber {
		t.Error("field number out of range")
	}
	num, typ, n := ConsumeTag(AppendTag(nil, MaxValidNumber, Fixed32Type))
	if num != MaxValidNumber || typ != Fixed32Type || n != 5 {
		t.Errorf("max tag %d %d %d", num, typ, n)
	}
	if _, _, n := ConsumeTag(nil); ParseError(n) != io.ErrUnexpectedEOF {
		t.Error("empty tag")
	}
}

func TestConsumeFieldValue(t *testing.T) {
	group := func(num Number, inner ...[]byte) []byte {
		b := AppendTag(nil, num, StartGroupType)
		for _, in := range inner {
			b = append(b, in...)
		}
		return AppendTag(b, num, EndGroupType)
	}
	// ConsumeFieldValue is called after the start-group tag was read.
	body := func(g []byte) []byte { _, _, n := ConsumeTag(g); return g[n:] }
	nested := group(1, AppendVarint(AppendTag(nil, 2, VarintType), 7), group(3, AppendString(AppendTag(nil, 4, BytesType), "x")))
	if n := ConsumeFieldValue(1, StartGroupType, body(nested)); n != len(body(nested)) {
		t.Errorf("group length %d, want %d", n, len(body(nested)))
	}
	for _, c := range []struct {
		name string
		num  Number
		typ  Type
		in   []byte
		want error
	}{
		{"mismatched end", 1, StartGroupType, AppendTag(nil, 2, EndGroupType), errEndGroup},
		{"stray end", 1, EndGroupType, nil, errEndGroup},
		{"unterminated group", 1, StartGroupType, AppendVarint(AppendTag(nil, 2, VarintType), 7), io.ErrUnexpectedEOF},
		{"group with field 0", 1, StartGroupType, []byte{0x00}, errFieldNumber},
		{"type 6", 1, 6, []byte{0}, errReserved},
		{"type 7", 1, 7, []byte{0}, errReserved},
		{"truncated bytes", 1, BytesType, []byte{9, 1}, io.ErrUnexpectedEOF},
		{"truncated fixed32", 1, Fixed32Type, []byte{1}, io.ErrUnexpectedEOF},
		{"truncated fixed64", 1, Fixed64Type, []byte{1}, io.ErrUnexpectedEOF},
		{"truncated varint", 1, VarintType, []byte{0x80}, io.ErrUnexpectedEOF},
	} {
		if n := ConsumeFieldValue(c.num, c.typ, c.in); !errors.Is(ParseError(n), c.want) {
			t.Errorf("%s: got %v, want %v", c.name, ParseError(n), c.want)
		}
	}
	// Groups nested past MaxDepth fail instead of exhausting the stack.
	deep := bytes.Repeat(AppendTag(nil, 1, StartGroupType), MaxDepth+10)
	if n := ConsumeFieldValue(1, StartGroupType, deep); n >= 0 {
		t.Error("deep nesting accepted")
	}
	ok := bytes.Repeat(AppendTag(nil, 1, StartGroupType), MaxDepth)
	ok = append(ok, bytes.Repeat(AppendTag(nil, 1, EndGroupType), MaxDepth+1)...)
	if n := ConsumeFieldValue(1, StartGroupType, ok); n != len(ok) {
		t.Errorf("nesting at the limit: %d", n)
	}
}

func TestParseError(t *testing.T) {
	if ParseError(0) != nil || ParseError(5) != nil {
		t.Error("non-negative is nil")
	}
	if ParseError(-99) == nil {
		t.Error("unknown code")
	}
}

// FuzzConsume feeds arbitrary bytes to the consumers.
func FuzzConsume(f *testing.F) {
	f.Add([]byte{0x08, 0x96, 0x01})
	f.Add([]byte{0x12, 0x07, 't', 'e', 's', 't', 'i', 'n', 'g'})
	f.Add([]byte{0x0b, 0x10, 0x01, 0x0c})
	f.Add(bytes.Repeat([]byte{0xff}, 11))
	f.Fuzz(func(t *testing.T, b []byte) {
		check := func(what string, n int) {
			if n > len(b) {
				t.Fatalf("%s used %d of %d bytes", what, n, len(b))
			}
		}
		_, n := ConsumeVarint(b)
		check("varint", n)
		_, n = ConsumeFixed32(b)
		check("fixed32", n)
		_, n = ConsumeFixed64(b)
		check("fixed64", n)
		v, n := ConsumeBytes(b)
		check("bytes", n)
		if n >= 0 && len(v) > n {
			t.Fatal("bytes value longer than its record")
		}
		_, _ = ConsumeString(b)
		num, typ, n := ConsumeTag(b)
		check("tag", n)
		if n >= 0 {
			if num < MinValidNumber {
				t.Fatalf("tag accepted number %d", num)
			}
			check("field", n+max(0, ConsumeFieldValue(num, typ, b[n:])))
		}
		for _, typ := range []Type{0, 1, 2, 3, 4, 5, 6, 7} {
			check("value", ConsumeFieldValue(1, typ, b))
		}
	})
}

// FuzzRoundTrip checks appended values read back equal.
func FuzzRoundTrip(f *testing.F) {
	f.Add(uint64(300), []byte("abc"))
	f.Fuzz(func(t *testing.T, x uint64, p []byte) {
		b := AppendVarint(nil, x)
		if got, n := ConsumeVarint(b); got != x || n != len(b) {
			t.Fatalf("varint %d: %d %d", x, got, n)
		}
		b = AppendBytes(nil, p)
		if got, n := ConsumeBytes(b); !bytes.Equal(got, p) || n != len(b) {
			t.Fatal("bytes")
		}
		b = AppendFixed64(nil, x)
		if got, n := ConsumeFixed64(b); got != x || n != 8 {
			t.Fatal("fixed64")
		}
		b = AppendFixed32(nil, uint32(x))
		if got, n := ConsumeFixed32(b); got != uint32(x) || n != 4 {
			t.Fatal("fixed32")
		}
	})
}
