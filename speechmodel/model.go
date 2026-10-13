// Package speechmodel is the seam between the editor and a speech recognition model that adapts to one
// person's voice (roadmap 56). It holds no model: it defines how a model is found, checked, opened and
// asked, how a small per-person adapter is applied to a shared base model, and how adapters trained on
// many devices are combined without any recording leaving a device (federated averaging with clipping and
// noise). A real model plugs in as a Backend; until then the "stub" and "http" backends let the rest of the
// product be built and tested.
//
// The package is written so that it can move to a repository of its own: it imports only the standard
// library and package speechcoach (for the WAV encoder and the HTTP recogniser).
//
// What is deliberately not here: the training step itself (a Trainer is an interface; a backend that can
// train supplies one), secure aggregation (the aggregator sees each clipped, noised update; see
// docs/speech-model.md), and any network upload (the caller decides, with the person's consent).
package speechmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// Info describes an opened model.
type Info struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Language   string `json:"language,omitempty"` // "" for a multilingual model
	SampleRate int    `json:"sampleRate"`
	SHA256     string `json:"sha256,omitempty"` // of the model file, checked before it was opened
	Adaptable  bool   `json:"adaptable"`        // takes an Adapter
}

// Options of one transcription.
type Options struct {
	Language string   // a code such as "de"; "" lets the model decide
	Prompt   string   // the vocabulary prompt (speechcoach.Vocabulary.Prompt)
	Hints    []string // words to favour, for a model that takes them
}

// Result of one transcription.
type Result struct {
	Text       string
	Confidence float64 // 0 to 1 when the model says, otherwise 0
}

// Model recognises speech. pcm is 16-bit mono audio at Info().SampleRate. A Model is safe for use from
// one goroutine at a time unless its backend says otherwise.
type Model interface {
	Info() Info
	Transcribe(ctx context.Context, pcm []int16, opt Options) (Result, error)
	Close() error
}

// Adaptable is a Model that can wear a per-person Adapter. Apply replaces the one worn; Apply(nil) takes
// it off. An adapter made for another base model is refused (ErrWrongBase).
type Adaptable interface {
	Model
	Apply(a *Adapter) error
}

// Example is one training example: a take and what it says, both from the person's own device.
type Example struct {
	PCM  []int16
	Text string
}

// TrainConfig bounds a training run.
type TrainConfig struct {
	Steps        int
	LearningRate float64
	Seed         int64
}

// Trainer fits an Adapter to examples, starting from base (nil starts a new one). It runs where the audio
// is, on the person's device; only the resulting Adapter (and from it an Update) can leave, never the
// examples.
type Trainer interface {
	Train(ctx context.Context, examples []Example, base *Adapter, cfg TrainConfig) (*Adapter, error)
}

// Spec says which model to open.
type Spec struct {
	Backend  string // the registered backend
	Path     string // a model file (checked against SHA256 before the backend sees it)
	URL      string // for a backend that reaches a service
	Token    string // optional bearer token for URL
	SHA256   string // hex digest the model file must have; required with Path
	MaxBytes int64  // largest model file read; 0 is DefaultMaxBytes
}

// DefaultMaxBytes is the largest model file read unless the Spec says otherwise (2 GiB).
const DefaultMaxBytes = 2 << 30

// Errors.
var (
	ErrNoBackend    = errors.New("speechmodel: no such backend")
	ErrNoDigest     = errors.New("speechmodel: a model file needs its SHA-256 digest")
	ErrBadDigest    = errors.New("speechmodel: the model file is not the one that was asked for")
	ErrTooLarge     = errors.New("speechmodel: the model file is too large")
	ErrWrongBase    = errors.New("speechmodel: the adapter was made for another model")
	ErrNotAdaptable = errors.New("speechmodel: this model does not take an adapter")
)

// Backend opens models of one kind (an ONNX runtime, a service, a test double).
type Backend interface {
	Name() string
	// Open returns the model. For a Spec with a Path the file has been verified already and is read from
	// the Path given.
	Open(ctx context.Context, s Spec) (Model, error)
}

var (
	mu       sync.Mutex
	backends = map[string]Backend{}
)

// Register makes a backend available by name. A later registration of the same name replaces it.
func Register(b Backend) {
	mu.Lock()
	defer mu.Unlock()
	backends[b.Name()] = b
}

// Backends lists the registered backend names.
func Backends() []string {
	mu.Lock()
	defer mu.Unlock()
	var n []string
	for k := range backends {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Open finds the backend, verifies the model file (when there is one) and opens the model.
func Open(ctx context.Context, s Spec) (Model, error) {
	mu.Lock()
	b, ok := backends[s.Backend]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (have %s)", ErrNoBackend, s.Backend, strings.Join(Backends(), ", "))
	}
	if s.Path != "" {
		if err := VerifyFile(s.Path, s.SHA256, s.MaxBytes); err != nil {
			return nil, err
		}
	}
	return b.Open(ctx, s)
}

// VerifyFile checks that the file at path is no larger than max (0 is DefaultMaxBytes) and has the given
// SHA-256 digest (hex). A model is code the machine will run on audio: it is pinned by digest, never taken
// on trust from where it was found.
func VerifyFile(path, wantHex string, max int64) error {
	want := strings.ToLower(strings.TrimSpace(wantHex))
	if want == "" {
		return ErrNoDigest
	}
	if max <= 0 {
		max = DefaultMaxBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > max {
		return ErrTooLarge
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, max+1)); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return ErrBadDigest
	}
	return nil
}
