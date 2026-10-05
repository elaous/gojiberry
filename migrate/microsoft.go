package migrate

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OneDrive is a Source over Microsoft Graph: the Word files (.docx) in a person's
// OneDrive and the folders holding them.
type OneDrive struct {
	// Token is an OAuth access token with the Files.Read scope.
	Token string
	// BaseURL is the API's address; "" is https://graph.microsoft.com/v1.0.
	BaseURL string
	// Client is the HTTP client; nil is http.DefaultClient.
	Client *http.Client
}

// Label implements Source.
func (*OneDrive) Label() string { return "Microsoft Word (OneDrive)" }

func (o *OneDrive) base() string {
	if o.BaseURL != "" {
		return strings.TrimSuffix(o.BaseURL, "/")
	}
	return "https://graph.microsoft.com/v1.0"
}

// List implements Source.
func (o *OneDrive) List(ctx context.Context, folder string) ([]Item, error) {
	u := o.base() + "/me/drive/root/children"
	if folder != "" {
		u = o.base() + "/me/drive/items/" + url.PathEscape(folder) + "/children"
	}
	u += "?" + url.Values{"$select": {"id,name,size,lastModifiedDateTime,folder,file"}, "$top": {"200"}}.Encode()
	var items []Item
	for u != "" {
		var out struct {
			Next  string `json:"@odata.nextLink"`
			Value []struct {
				ID, Name     string
				Size         int64
				LastModified string    `json:"lastModifiedDateTime"`
				Folder       *struct{} `json:"folder"`
				File         *struct{} `json:"file"`
			} `json:"value"`
		}
		if err := getJSON(ctx, o.Client, o.Token, u, &out); err != nil {
			return nil, err
		}
		for _, f := range out.Value {
			isFolder := f.Folder != nil
			if !isFolder && !strings.HasSuffix(strings.ToLower(f.Name), ".docx") {
				continue // Excel, PDFs and the rest are not documents to migrate
			}
			t, _ := time.Parse(time.RFC3339, f.LastModified)
			items = append(items, Item{ID: f.ID, Name: f.Name, Folder: isFolder, Modified: t, Size: f.Size})
		}
		if len(items) >= MaxFiles {
			break
		}
		u = out.Next
	}
	return items, nil
}

// Fetch implements Source.
func (o *OneDrive) Fetch(ctx context.Context, item Item) ([]byte, error) {
	return getBytes(ctx, o.Client, o.Token, o.base()+"/me/drive/items/"+url.PathEscape(item.ID)+"/content")
}
