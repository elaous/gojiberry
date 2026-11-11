package docstore

import (
	"context"
	"errors"
	"sort"
	"strings"
)

func versionDocID(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, versionsPrefix)
	if !ok || strings.Contains(rest, "..") {
		return "", false
	}
	id, name, ok := strings.Cut(rest, "/")
	if !ok || id == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return id, true
}

func (s *Server) versionACL(ctx context.Context, id string) (ACL, bool, error) {
	acl, err := s.loadACL(ctx, id)
	if err == nil {
		return acl, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ACL{}, false, err
	}
	metas, err := ListPrefix(ctx, s.adapter, aclKeyPrefix+id+".")
	if err != nil {
		return ACL{}, false, err
	}
	var keys []string
	for _, m := range metas {
		if doc := strings.TrimPrefix(m.ID, aclKeyPrefix); logicalID(doc) == id {
			keys = append(keys, doc)
		}
	}
	sort.Strings(keys)
	for _, doc := range keys {
		if acl, err := s.loadACL(ctx, doc); err == nil {
			return acl, true, nil
		} else if !errors.Is(err, ErrNotFound) {
			return ACL{}, false, err
		}
	}
	return ACL{}, false, nil
}
