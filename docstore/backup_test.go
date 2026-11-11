package docstore

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"testing"
)

func fill(t *testing.T) *MemoryAdapter {
	t.Helper()
	m := NewMemoryAdapter()
	for id, body := range map[string]string{"doc/1": "first", "catalog:folder/ü ñ": "second", "empty": ""} {
		if err := m.Save(context.Background(), id, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := fill(t)
	var buf bytes.Buffer
	man, err := Backup(ctx, src, &buf)
	if err != nil || len(man.Entries) != 3 || man.Version != 1 {
		t.Fatalf("%+v %v", man, err)
	}
	dst := NewMemoryAdapter()
	_ = dst.Save(ctx, "doc/1", []byte("older text that the restore replaces"))
	_ = dst.Save(ctx, "other", []byte("left alone"))
	n, err := Restore(ctx, dst, bytes.NewReader(buf.Bytes()))
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	for id, want := range map[string]string{"doc/1": "first", "catalog:folder/ü ñ": "second", "empty": "", "other": "left alone"} {
		d, err := dst.Load(ctx, id)
		if err != nil || string(d.Data) != want {
			t.Fatalf("%q: %q %v", id, d.Data, err)
		}
	}
}

func TestRestoreRefusesADamagedBackupAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	var good bytes.Buffer
	if _, err := Backup(ctx, fill(t), &good); err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(good.Bytes())
	at := bytes.Index(flipped, []byte("first"))
	flipped[at] ^= 1

	build := func(entries map[string]string, order []string) []byte {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for _, name := range order {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(entries[name])), Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte(entries[name]))
		}
		_ = tw.Close()
		return b.Bytes()
	}
	noManifest := build(map[string]string{"6162": "x"}, []string{"6162"})
	badManifest := build(map[string]string{manifestName: `not json`}, []string{manifestName})
	wrongVersion := build(map[string]string{manifestName: `{"version":9}`}, []string{manifestName})
	extra := build(map[string]string{"6162": "x", manifestName: `{"version":1,"entries":[]}`}, []string{"6162", manifestName})

	cases := map[string][]byte{
		"a changed byte": flipped, "cut short": good.Bytes()[:len(good.Bytes())/2], "no manifest": noManifest,
		"bad manifest": badManifest, "wrong version": wrongVersion, "an entry the manifest lacks": extra,
		"not a tar": []byte("this is not an archive at all, not even close to one........................................"),
		"empty":     nil,
	}
	for name, data := range cases {
		dst := NewMemoryAdapter()
		n, err := Restore(ctx, dst, bytes.NewReader(data))
		if !errors.Is(err, ErrBadBackup) {
			t.Errorf("%s: %d %v", name, n, err)
		}
		if metas, _ := dst.List(ctx); len(metas) != 0 {
			t.Errorf("%s: wrote %d documents", name, len(metas))
		}
	}
}

type failSave struct{ Adapter }

func (failSave) Save(context.Context, string, []byte) error { return errors.New("full") }

func TestBackupAndRestoreReportStoreErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Backup(ctx, brokenStore{NewMemoryAdapter()}, &bytes.Buffer{}); err == nil {
		t.Fatal("a failing list is an error")
	}
	var buf bytes.Buffer
	if _, err := Backup(ctx, fill(t), &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, failSave{NewMemoryAdapter()}, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("a failing save is an error")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Backup(cctx, fill(t), &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if _, err := Restore(cctx, NewMemoryAdapter(), bytes.NewReader(buf.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
