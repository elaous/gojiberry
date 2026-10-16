// Command pbgolden regenerates the golden files of docfile/testdata with the protobuf library, so that the codecs of
// package docfile are checked against an encoder they did not write. Usage, from the repository root:
//
//	go test ./docfile -run TestDumpGolden -count=1 -dump=/tmp/pb   # writes /tmp/pb/rich.txt and /tmp/pb/schema.proto
//	cd tools && go run ./pbgolden /tmp/pb/schema.proto /tmp/pb/rich.txt ../docfile/testdata/rich.pb ../docfile/testdata/rich.pbtxt
//	cp /tmp/pb/schema.proto docfile/testdata/schema.proto
//
// It reads the proto text our own codec wrote for fillDoc(), loads it into a message built from the schema, and writes
// that message again in binary and in text. Run on the old schema and the old rich.pbtxt it gives the committed files
// byte for byte, which is the check that the pipeline itself is right.
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
