package docstore

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// A version history lives under keys of its own ("versions/<id>/<time><ext>" and "versions/<id>/meta.json", the
// saver's layout), which have no access list: the history of a shared document must follow the document's. So a
// request for such a key is decided by the access list of the document it belongs to (versionACL): a viewer can read
// the history (to compare and to see who changed what), only an owner or editor can add, name, keep, restore from or
// delete a version, a share link works as it does for the document, and a stranger can do nothing. A history whose
// document has no access list is open or closed as the store is for any unshared key (RequireACL), as before.

// versionDocID is the document id a version key belongs to ("versions/<id>/<name>", with exactly one slash after the
// id), or false for any other key.
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

// versionACL finds the access list of the document id a version history belongs to: its own, else the one of a document
// whose logical id it is (the saver's id "plan" for the stored "plan.open"). false when the document has none.
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
