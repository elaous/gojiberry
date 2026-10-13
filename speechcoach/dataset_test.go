package speechcoach

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func sampleDataset() *Dataset {
	d := &Dataset{}
	d.Add(NewEntry("en", "Good morning", "Good morning", Prompted, tone(1.2, 0.4, 0.002), nil, t0))
	d.Add(NewEntry("de", "Guten Tag", "guten Tag", Prompted, tone(1.0, 0.3, 0.002), []string{HintTooQuiet}, t0.Add(time.Hour)))
	d.Add(NewEntry("en", "", "call my sister", Dictation, tone(0.8, 0.4, 0.002), nil, t0.Add(2*time.Hour)))
	return d
}

func exportBytes(t *testing.T, d *Dataset, keep func(Entry) bool) ([]byte, Manifest) {
	t.Helper()
	var b bytes.Buffer
	m, err := d.Export(&b, keep)
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), m
}

func TestExportManifestAndLines(t *testing.T) {
	d := sampleDataset()
	b, m := exportBytes(t, d, nil)
	if m.Count != 3 || m.Format != FormatName || m.SampleRate != 16000 || m.Channels != 1 || !slices.Equal(m.Languages, []string{"de", "en"}) {
		t.Fatalf("manifest %+v", m)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var lines []byte
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "data.jsonl" {
			rc, _ := f.Open()
			lines, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	if !names["manifest.json"] || !names["data.jsonl"] || len(names) != 5 {
		t.Fatalf("files %v", names)
	}
	rows := strings.Split(strings.TrimSpace(string(lines)), "\n")
	if len(rows) != 3 {
		t.Fatalf("rows %d", len(rows))
	}
	var l Line
	if err := json.Unmarshal([]byte(rows[0]), &l); err != nil || l.Text != "Good morning" || !strings.HasPrefix(l.Audio, "audio/") || !names[l.Audio] {
		t.Fatalf("line %+v %v", l, err)
	}
	// the same dataset gives the same bytes, and a filter exports only what it keeps
	b2, _ := exportBytes(t, d, nil)
	if !bytes.Equal(b, b2) {
		t.Error("export is not reproducible")
	}
	if _, m := exportBytes(t, d, func(e Entry) bool { return e.Lang == "de" }); m.Count != 1 {
		t.Errorf("filtered %d", m.Count)
	}
}

func TestExportThenImportRoundTrips(t *testing.T) {
	d := sampleDataset()
	b, _ := exportBytes(t, d, nil)
	var back Dataset
	res, err := back.Import(b)
	if err != nil || res.Added != 3 || res.Duplicate != 0 || len(res.Problems) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if !slices.EqualFunc(d.List(), back.List(), func(a, b Entry) bool {
		return a.ID == b.ID && a.Transcript == b.Transcript && a.Prompt == b.Prompt && a.Lang == b.Lang &&
			a.Source == b.Source && bytes.Equal(a.WAV, b.WAV) && a.Created.Equal(b.Created) && slices.Equal(a.Hints, b.Hints)
	}) {
		t.Error("the imported dataset differs")
	}
	// a second import adds nothing and says so
	if res, _ := back.Import(b); res.Added != 0 || res.Duplicate != 3 {
		t.Errorf("again: %+v", res)
	}
}

// rebuild writes a zip with the given files, for the refusals below.
func rebuild(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write(c)
	}
	_ = zw.Close()
	return b.Bytes()
}

func readZip(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		out[f.Name], _ = io.ReadAll(rc)
		rc.Close()
	}
	return out
}

