package docfile

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The files in testdata were written by the protobuf library's own encoders (from
// fillDoc): the codecs here must write the same bytes and read them back.

func TestBinaryMatchesProtobufLibrary(t *testing.T) {
	want, err := os.ReadFile("testdata/rich.pb")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ProtoBinary.Marshal(fillDoc())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("wire bytes differ: %d bytes, want %d", len(got), len(want))
	}
	d, err := ProtoBinary.Unmarshal(want)
	if err != nil || !reflect.DeepEqual(d, fillDoc()) {
		t.Fatalf("read back: %v", err)
	}
}

func TestTextReadsWhatProtobufLibraryWrote(t *testing.T) {
	want, err := os.ReadFile("testdata/rich.pbtxt")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ProtoText.Unmarshal(want)
	if err != nil || !reflect.DeepEqual(d, fillDoc()) {
		t.Fatalf("read: %v", err)
	}
	out, err := ProtoText.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ProtoText.Unmarshal(out)
	if err != nil || !reflect.DeepEqual(back, fillDoc()) {
		t.Fatalf("round trip: %v\n%s", err, out)
	}
}

func TestSchemaMatchesGolden(t *testing.T) {
	want, _ := os.ReadFile("testdata/schema.proto")
	if Schema() != string(want) {
		t.Fatalf("schema changed:\n%s", Schema())
	}
}

func TestEmptyMessagesSurvive(t *testing.T) {
	d := Document{Content: []Item{{Break: &Break{}}, {Chars: &Chars{}}}}
	for _, c := range []Codec{ProtoBinary, ProtoText} {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Unmarshal(b)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Fatalf("%s: %+v %v", c.Name(), got, err)
		}
	}
}

func TestCorruptBinaryIsAnErrorNeverAPanic(t *testing.T) {
	good, _ := os.ReadFile("testdata/rich.pb")
	for n := range len(good) {
		_, _ = ProtoBinary.Unmarshal(good[:n]) // an error or a shorter document, no panic
	}
	for _, bad := range [][]byte{{0x0a, 0x05, 'a'}, {0x0a, 0x02, 0xff, 0xfe}, {0x00}, {0x80}} {
		if _, err := ProtoBinary.Unmarshal(bad); err == nil {
			t.Errorf("% x read without error", bad)
		}
	}
}

func TestBinaryUnknownFieldsAndUnpackedListsAreRead(t *testing.T) {
	// field 99 (unknown, a varint), then columns (2 of an item) with unpacked widths.
	raw := []byte{0x98, 0x06, 0x07}
	d, err := ProtoBinary.Unmarshal(raw)
	if err != nil || !reflect.DeepEqual(d, Document{}) {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestTextAcceptsTheLooseForms(t *testing.T) {
	src := `# a comment
title: "a" "b"; updated: 5
content < chars { text: 'x\x41\101é' } >
content: [ { chars: { text: "y" } } ]
`
	d, err := ProtoText.Unmarshal([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "ab" || d.Updated != 5 || len(d.Content) != 2 || d.Content[0].Chars.Text != "xAAé" || d.Content[1].Chars.Text != "y" {
		t.Fatalf("%+v", d)
	}
	for _, bad := range []string{`nope: 1`, `title: "x`, `title 1`, `content: {`, `updated: abc`, strings.Repeat("content {", 100)} {
		if _, err := ProtoText.Unmarshal([]byte(bad)); err == nil {
			t.Errorf("%q read without error", bad)
		}
	}
}
