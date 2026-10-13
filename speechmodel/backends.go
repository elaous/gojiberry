package speechmodel

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/speechcoach"
)

func init() {
	Register(stubBackend{})
	Register(httpBackend{})
}

// The "stub" backend is a model that answers with fixed text and wears adapters, so that the editor's
// dictation, correction and contribution flows can be built and tested before a real model exists. It is
// never a recogniser: its text is whatever the Spec's URL field holds ("" answers "stub").
type stubBackend struct{}

func (stubBackend) Name() string { return "stub" }
func (stubBackend) Open(_ context.Context, s Spec) (Model, error) {
	text := s.URL
	if text == "" {
		text = "stub"
	}
	return &Stub{Text: text, base: strings.ToLower(strings.TrimSpace(s.SHA256))}, nil
}

// Stub is the model of the "stub" backend.
type Stub struct {
	Text string

	mu      sync.Mutex
	base    string
	adapter *Adapter
	closed  bool
}

// Info implements Model.
func (s *Stub) Info() Info {
	return Info{Name: "stub", Version: "0", SampleRate: 16000, SHA256: s.base, Adaptable: true}
}

// Transcribe implements Model.
func (s *Stub) Transcribe(ctx context.Context, pcm []int16, _ Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Result{}, errors.New("speechmodel: the model is closed")
	}
	if len(pcm) == 0 {
		return Result{}, nil
	}
	return Result{Text: s.Text, Confidence: 1}, nil
}

// Apply implements Adaptable.
func (s *Stub) Apply(a *Adapter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a != nil && s.base != "" && a.Base != s.base {
		return ErrWrongBase
	}
	s.adapter = a
	return nil
}

// Adapter is the adapter being worn, nil if none.
func (s *Stub) Adapter() *Adapter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adapter
}

// Close implements Model.
func (s *Stub) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

// The "http" backend reaches a recogniser with the contract of cmd/voiceserver (package speechcoach's
// Recogniser): the audio goes to Spec.URL when a transcription is asked for, and nowhere else. It takes no
// adapter; a personalised model behind the URL is the service's business.
type httpBackend struct{}

func (httpBackend) Name() string { return "http" }
func (httpBackend) Open(_ context.Context, s Spec) (Model, error) {
	if _, err := speechcoach.CheckEndpoint(s.URL); err != nil {
		return nil, err
	}
	return &remote{r: &speechcoach.Recogniser{URL: s.URL, Token: s.Token}}, nil
}

type remote struct{ r *speechcoach.Recogniser }

func (m *remote) Info() Info { return Info{Name: "http", Version: "0", SampleRate: 16000} }
func (m *remote) Transcribe(ctx context.Context, pcm []int16, o Options) (Result, error) {
	t, err := m.r.Transcribe(ctx, pcm, o.Prompt, o.Language)
	return Result{Text: t}, err
}
func (m *remote) Close() error { return nil }
