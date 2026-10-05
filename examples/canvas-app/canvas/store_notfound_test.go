package canvas

import (
	"context"
	"errors"
	"testing"
)

// An ID that is not a UUID is a document that does not exist (404), not a database
// error (500); no database is needed to know that.
func TestAnIDThatIsNotAUUIDIsNotFound(t *testing.T) {
	s := &Store{}
	if _, err := s.GetDocument(context.Background(), "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if err := s.SaveDocument(context.Background(), "../etc", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("save: %v", err)
	}
}
