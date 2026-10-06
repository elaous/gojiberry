package docfile

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// The protobuf schema is derived from the model's Go types: a struct is a message, its
// `pb:"N"` tags are the field numbers. That keeps one definition (the Go types), and
// Schema prints it as .proto text. The wire and text formats are written here against
// those types directly (wire.go, text.go) on top of the small internal/wire package, so
// no protobuf module is a dependency at all: a browser build saves several megabytes by it.

const protoPackage = "gojiberry.doc"

type kind int

const (
	kString kind = iota
	kBool
	kFloat
	kInt
	kMsg
)

// protoName is the type name .proto text uses for a scalar kind.
func (k kind) protoName() string {
	return [...]string{"string", "bool", "double", "int64"}[k]
}

type field struct {
	index    int
	num      int32
	name     string // snake_case, the .proto name
	goName   string // the Go name, which text format also accepts (the JSON name)
	kind     kind
	repeated bool
	pointer  bool     // a message held behind a pointer
	msg      *message // for kMsg
}

type message struct {
	typ    reflect.Type
	name   string
	fields []field // by number
	byNum  map[int32]*field
}

var (
	schemaOnce sync.Once
	schemaRoot *message
	schemaList []*message // in the order Schema prints them
)

func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func fieldNumber(f reflect.StructField) int32 {
	var n int
	_, _ = fmt.Sscan(f.Tag.Get("pb"), &n)
	return int32(n)
}

// elemStruct returns the struct type a field holds (directly, behind a
// pointer, or as a slice element), if it holds one.
func elemStruct(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t, t.Kind() == reflect.Struct
}

func scalarKind(t reflect.Type) kind {
	switch t.Kind() {
	case reflect.String:
		return kString
	case reflect.Bool:
		return kBool
	case reflect.Float64:
		return kFloat
	case reflect.Int, reflect.Int64:
		return kInt
	}
	panic("docfile: unsupported field type " + t.String())
}

// buildMessage describes the struct t and, first, the messages it holds. Messages are
// listed children first, the way Schema has always printed them.
func buildMessage(t reflect.Type, seen map[reflect.Type]*message, list *[]*message) *message {
	if m, ok := seen[t]; ok {
		return m
	}
	m := &message{typ: t, name: t.Name(), byNum: map[int32]*field{}}
	seen[t] = m
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.Tag.Get("pb") == "" {
			continue
		}
		f := field{index: i, num: fieldNumber(sf), name: snake(sf.Name), goName: sf.Name, repeated: sf.Type.Kind() == reflect.Slice}
		if st, ok := elemStruct(sf.Type); ok {
			f.kind = kMsg
			f.pointer = sf.Type.Kind() == reflect.Pointer
			f.msg = buildMessage(st, seen, list)
		} else {
			et := sf.Type
			if et.Kind() == reflect.Slice {
				et = et.Elem()
			}
			f.kind = scalarKind(et)
		}
		m.fields = append(m.fields, f)
	}
	sort.Slice(m.fields, func(i, j int) bool { return m.fields[i].num < m.fields[j].num })
	for i := range m.fields {
		m.byNum[m.fields[i].num] = &m.fields[i]
	}
	*list = append(*list, m)
	return m
}

func schema() *message {
	schemaOnce.Do(func() {
		schemaRoot = buildMessage(reflect.TypeFor[Document](), map[reflect.Type]*message{}, &schemaList)
	})
	return schemaRoot
}

// Schema returns the document's protobuf schema as .proto source.
func Schema() string {
	schema()
	var b strings.Builder
	fmt.Fprintf(&b, "syntax = \"proto3\";\n\npackage %s;\n", protoPackage)
	for _, m := range schemaList {
		fmt.Fprintf(&b, "\nmessage %s {\n", m.name)
		for _, f := range m.fields {
			name := ""
			if f.kind == kMsg {
				name = f.msg.name
			} else {
				name = f.kind.protoName()
			}
			label := ""
			if f.repeated {
				label = "repeated "
			}
			fmt.Fprintf(&b, "  %s%s %s = %d;\n", label, name, f.name, f.num)
		}
		b.WriteString("}\n")
	}
	return b.String()
}
