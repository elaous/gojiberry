package docfile

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The protobuf schema is derived from the model's Go types: a struct is
// a message, its `pb:"N"` tags are the field numbers. That keeps one
// definition (the Go types), and Schema prints it as .proto text.

const protoPackage = "gojiberry.doc"

var (
	schemaOnce sync.Once
	schemaFile protoreflect.FileDescriptor
	schemaErr  error
	schemaDesc *descriptorpb.FileDescriptorProto
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

func scalarType(t reflect.Type) descriptorpb.FieldDescriptorProto_Type {
	switch t.Kind() {
	case reflect.String:
		return descriptorpb.FieldDescriptorProto_TYPE_STRING
	case reflect.Bool:
		return descriptorpb.FieldDescriptorProto_TYPE_BOOL
	case reflect.Float64:
		return descriptorpb.FieldDescriptorProto_TYPE_DOUBLE
	case reflect.Int, reflect.Int64:
		return descriptorpb.FieldDescriptorProto_TYPE_INT64
	}
	panic("docfile: unsupported field type " + t.String())
}

func buildMessage(t reflect.Type, seen map[reflect.Type]bool, out *[]*descriptorpb.DescriptorProto) {
	if seen[t] {
		return
	}
	seen[t] = true
	m := &descriptorpb.DescriptorProto{Name: proto.String(t.Name())}
	for i := range t.NumField() {
		f := t.Field(i)
		fd := &descriptorpb.FieldDescriptorProto{
			Name:     proto.String(snake(f.Name)),
			JsonName: proto.String(f.Name),
			Number:   proto.Int32(fieldNumber(f)),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}
		if f.Type.Kind() == reflect.Slice {
			fd.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		}
		if st, ok := elemStruct(f.Type); ok {
			fd.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
			fd.TypeName = proto.String("." + protoPackage + "." + st.Name())
			buildMessage(st, seen, out)
		} else {
			et := f.Type
			if et.Kind() == reflect.Slice {
				et = et.Elem()
			}
			fd.Type = scalarType(et).Enum()
		}
		m.Field = append(m.Field, fd)
	}
	*out = append(*out, m)
}

func schema() (protoreflect.FileDescriptor, error) {
	schemaOnce.Do(func() {
		var msgs []*descriptorpb.DescriptorProto
		buildMessage(reflect.TypeFor[Document](), map[reflect.Type]bool{}, &msgs)
		schemaDesc = &descriptorpb.FileDescriptorProto{
			Name:        proto.String("gojiberry/doc.proto"),
			Package:     proto.String(protoPackage),
			Syntax:      proto.String("proto3"),
			MessageType: msgs,
		}
		schemaFile, schemaErr = protodesc.NewFile(schemaDesc, nil)
	})
	return schemaFile, schemaErr
}

// Schema returns the document's protobuf schema as .proto source.
func Schema() string {
	if _, err := schema(); err != nil {
		return "// " + err.Error() + "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "syntax = \"proto3\";\n\npackage %s;\n", protoPackage)
	for _, m := range schemaDesc.MessageType {
		fmt.Fprintf(&b, "\nmessage %s {\n", m.GetName())
		for _, f := range m.Field {
			typ := strings.ToLower(strings.TrimPrefix(f.GetType().String(), "TYPE_"))
			if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
				typ = strings.TrimPrefix(f.GetTypeName(), "."+protoPackage+".")
			}
			label := ""
			if f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				label = "repeated "
			}
			fmt.Fprintf(&b, "  %s%s %s = %d;\n", label, typ, f.GetName(), f.GetNumber())
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func messageDescriptor(t reflect.Type) (protoreflect.MessageDescriptor, error) {
	file, err := schema()
	if err != nil {
		return nil, err
	}
	md := file.Messages().ByName(protoreflect.Name(t.Name()))
	if md == nil {
		return nil, fmt.Errorf("docfile: no message for %s", t)
	}
	return md, nil
}

// toMessage fills a dynamic message from the struct v.
func toMessage(v reflect.Value) (*dynamicpb.Message, error) {
	md, err := messageDescriptor(v.Type())
	if err != nil {
		return nil, err
	}
	msg := dynamicpb.NewMessage(md)
	for i := range v.NumField() {
		fv := v.Field(i)
		fd := md.Fields().ByNumber(protoreflect.FieldNumber(fieldNumber(v.Type().Field(i))))
		switch {
		case fd.IsList():
			if fv.Len() == 0 {
				continue
			}
			list := msg.Mutable(fd).List()
			for j := range fv.Len() {
				if fd.Kind() == protoreflect.MessageKind {
					sub, err := toMessage(fv.Index(j))
					if err != nil {
						return nil, err
					}
					list.Append(protoreflect.ValueOfMessage(sub))
				} else {
					list.Append(scalarValue(fd, fv.Index(j)))
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			} else if fv.IsZero() {
				continue
			}
			sub, err := toMessage(fv)
			if err != nil {
				return nil, err
			}
			msg.Set(fd, protoreflect.ValueOfMessage(sub))
		default:
			if !fv.IsZero() {
				msg.Set(fd, scalarValue(fd, fv))
			}
		}
	}
	return msg, nil
}

func scalarValue(fd protoreflect.FieldDescriptor, v reflect.Value) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(v.String())
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(v.Bool())
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(v.Float())
	default:
		return protoreflect.ValueOfInt64(v.Int())
	}
}

// fromMessage fills the struct dst (addressable) from msg.
func fromMessage(dst reflect.Value, msg protoreflect.Message) {
	md := msg.Descriptor()
	for i := range dst.NumField() {
		fv := dst.Field(i)
		fd := md.Fields().ByNumber(protoreflect.FieldNumber(fieldNumber(dst.Type().Field(i))))
		if !msg.Has(fd) {
			continue
		}
		val := msg.Get(fd)
		switch {
		case fd.IsList():
			list := val.List()
			fv.Set(reflect.MakeSlice(fv.Type(), list.Len(), list.Len()))
			for j := range list.Len() {
				if fd.Kind() == protoreflect.MessageKind {
					fromMessage(fv.Index(j), list.Get(j).Message())
				} else {
					setScalar(fv.Index(j), list.Get(j))
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			if fv.Kind() == reflect.Pointer {
				fv.Set(reflect.New(fv.Type().Elem()))
				fv = fv.Elem()
			}
			fromMessage(fv, val.Message())
		default:
			setScalar(fv, val)
		}
	}
}

func setScalar(dst reflect.Value, v protoreflect.Value) {
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(v.String())
	case reflect.Bool:
		dst.SetBool(v.Bool())
	case reflect.Float64:
		dst.SetFloat(v.Float())
	default:
		dst.SetInt(v.Int())
	}
}