func TestImportRefusesWhatItShould(t *testing.T) {
	good, _ := exportBytes(t, sampleDataset(), nil)
	files := readZip(t, good)

	var d Dataset
	if _, err := d.Import([]byte("not a zip")); err == nil {
		t.Error("not a zip")
	}
	if _, err := d.Import(rebuild(t, map[string][]byte{"x.txt": []byte("hi")})); err == nil {
		t.Error("a zip that is no export")
	}
	newer := bytes.Replace(files["manifest.json"], []byte(`"version": 1`), []byte(`"version": 2`), 1)
	if _, err := d.Import(rebuild(t, map[string][]byte{"manifest.json": newer, "data.jsonl": files["data.jsonl"]})); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Errorf("newer version: %v", err)
	}
	if d.Len() != 0 {
		t.Error("nothing is added by a refused file")
	}

	// one damaged recording is reported and the others still come in
	var first Line
	_ = json.Unmarshal([]byte(strings.Split(string(files["data.jsonl"]), "\n")[0]), &first)
	files[first.Audio] = append([]byte{}, files[first.Audio]...)
	files[first.Audio][60] ^= 0xff
	res, err := d.Import(rebuild(t, files))
	if err != nil || res.Added != 2 || len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "checksum") {
		t.Fatalf("damaged: %+v %v", res, err)
	}

	// a path that escapes is not followed
	evil := strings.Replace(string(files["data.jsonl"]), "audio/", "../audio/", 1)
	files["data.jsonl"] = []byte(evil)
	var e2 Dataset
	res, _ = e2.Import(rebuild(t, files))
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "path") {
		t.Errorf("escaping path: %+v", res)
	}
	// an unknown source or a missing file is reported
	files["data.jsonl"] = []byte(`{"audio":"audio/` + strings.Repeat("0", 24) + `.wav","source":"prompted"}` + "\n")
	var e3 Dataset
	if res, _ = e3.Import(rebuild(t, files)); len(res.Problems) != 1 || e3.Len() != 0 {
		t.Errorf("missing audio: %+v", res)
	}
}

func TestAddReplacesARetakeOfTheSamePhrase(t *testing.T) {
	var d Dataset
	d.Add(NewEntry("en", "Hello", "Hello", Prompted, tone(1, 0.4, 0.002), nil, t0))
	d.Add(NewEntry("en", "Hello", "Hello", Prompted, tone(1.1, 0.4, 0.002), nil, t0.Add(time.Minute)))
	d.Add(NewEntry("de", "Hello", "Hello", Prompted, tone(1, 0.4, 0.002), nil, t0))
	if d.Len() != 2 || d.ByPrompt("Hello") == nil {
		t.Fatalf("len %d", d.Len())
	}
	id := d.List()[0].ID
	if !d.Remove(id) || d.Remove(id) || d.Len() != 1 {
		t.Error("remove")
	}
	d.Clear()
	if d.Len() != 0 {
		t.Error("clear")
	}
}

func TestEntryEncodingChecksTheAudio(t *testing.T) {
	e := NewEntry("en", "Hi", "Hi", Prompted, tone(1, 0.4, 0.002), nil, t0)
	b := EncodeEntry(e)
	got, err := DecodeEntry(b)
	if err != nil || got.ID != e.ID || !bytes.Equal(got.WAV, e.WAV) {
		t.Fatalf("%v", err)
	}
	bad := append([]byte{}, b...)
	bad[len(bad)-5] ^= 1
	if _, err := DecodeEntry(bad); err == nil {
		t.Error("a changed byte is refused")
	}
	for _, junk := range [][]byte{nil, []byte("GJSE1\n"), []byte("GJSE1\n99999\n{}"), []byte("other")} {
		if _, err := DecodeEntry(junk); err == nil {
			t.Errorf("%q", junk)
		}
	}
}

func TestSealOpen(t *testing.T) {
	key, err := SealKey([]byte("owner secret"), "entry")
	if err != nil {
		t.Fatal(err)
	}
	data := EncodeEntry(NewEntry("en", "Hi", "Hi", Prompted, tone(1, 0.4, 0.002), nil, t0))
	sealed, err := Seal(key, "speech/a", data)
	if err != nil || bytes.Contains(sealed, data[:20]) {
		t.Fatalf("sealed %v", err)
	}
	if got, err := Open(key, "speech/a", sealed); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("open: %v", err)
	}
	other, _ := SealKey([]byte("another secret"), "entry")
	changed := append([]byte{}, sealed...)
	changed[len(changed)-3] ^= 1
	for name, c := range map[string]struct {
		key  []byte
		name string
		b    []byte
	}{
		"wrong key":  {other, "speech/a", sealed},
		"moved":      {key, "speech/b", sealed},
		"changed":    {key, "speech/a", changed},
		"short":      {key, "speech/a", sealed[:10]},
		"not sealed": {key, "speech/a", data},
	} {
		if _, err := Open(c.key, c.name, c.b); err != ErrSealed {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := SealKey(nil, "x"); err == nil {
		t.Error("no secret")
	}
	if _, err := Seal([]byte("short"), "x", data); err == nil {
		t.Error("short key")
	}
}
