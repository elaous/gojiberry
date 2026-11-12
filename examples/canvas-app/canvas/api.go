package canvas

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"encore.dev/storage/sqldb"

	"github.com/elaous/xango"
)

var (
	encoreStoreOnce sync.Once
	encoreStoreVal  *Store
)

func encoreStore() *Store {
	encoreStoreOnce.Do(func() {
		db := sqldb.NewDatabase("canvas", sqldb.DatabaseConfig{Migrations: "migrations"})
		encoreStoreVal = NewStore(db.Stdlib())
	})
	return encoreStoreVal
}

// CreateDocumentResponse is the response for CreateDocument.
type CreateDocumentResponse struct {
	ID string `json:"id"`
}

// SaveDocumentRequest is the request body for SaveDocument.
type SaveDocumentRequest struct {
	Content string `json:"content"`
}

// CreateDocument creates a new, empty document.
func CreateDocument(ctx context.Context) (*CreateDocumentResponse, error) {
	id, err := encoreStore().CreateDocument(ctx)
	if err != nil {
		return nil, err
	}
	return &CreateDocumentResponse{ID: id}, nil
}

func SaveDocument(ctx context.Context, id string, req *SaveDocumentRequest) error {
	return encoreStore().SaveDocument(ctx, id, req.Content)
}

// ViewDocument serves the canvas page for one document.
func ViewDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	content, err := encoreStore().GetDocument(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "could not read the document", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := xango.Render(w, CanvasPage(CanvasPageProps{ID: id, Content: content})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func Home(w http.ResponseWriter, r *http.Request) {
	docs, err := encoreStore().ListDocuments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := xango.Render(w, HomePage(HomePageProps{Documents: docs})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
