package canvas

import (
	"context"
	"net/http"
	"sync"

	"encore.dev/storage/sqldb"

	"github.com/elaous/gojiberry/xango"
)

// encoreStore wires the same Store business logic (store.go) that
// cmd/devserver's plain net/http handlers use into a real Encore
// -managed Postgres database. This much compiles against the genuine
// encore.dev module (added as a real dependency, confirmed via `go
// doc encore.dev/storage/sqldb` against the actual published API) --
// but the Encore CLI itself couldn't be installed in this sandbox
// (network policy blocked the installer), so the actual `encore run`
// experience (does this database provision correctly, do the
// //encore:api annotations route as expected, does a raw endpoint's
// http.Request carry path params via r.PathValue like Go 1.22's
// stdlib mux) is unverified. cmd/devserver + a real Postgres started
// directly via Docker is what's genuinely tested in this repo; see
// the README.
//
// Lazily initialized (not a package-level var) so importing this
// package -- which cmd/devserver does, for the shared Store/pages --
// never calls sqldb.NewDatabase outside of an actual Encore-annotated
// handler actually being invoked, i.e. never outside `encore run`.
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
//
//encore:api public method=POST path=/documents
func CreateDocument(ctx context.Context) (*CreateDocumentResponse, error) {
	id, err := encoreStore().CreateDocument(ctx)
	if err != nil {
		return nil, err
	}
	return &CreateDocumentResponse{ID: id}, nil
}

// SaveDocument overwrites a document's content -- called by the WASM
// client's debounced autosave.
//
//encore:api public method=PUT path=/api/documents/:id
func SaveDocument(ctx context.Context, id string, req *SaveDocumentRequest) error {
	return encoreStore().SaveDocument(ctx, id, req.Content)
}

// ViewDocument serves the canvas page for one document. A raw
// endpoint (not a typed API) because it renders HTML via xango, not
// JSON.
//
//encore:api public raw method=GET path=/documents/:id
func ViewDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	content, err := encoreStore().GetDocument(r.Context(), id)
	if err != nil {
		http.Error(w, "document not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := xango.Render(w, CanvasPage(CanvasPageProps{ID: id, Content: content})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Home lists existing documents with a way to create a new one.
//
//encore:api public raw method=GET path=/
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
