package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	docxMIME        = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	googleDocMIME   = "application/vnd.google-apps.document"
	googleFolderMIM = "application/vnd.google-apps.folder"
)

type GoogleDocs struct {
	// Token is an OAuth access token with the drive.readonly scope.
	Token string
	// BaseURL is the API's address; "" is https://www.googleapis.com.
	BaseURL string
	// Client is the HTTP client; nil is http.DefaultClient.
	Client *http.Client
}

// Label implements Source.
func (*GoogleDocs) Label() string { return "Google Docs" }

func (g *GoogleDocs) base() string {
	if g.BaseURL != "" {
		return strings.TrimSuffix(g.BaseURL, "/")
	}
	return "https://www.googleapis.com"
}

// List implements Source.
func (g *GoogleDocs) List(ctx context.Context, folder string) ([]Item, error) {
	if folder == "" {
		folder = "root"
	}
	q := fmt.Sprintf("'%s' in parents and trashed = false and (mimeType = '%s' or mimeType = '%s')", escapeQuery(folder), googleDocMIME, googleFolderMIM)
	var items []Item
	for page := ""; ; {
		v := url.Values{"q": {q}, "fields": {"nextPageToken,files(id,name,mimeType,modifiedTime,size)"}, "pageSize": {"100"}, "orderBy": {"folder,name"}}
		if page != "" {
			v.Set("pageToken", page)
		}
		var out struct {
			NextPageToken string `json:"nextPageToken"`
			Files         []struct {
				ID, Name, MimeType, ModifiedTime string
			} `json:"files"`
		}
		if err := getJSON(ctx, g.Client, g.Token, g.base()+"/drive/v3/files?"+v.Encode(), &out); err != nil {
			return nil, err
		}
		for _, f := range out.Files {
			t, _ := time.Parse(time.RFC3339, f.ModifiedTime)
			items = append(items, Item{ID: f.ID, Name: f.Name, Folder: f.MimeType == googleFolderMIM, Modified: t})
		}
		if out.NextPageToken == "" || len(items) >= MaxFiles {
			return items, nil
		}
		page = out.NextPageToken
	}
}

func (g *GoogleDocs) Fetch(ctx context.Context, item Item) ([]byte, error) {
	u := g.base() + "/drive/v3/files/" + url.PathEscape(item.ID) + "/export?" + url.Values{"mimeType": {docxMIME}}.Encode()
	return getBytes(ctx, g.Client, g.Token, u)
}

func (g *GoogleDocs) Versions(ctx context.Context, item Item, max int) ([]Version, error) {
	type rev struct {
		ID           string            `json:"id"`
		ModifiedTime string            `json:"modifiedTime"`
		ExportLinks  map[string]string `json:"exportLinks"`
	}
	var all []rev
	for page := ""; len(all) < 2000; {
		v := url.Values{"fields": {"nextPageToken,revisions(id,modifiedTime,exportLinks)"}, "pageSize": {"200"}}
		if page != "" {
			v.Set("pageToken", page)
		}
		var out struct {
			NextPageToken string `json:"nextPageToken"`
			Revisions     []rev  `json:"revisions"`
		}
		if err := getJSON(ctx, g.Client, g.Token, g.base()+"/drive/v3/files/"+url.PathEscape(item.ID)+"/revisions?"+v.Encode(), &out); err != nil {
			return nil, err
		}
		all = append(all, out.Revisions...)
		if out.NextPageToken == "" {
			break
		}
		page = out.NextPageToken
	}
	if len(all) > 0 {
		all = all[:len(all)-1] // the newest is the document as it is
	}
	if len(all) > max {
		all = all[len(all)-max:]
	}
	var out []Version
	for _, r := range all {
		link := r.ExportLinks[docxMIME]
		if link == "" {
			continue
		}
		if !g.trusts(link) {
			continue // the token goes only where the API itself lives
		}
		data, err := getBytes(ctx, g.Client, g.Token, link)
		if err != nil {
			return out, err
		}
		t, _ := time.Parse(time.RFC3339, r.ModifiedTime)
		out = append(out, Version{Saved: t, Data: data})
	}
	return out, nil
}

func (g *GoogleDocs) trusts(link string) bool {
	u, err := url.Parse(link)
	if err != nil {
		return false
	}
	b, err := url.Parse(g.base())
	if err != nil {
		return false
	}
	if u.Scheme == b.Scheme && u.Host == b.Host {
		return true
	}
	if g.BaseURL != "" {
		return false
	}
	return u.Scheme == "https" && (u.Host == "docs.google.com" || strings.HasSuffix(u.Host, ".googleapis.com") || u.Host == "www.googleapis.com")
}

func escapeQuery(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

func do(ctx context.Context, c *http.Client, token, u string) (*http.Response, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("migrate: %s said %s: %s", req.URL.Host, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func getJSON(ctx context.Context, c *http.Client, token, u string, into any) error {
	resp, err := do(ctx, c, token, u)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(into)
}

func getBytes(ctx context.Context, c *http.Client, token, u string) ([]byte, error) {
	resp, err := do(ctx, c, token, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("migrate: the file is over %d MiB", MaxFileBytes>>20)
	}
	return data, nil
}
