package speechcoach

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry is one recording with its transcript, in the person's own dataset.
type Entry struct {
	ID         string    `json:"id"`         // first 24 hex digits of the SHA-256 of the WAV and the prompt
	Lang       string    `json:"lang"`       // language spoken
	Prompt     string    `json:"prompt"`     // the phrase that was asked for ("" for free dictation)
	Transcript string    `json:"transcript"` // what the person says it says (the prompt, or their correction)
	Source     string    `json:"source"`     // "prompted" or "dictation"
	Seconds    float64   `json:"seconds"`
	Hints      []string  `json:"hints,omitempty"`
	Created    time.Time `json:"created"`
	SHA256     string    `json:"sha256"` // of the WAV file, hex
	WAV        []byte    `json:"-"`
}

// Sources of an entry.
const (
	Prompted  = "prompted"
	Dictation = "dictation"
)

// NewEntry builds an entry from a take. The ID follows from the audio and the prompt, so the same take
// added twice is one entry.
func NewEntry(lang, prompt, transcript, source string, pcm []int16, hints []string, at time.Time) Entry {
	wav := EncodeWAV(pcm)
	sum := sha256.Sum256(wav)
	idh := sha256.Sum256(append(append([]byte{}, wav...), prompt...))
	return Entry{ID: hex.EncodeToString(idh[:12]), Lang: lang, Prompt: prompt, Transcript: transcript, Source: source,
		Seconds: float64(len(pcm)) / SampleRate, Hints: hints, Created: at.UTC().Truncate(time.Second), SHA256: hex.EncodeToString(sum[:]), WAV: wav}
}

// Dataset is the person's recordings. It lives where the caller keeps it (in the browser, sealed with
// the key of the encrypted store when that is unlocked); this type only holds and exports it.
type Dataset struct {
	entries map[string]*Entry
}

// Len is the number of entries.
func (d *Dataset) Len() int { return len(d.entries) }

// Add keeps e, replacing an entry with the same ID. A prompted entry replaces an earlier one with the
// same prompt and language (a retake does not pile up copies).
func (d *Dataset) Add(e Entry) {
	if d.entries == nil {
		d.entries = map[string]*Entry{}
	}
	if e.Prompt != "" {
		for id, old := range d.entries {
			if old.Prompt == e.Prompt && old.Lang == e.Lang && old.Source == e.Source {
				delete(d.entries, id)
			}
		}
	}
	d.entries[e.ID] = &e
}

// Remove deletes an entry; it reports whether there was one.
func (d *Dataset) Remove(id string) bool {
	_, ok := d.entries[id]
	delete(d.entries, id)
	return ok
}

// Clear deletes everything.
func (d *Dataset) Clear() { d.entries = nil }

// Get returns the entry with id.
func (d *Dataset) Get(id string) *Entry { return d.entries[id] }

// ByPrompt returns the prompted entry for a phrase, or nil.
func (d *Dataset) ByPrompt(prompt string) *Entry {
	for _, e := range d.entries {
		if e.Prompt == prompt && e.Source == Prompted {
			return e
		}
	}
	return nil
}

// List returns the entries oldest first (ties by ID), so exports are the same each time.
func (d *Dataset) List() []Entry {
	out := make([]Entry, 0, len(d.entries))
	for _, e := range d.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Format identifiers of the export.
const (
	FormatName    = "gojiberry-speechcoach"
	FormatVersion = 1
	manifestName  = "manifest.json"
	linesName     = "data.jsonl"
)

// Manifest describes an export. Nothing in it names the person or the device.
type Manifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	Created    time.Time `json:"created"`
	SampleRate int       `json:"sample_rate"`
	Channels   int       `json:"channels"`
	Encoding   string    `json:"encoding"`
	Count      int       `json:"count"`
	Seconds    float64   `json:"seconds"`
	Languages  []string  `json:"languages"`
	Lines      string    `json:"lines"` // the name of the JSON-lines file
}

