package docstore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// UseShareLink makes every request of this adapter carry the share token, so a person who opened a link
// gets the role it gives (until it expires or is revoked). An empty token removes it.
func (a *HTTPAdapter) UseShareLink(token string) {
	if token == "" {
		a.Header.Del(ShareHeader)
		return
	}
	a.Header.Set(ShareHeader, token)
}

// CreateLink asks the server for a share link on document id (the caller must own it): a role (RoleViewer
// or RoleEditor) and how long it lasts (DefaultLinkLife if zero). The token is in the answer and nowhere
// else, so it is shown once.
func (a *HTTPAdapter) CreateLink(ctx context.Context, id string, role Role, life time.Duration) (token, linkID string, expires time.Time, err error) {
	body, _ := json.Marshal(createLinkRequest{Role: role, Hours: life.Hours()})
	resp, err := a.do(ctx, http.MethodPost, docPath(id)+"/acl", body)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", "", time.Time{}, httpError(resp)
	}
	var out createLinkResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&out); err != nil {
		return "", "", time.Time{}, err
	}
	return out.Token, out.ID, out.Expires, nil
}

// RevokeLink ends a share link of document id at once (the caller must own it).
func (a *HTTPAdapter) RevokeLink(ctx context.Context, id, linkID string) error {
	resp, err := a.do(ctx, http.MethodDelete, docPath(id)+"/acl?link="+url.QueryEscape(linkID), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return httpError(resp)
	}
	return nil
}

// LoadACL reads the access list of document id (the caller must own it).
func (a *HTTPAdapter) LoadACL(ctx context.Context, id string) (ACL, error) {
	resp, err := a.do(ctx, http.MethodGet, docPath(id)+"/acl", nil)
	if err != nil {
		return ACL{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ACL{}, httpError(resp)
	}
	var acl ACL
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&acl)
	return acl, err
}

// SaveACL replaces the people on the access list of document id (who has what role, and until when) with
// acl's Grants and Until; the first save by a signed-in person makes them the owner. The owner is never changed
// and the share links are left as they are (they are made and revoked by CreateLink and RevokeLink). The
// caller must own the document, or it must have no list yet.
func (a *HTTPAdapter) SaveACL(ctx context.Context, id string, acl ACL) error {
	body, err := json.Marshal(ACL{Grants: acl.Grants, Until: acl.Until})
	if err != nil {
		return err
	}
	resp, err := a.do(ctx, http.MethodPut, docPath(id)+"/acl", body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return httpError(resp)
	}
	return nil
}
