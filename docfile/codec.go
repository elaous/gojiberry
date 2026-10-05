package docfile

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Codec is a way to write a Document as bytes.
type Codec interface {
	// Name is the format's id ("json", "proto-text", "proto").
	Name() string
	// Label is what a menu shows.
	Label() string
	// Ext is the file extension, with the dot.
	Ext() string
	// MIME is the media type.
	MIME() string
	Marshal(Document) ([]byte, error)
	Unmarshal([]byte) (Document, error)
}

type format struct {
	name, label, ext, mime string
	marshal                func(Document) ([]byte, error)
	unmarshal              func([]byte) (Document, error)
}

func (f format) Name() string                         { return f.name }
func (f format) Label() string                        { return f.label }
func (f format) Ext() string                          { return f.ext }
func (f format) MIME() string                         { return f.mime }
func (f format) Marshal(d Document) ([]byte, error)   { return f.marshal(d) }
func (f format) Unmarshal(b []byte) (Document, error) { return f.unmarshal(b) }

// JSON writes indented JSON.
var JSON Codec = &format{
	name: "json", label: "JSON", ext: ".json", mime: "application/json",
	marshal: func(d Document) ([]byte, error) { return json.MarshalIndent(d, "", "  ") },
	unmarshal: func(b []byte) (d Document, err error) {
		err = json.Unmarshal(b, &d)
		return d, err
	},
}

// ProtoText writes the protobuf text format (readable, diffable).
var ProtoText Codec = &format{
	name: "proto-text", label: "Protobuf (text)", ext: ".pbtxt", mime: "text/plain",
	marshal: func(d Document) ([]byte, error) {
		msg, err := toMessage(reflect.ValueOf(d))
		if err != nil {
			return nil, err
		}
		return prototext.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(msg)
	},
	unmarshal: func(b []byte) (Document, error) {
		return fromProto(b, func(msg *dynamicpb.Message) error { return prototext.Unmarshal(b, msg) })
	},
}

// ProtoBinary writes the protobuf wire format (compact).
var ProtoBinary Codec = &format{
	name: "proto", label: "Protobuf (binary)", ext: ".pb", mime: "application/x-protobuf",
	marshal: func(d Document) ([]byte, error) {
		msg, err := toMessage(reflect.ValueOf(d))
		if err != nil {
			return nil, err
		}
		return proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	},
	unmarshal: func(b []byte) (Document, error) {
		return fromProto(b, func(msg *dynamicpb.Message) error { return proto.Unmarshal(b, msg) })
	},
}

// Open is opendocs' own file format, a .open file: the document as protobuf wire
// format, byte for byte what ProtoBinary writes, under its own name and extension.
// Fields are only ever added (a removed one is reserved, its number never reused),
// so older readers skip what they do not know.
var Open Codec = &format{
	name: "open", label: "opendocs (.open)", ext: ".open", mime: "application/vnd.opendocs.document",
	marshal:   func(d Document) ([]byte, error) { return ProtoBinary.Marshal(d) },
	unmarshal: func(b []byte) (Document, error) { return ProtoBinary.Unmarshal(b) },
}

func fromProto(_ []byte, decode func(*dynamicpb.Message) error) (Document, error) {
	md, err := messageDescriptor(reflect.TypeFor[Document]())
	if err != nil {
		return Document{}, err
	}
	msg := dynamicpb.NewMessage(md)
	if err := decode(msg); err != nil {
		return Document{}, err
	}
	var d Document
	fromMessage(reflect.ValueOf(&d).Elem(), msg)
	return d, nil
}

var formats = []Codec{JSON, ProtoText, ProtoBinary, Open}

// Formats lists the built-in codecs.
func Formats() []Codec { return append([]Codec(nil), formats...) }

// FormatNames returns the names of Formats, sorted.
func FormatNames() []string {
	var names []string
	for _, f := range formats {
		names = append(names, f.Name())
	}
	sort.Strings(names)
	return names
}

// ByName finds a format by its name.
func ByName(name string) (Codec, error) {
	for _, f := range formats {
		if f.Name() == name {
			return f, nil
		}
	}
	return nil, fmt.Errorf("docfile: unknown format %q", name)
}

// Detect guesses the format of data by its first bytes: JSON starts
// with '{'; protobuf text with a field name; anything else is binary.
func Detect(data []byte) Codec {
	// A binary document starts with the tag byte 0x0A, which is also a
	// newline; it is binary if it parses as such.
	if len(data) > 0 && data[0] == 0x0A {
		if _, err := ProtoBinary.Unmarshal(data); err == nil {
			return ProtoBinary
		}
	}
	for _, b := range data {
		switch {
		case b == ' ' || b == '\n' || b == '\t' || b == '\r':
			continue
		case b == '{':
			return JSON
		case b >= 'a' && b <= 'z':
			return ProtoText
		}
		break
	}
	return ProtoBinary
}