// Line is one line of data.jsonl: the shape other speech tools read (an audio path and its text), plus
// the fields of the entry.
type Line struct {
	Audio   string    `json:"audio"` // "audio/<id>.wav"
	Text    string    `json:"text"`  // the transcript
	Prompt  string    `json:"prompt,omitempty"`
	Lang    string    `json:"lang"`
	Source  string    `json:"source"`
	Seconds float64   `json:"seconds"`
	Hints   []string  `json:"hints,omitempty"`
	Created time.Time `json:"created"`
	SHA256  string    `json:"sha256"`
	ID      string    `json:"id"`
}

var audioPath = regexp.MustCompile(`^audio/[0-9a-f]{24}\.wav$`)

// Export writes the dataset as a zip: manifest.json, data.jsonl and audio/<id>.wav (16 kHz mono 16-bit
// PCM). Only the entries chosen by keep are written (nil keeps all). It returns the manifest.
func (d *Dataset) Export(w io.Writer, keep func(Entry) bool) (Manifest, error) {
	zw := zip.NewWriter(w)
	m := Manifest{Format: FormatName, Version: FormatVersion, SampleRate: SampleRate, Channels: 1, Encoding: "pcm_s16le", Lines: linesName}
	langs := map[string]bool{}
	var lines bytes.Buffer
	enc := json.NewEncoder(&lines)
	var newest time.Time
	for _, e := range d.List() {
		if keep != nil && !keep(e) {
			continue
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: "audio/" + e.ID + ".wav", Method: zip.Store, Modified: e.Created})
		if err != nil {
			return m, err
		}
		if _, err := fw.Write(e.WAV); err != nil {
			return m, err
		}
		if err := enc.Encode(Line{Audio: "audio/" + e.ID + ".wav", Text: e.Transcript, Prompt: e.Prompt, Lang: e.Lang, Source: e.Source,
			Seconds: e.Seconds, Hints: e.Hints, Created: e.Created, SHA256: e.SHA256, ID: e.ID}); err != nil {
			return m, err
		}
		m.Count++
		m.Seconds += e.Seconds
		langs[e.Lang] = true
		if e.Created.After(newest) {
			newest = e.Created
		}
	}
	for l := range langs {
		m.Languages = append(m.Languages, l)
	}
	sort.Strings(m.Languages)
	m.Created = newest // no export time: the same dataset gives the same bytes
	lw, err := zw.CreateHeader(&zip.FileHeader{Name: linesName, Method: zip.Deflate, Modified: newest})
	if err != nil {
		return m, err
	}
	if _, err := lw.Write(lines.Bytes()); err != nil {
		return m, err
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Deflate, Modified: newest})
	if err != nil {
		return m, err
	}
	if _, err := fw.Write(append(mb, '\n')); err != nil {
		return m, err
	}
	return m, zw.Close()
}

// Limits when reading an import, so a hostile zip cannot exhaust memory.
const (
	maxImportBytes   = 512 << 20 // all uncompressed content
	maxImportEntries = 5000
	maxLineBytes     = 64 << 10
)

// ImportResult says what an import did.
type ImportResult struct {
	Added     int      `json:"added"`
	Duplicate int      `json:"duplicate"` // already in the dataset, left as it was
	Problems  []string `json:"problems,omitempty"`
}

