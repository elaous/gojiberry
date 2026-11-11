package canvas

import (
	"context"
	"errors"
	"testing"
)

func TestAnIDThatIsNotAUUIDIsNotFound(t *testing.T) {
	s := &Store{}
	if _, err := s.GetDocument(context.Background(), "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if err := s.SaveDocument(context.Background(), "../etc", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("save: %v", err)
	}
}
