package docfile

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// fillDoc returns a Document with every field set to a non-zero value, two elements in
// every list, so that a codec is checked on all of the model.
func fillDoc() Document {
	var d Document
	n := 0
	fill(reflect.ValueOf(&d).Elem(), &n, 0)
	return d
}

func fill(v reflect.Value, n *int, depth int) {
	*n++
	switch v.Kind() {
	case reflect.String:
		// Some awkward characters on purpose: quotes, a newline, a control character,
		// non-ASCII text.
		v.SetString("s" + strconv.Itoa(*n) + " \"q\" \\ \n\t\x01 é世")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Float64:
		v.SetFloat(float64(*n) + 0.25)
	case reflect.Int, reflect.Int64:
		if *n%3 == 0 {
			v.SetInt(-int64(*n) * 1000003)
		} else {
			v.SetInt(int64(*n) * 7)
		}
	case reflect.Pointer:
		if depth > 6 {
			return
		}
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), n, depth+1)
	case reflect.Slice:
		if depth > 6 {
			return
		}
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := range 2 {
			fill(v.Index(i), n, depth+1)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).Tag.Get("pb") != "" {
				fill(v.Field(i), n, depth+1)
			}
		}
	}
}

// TestDumpGolden writes what tools/pbgolden needs to regenerate the golden files (see that command): it does nothing
// unless -dump names a directory.
var dumpDir = flag.String("dump", "", "directory to write rich.txt and schema.proto to, for tools/pbgolden")

func TestDumpGolden(t *testing.T) {
	if *dumpDir == "" {
		t.Skip("set -dump to regenerate the golden files")
	}
	b, err := ProtoText.Marshal(fillDoc())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(*dumpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*dumpDir, "rich.txt"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*dumpDir, "schema.proto"), []byte(Schema()), 0o644); err != nil {
		t.Fatal(err)
	}
}
