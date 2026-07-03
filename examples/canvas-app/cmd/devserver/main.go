// Command devserver runs the gox-canvas demo against a real Postgres
// using plain net/http -- no Encore CLI required. It exists because
// this sandbox's network policy blocked the Encore CLI installer, so
// this is how the app is actually run and tested in this repo; a real
// Encore deployment would use canvas/api.go's //encore:api-annotated
// handlers instead, which call into the exact same Store.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/devserver
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/elaous/gojiberry-canvas-app/canvas"
	"github.com/elaous/gojiberry/gox"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("opening database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("pinging database: %v", err)
	}

	store := canvas.NewStore(db)
	mux := newMux(store)

	addr := ":8090"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	fmt.Println("gox-canvas devserver listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// newMux wires the same canvas.Store logic that canvas/api.go's Encore
// handlers use into a plain http.ServeMux (Go 1.22+ method+path-param
// patterns, no third-party router needed).
func newMux(store *canvas.Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		docs, err := store.ListDocuments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := gox.Render(w, canvas.HomePage(canvas.HomePageProps{Documents: docs})); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, r *http.Request) {
		id, err := store.CreateDocument(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/documents/"+id, http.StatusSeeOther)
	})

	mux.HandleFunc("GET /documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		content, err := store.GetDocument(r.Context(), id)
		if err != nil {
			http.Error(w, "document not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := gox.Render(w, canvas.CanvasPage(canvas.CanvasPageProps{ID: id, Content: content})); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("PUT /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req canvas.SaveDocumentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := store.SaveDocument(r.Context(), id, req.Content); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	return mux
}
