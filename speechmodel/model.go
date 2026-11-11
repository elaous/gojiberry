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

// Model recognises speech.
type Model interface {
	Info() Info
	Transcribe(ctx context.Context, pcm []int16, opt Options) (Result, error)
	Close() error
}

// Adaptable is a Model that can wear a per-person Adapter.
type Adaptable interface {
	Model
	Apply(a *Adapter) error
}

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

type Trainer interface {
	Train(ctx context.Context, examples []Example, base *Adapter, cfg TrainConfig) (*Adapter, error)
}

// Spec says which model to open.
type Spec struct {
	Backend  string // the registered backend
	Path     string
	URL      string // for a backend that reaches a service
	Token    string // optional bearer token for URL
	SHA256   string // hex digest the model file must have; required with Path
	MaxBytes int64  // largest model file read; 0 is DefaultMaxBytes
}

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

type Backend interface {
	Name() string
	// Open returns the model.
	Open(ctx context.Context, s Spec) (Model, error)
}

var (
	mu       sync.Mutex
	backends = map[string]Backend{}
)

// Register makes a backend available by name.
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
