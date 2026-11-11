package migrate

import (
	"context"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/saver"
)

type SaverSink struct {
	Saver  *saver.Saver
	Target string
	Folder string
	Codec  docfile.Codec

	mu      sync.Mutex
	folders map[string]string // path joined by "\x00" -> folder ID
}

// Save implements Sink.
func (s *SaverSink) Save(ctx context.Context, path []string, name, tag string, doc docfile.Document) error {
	folder, err := s.folderFor(ctx, path)
	if err != nil {
		return err
	}
	doc.SetID("")
	doc.SetName(name)
	doc.SetFolder(folder)
	if tag != "" {
		doc.SetTags(append(doc.Tags(), tag))
	}
	c := s.Codec
	if c == nil {
		c = docfile.JSON
	}
	_, err = s.Saver.Save(ctx, s.Target, c, doc)
	return err
}

func (s *SaverSink) folderFor(ctx context.Context, path []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.folders == nil {
		s.folders = map[string]string{}
	}
	parent := s.Folder
	for i := range path {
		key := strings.Join(path[:i+1], "\x00")
		if id, ok := s.folders[key]; ok {
			parent = id
			continue
		}
		id, err := s.findOrCreate(ctx, parent, path[i])
		if err != nil {
			return "", err
		}
		s.folders[key] = id
		parent = id
	}
	return parent, nil
}

func (s *SaverSink) findOrCreate(ctx context.Context, parent, name string) (string, error) {
	existing, err := s.Saver.Folders(ctx, s.Target)
	if err != nil {
		return "", err
	}
	for _, f := range existing {
		if f.Parent == parent && f.Name == name {
			return f.ID, nil
		}
	}
	f, err := s.Saver.CreateFolder(ctx, s.Target, parent, name)
	return f.ID, err
}
