package migrate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

// PackFormat is the name of the choice "one .opendoc pack" in a Session's formats: the
// migrated documents are not saved in a store but gathered into one pack that is handed
// over (Session.Deliver).
const PackFormat = "opendoc"

// PackSink gathers migrated documents into a .opendoc pack in memory, the folders of the
// source becoming folders of the pack.
type PackSink struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	w       *pack.Writer
	folders map[string]string // path joined by "\x00" -> folder ID
	n       int
}

// NewPackSink starts a pack called name.
func NewPackSink(name string) (*PackSink, error) {
	s := &PackSink{folders: map[string]string{}}
	w, err := pack.NewWriter(&s.buf, pack.Meta{ID: docfile.NewID(), Name: name, Created: time.Now().UnixNano(), Producer: "gojiberry migrate"})
	if err != nil {
		return nil, err
	}
	s.w = w
	return s, nil
}

// Save implements Sink.
func (s *PackSink) Save(_ context.Context, path []string, name, tag string, doc docfile.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent := ""
	for i := range path {
		key := strings.Join(path[:i+1], "\x00")
		id, ok := s.folders[key]
		if !ok {
			id = docfile.NewID()
			if err := s.w.AddFolder(pack.Folder{ID: id, Name: path[i], Parent: parent}); err != nil {
				return err
			}
			s.folders[key] = id
		}
		parent = id
	}
	doc.SetID(docfile.NewID())
	doc.SetName(name)
	doc.SetFolder(parent)
	if tag != "" {
		doc.SetTags(append(doc.Tags(), tag))
	}
	if err := s.w.Add(doc); err != nil {
		return err
	}
	s.n++
	return nil
}

// Bytes finishes the pack (signing it with key if not nil) and returns its bytes, and how
// many documents it holds.
func (s *PackSink) Bytes(key ed25519.PrivateKey) ([]byte, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.w.Close(key); err != nil {
		return nil, 0, err
	}
	return s.buf.Bytes(), s.n, nil
}