// Import reads a zip written by Export. Each recording is checked (path form, SHA-256, WAV format) and
// one that fails is reported and left out, without stopping the others. A zip that is not an export, is
// of a newer version, or is larger than the limits is refused whole.
func (d *Dataset) Import(zipBytes []byte) (ImportResult, error) {
	var res ImportResult
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return res, errors.New("this is not a zip file")
	}
	if len(zr.File) > maxImportEntries {
		return res, errors.New("the file holds too many items")
	}
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > maxImportBytes {
			return res, errors.New("the file is too large")
		}
		files[f.Name] = f
	}
	read := func(name string, limit int64) ([]byte, error) {
		f := files[name]
		if f == nil {
			return nil, fmt.Errorf("%s is missing", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("%s is too large", name)
		}
		return b, nil
	}
	mb, err := read(manifestName, 1<<20)
	if err != nil {
		return res, errors.New("this is not a speech recordings export")
	}
	var m Manifest
	if json.Unmarshal(mb, &m) != nil || m.Format != FormatName {
		return res, errors.New("this is not a speech recordings export")
	}
	if m.Version != FormatVersion {
		return res, fmt.Errorf("this export is version %d; this editor reads version %d", m.Version, FormatVersion)
	}
	if m.SampleRate != SampleRate || m.Channels != 1 || m.Encoding != "pcm_s16le" {
		return res, errors.New("the audio is not 16 kHz mono 16-bit")
	}
	lb, err := read(m.Lines, maxImportBytes)
	if err != nil || m.Lines != linesName {
		return res, errors.New("the list of recordings is missing")
	}
	for n, raw := range bytes.Split(lb, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		prob := func(msg string) { res.Problems = append(res.Problems, fmt.Sprintf("line %d: %s", n+1, msg)) }
		if len(raw) > maxLineBytes {
			prob("too long")
			continue
		}
		var l Line
		if json.Unmarshal(raw, &l) != nil {
			prob("not valid")
			continue
		}
		if !audioPath.MatchString(l.Audio) {
			prob("unexpected audio path")
			continue
		}
		wav, err := read(l.Audio, 44+2*MaxSamples)
		if err != nil {
			prob(err.Error())
			continue
		}
		sum := sha256.Sum256(wav)
		if hex.EncodeToString(sum[:]) != l.SHA256 {
			prob("the audio does not match its checksum")
			continue
		}
		pcm, err := DecodeWAV(wav)
		if err != nil {
			prob(err.Error())
			continue
		}
		if len(l.Text) > 4096 || len(l.Prompt) > 4*MaxPhraseLen || len(l.Lang) > 16 || (l.Source != Prompted && l.Source != Dictation) {
			prob("unexpected fields")
			continue
		}
		// the ID is made again here: a file cannot choose where it lands
		e := NewEntry(cleanLine(l.Lang), cleanLine(l.Prompt), cleanLine(l.Text), l.Source, pcm, cleanHints(l.Hints), l.Created)
		if d.Get(e.ID) != nil {
			res.Duplicate++
			continue
		}
		d.Add(e)
		res.Added++
	}
	return res, nil
}

func cleanHints(h []string) []string {
	var out []string
	for _, s := range h {
		switch s {
		case HintSilent, HintTooShort, HintTooQuiet, HintClipping, HintNoisy, HintLongPause:
			out = append(out, s)
		}
	}
	return out
}

// EncodeEntry is one entry as bytes (a JSON header and the WAV), for keeping each recording on its own.
func EncodeEntry(e Entry) []byte {
	h, _ := json.Marshal(e)
	var b bytes.Buffer
	fmt.Fprintf(&b, "GJSE1\n%d\n", len(h))
	b.Write(h)
	b.Write(e.WAV)
	return b.Bytes()
}

// DecodeEntry reads what EncodeEntry wrote and checks the audio against the checksum.
func DecodeEntry(b []byte) (Entry, error) {
	var e Entry
	rest, ok := strings.CutPrefix(string(b), "GJSE1\n")
	if !ok {
		return e, errors.New("speechcoach: not a recording")
	}
	var n int
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return e, errors.New("speechcoach: not a recording")
	}
	if _, err := fmt.Sscanf(rest[:nl], "%d", &n); err != nil || n < 0 || nl+1+n > len(rest) {
		return e, errors.New("speechcoach: not a recording")
	}
	body := rest[nl+1:]
	if json.Unmarshal([]byte(body[:n]), &e) != nil {
		return e, errors.New("speechcoach: not a recording")
	}
	e.WAV = []byte(body[n:])
	sum := sha256.Sum256(e.WAV)
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		return Entry{}, errors.New("speechcoach: the recording does not match its checksum")
	}
	if _, err := DecodeWAV(e.WAV); err != nil {
		return Entry{}, err
	}
	return e, nil
}
