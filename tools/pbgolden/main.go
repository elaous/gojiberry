package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

func main() {
	schema, docJSON, outPB, outTXT := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	src, _ := os.ReadFile(schema)
	c := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"doc.proto": string(src)})})}
	fds, err := c.Compile(context.Background(), "doc.proto")
	if err != nil {
		panic(err)
	}
	md := fds[0].Messages().ByName("Document")
	m := dynamicpb.NewMessage(md)
	js, _ := os.ReadFile(docJSON)
	if err := prototext.Unmarshal(js, m); err != nil {
		panic(err)
	}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		panic(err)
	}
	os.WriteFile(outPB, b, 0o644)
	t, _ := prototext.MarshalOptions{Multiline: true}.Marshal(m)
	txt := strings.ReplaceAll(string(t), ":  ", ": ")
	os.WriteFile(outTXT, []byte(txt), 0o644)
	fmt.Println("wrote", len(b), "bytes")
}
