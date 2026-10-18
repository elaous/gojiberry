package saver

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

func TestBlurPlainPadsToABucketAndStaysReadable(t *testing.T) {
	d := docWithText("n", strings.Repeat("a", 100))
	d.SetID(docfile.NewID())
	plain, err := docfile.Open.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int{0, 500, 1100, 5000, 70000} {
		in := append(append([]byte(nil), plain...), make([]byte, 0)...)
		if extra > 0 {
			d2 := docWithText("n", strings.Repeat("a", extra))
			d2.SetID(d.ID())
			in, _ = docfile.Open.Marshal(d2)
		}
		out := blurPlain(in)
		n := len(out)
		if n&(n-1) != 0 || n < 1024 || n < len(in) {
			t.Fatalf("%d bytes became %d, not a bucket", len(in), n)
		}
		back, err := docfile.Open.Unmarshal(out)
		if err != nil || back.ID() != d.ID() {
			t.Fatalf("the padded message does not read: %v", err)
		}
	}
	if blurTime(int64(26*time.Hour)+5) != int64(24*time.Hour) || blurTime(0) != 0 {
		t.Error("blurTime")
	}
}

func TestExportPackBlursTheSealedRecords(t *testing.T) {
	ctx := context.Background()
	s, _, plain, _, secret := packSource(t)
	var buf bytes.Buffer
	if _, err := s.ExportPack(ctx, "src", "", &buf, PackOptions{Seal: fakeSeal, Blur: true}); err != nil {
		t.Fatal(err)
	}
	r, err := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Index().Entries {
		if e.ID == secret {
			if day := int64(24 * time.Hour); e.Updated%day != 0 {
				t.Errorf("the time %d is not a whole day", e.Updated)
			}
			// the envelope is "SEAL:<id>:" + the padded message
			if n := int(e.Length) - len("SEAL:"+secret+":"); n&(n-1) != 0 {
				t.Errorf("the sealed size %d is not a bucket", n)
			}
		}
		if e.ID == plain && e.Updated%int64(24*time.Hour) == 0 && e.Updated != 0 {
			t.Error("an ordinary document was blurred")
		}
	}
	// It imports as before.
	dst := newDst()
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Documents != 3 || len(rep.Failed) != 0 || len(rep.NeedsKey) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func bigPicture(seed byte) string {
	b := make([]byte, 100<<10)
	for i := range b {
		b[i] = byte(i*7) ^ seed
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
}

func TestExportPackKeepsLargeImagesApartAndImportRestoresThem(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	pic := bigPicture(3)
	for _, name := range []string{"one", "two"} {
		d := docWithText(name, name)
		d.Content = append(d.Content, docfile.Item{Image: &docfile.ImageItem{URL: pic, Width: 10, Height: 10}}, docfile.Item{Image: &docfile.ImageItem{URL: "data:image/png;base64,AAAA"}})
		if _, err := src.Save(ctx, "m", docfile.Open, d); err != nil {
			t.Fatal(err)
		}
	}
	var plainPack, apart bytes.Buffer
	if _, err := src.ExportPack(ctx, "m", "", &plainPack, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.ExportPack(ctx, "m", "", &apart, PackOptions{Attachments: DefaultAttachmentSize}); err != nil {
		t.Fatal(err)
	}
	r, err := pack.Open(bytes.NewReader(apart.Bytes()), int64(apart.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if r.Version() != 2 || len(r.Index().Attachments) != 1 || !r.Verify().OK() {
		t.Fatalf("version %d, %d attachments", r.Version(), len(r.Index().Attachments))
	}
	if apart.Len()*3 > plainPack.Len()*2 {
		t.Errorf("two documents with one picture: %d bytes, against %d with the picture twice", apart.Len(), plainPack.Len())
	}
	if bytes.Contains(apart.Bytes(), []byte(pic[30:80])) {
		t.Error("the picture is still inside a document")
	}
	dst := memSaver()
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(apart.Bytes()), int64(apart.Len()), ImportOptions{})
	if err != nil || rep.Documents != 2 || len(rep.Failed) != 0 || len(rep.Warnings) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	list, _ := dst.List(ctx, "m")
	for _, e := range list {
		d, err := dst.Load(ctx, "m", e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Content[1].Image == nil || d.Content[1].Image.URL != pic || d.Content[2].Image.URL != "data:image/png;base64,AAAA" {
			t.Fatalf("the pictures came back wrong in %q", e.Name)
		}
	}
}

func TestAPackWithNoLargeImageStaysVersionOne(t *testing.T) {
	ctx := context.Background()
	s := memSaver()
	d := docWithText("n", "t")
	d.Content = append(d.Content, docfile.Item{Image: &docfile.ImageItem{URL: "data:image/png;base64,AAAA"}})
	if _, err := s.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := s.ExportPack(ctx, "m", "", &buf, PackOptions{Attachments: DefaultAttachmentSize}); err != nil {
		t.Fatal(err)
	}
	r, _ := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if r.Version() != 1 {
		t.Errorf("version %d", r.Version())
	}
}
