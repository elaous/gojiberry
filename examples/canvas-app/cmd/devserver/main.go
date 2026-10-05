// Command devserver runs the xango-canvas demo against a real Postgres
// using plain net/http -- no Encore CLI required. It exists because
// this sandbox's network policy blocked the Encore CLI installer, so
// this is how the app is actually run and tested in this repo; a real
// Encore deployment would use canvas/api.go's //encore:api-annotated
// handlers instead, which call into the exact same Store.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/devserver
//
// Set DEV=1 (cmd/devwatch does this) to inject a small live-reload
// script into every rendered page, via the gojiberry/devreload package:
// it polls GET /__dev/version and reloads the tab once the value
// changes, i.e. once devwatch has rebuilt and restarted this process.
// Off by default -- production (or a plain `go run` without devwatch)
// renders exactly the same bytes as before, no injected script.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/elaous/gojiberry/devreload"
	"github.com/elaous/gojiberry/examples/canvas-app/canvas"
	"github.com/elaous/gojiberry/xango"
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

	// A nil *devreload.Reloader outside dev mode makes Wrap a no-op and
	// Handler always 404 -- newMux and renderPage need no devMode branch
	// of their own, they just always call through Reloader.
	var reloader *devreload.Reloader
	if os.Getenv("DEV") == "1" {
		reloader = devreload.New()
	}
	mux := newMux(store, reloader)

	addr := ":8090"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	fmt.Println("xango-canvas devserver listening on", addr)
	if reloader != nil {
		fmt.Println("dev mode: live-reload script injected, GET", devreload.Path(), "enabled")
	}
	log.Fatal(http.ListenAndServe(addr, mux))
}

// newMux wires the same canvas.Store logic that canvas/api.go's Encore
// handlers use into a plain http.ServeMux (Go 1.22+ method+path-param
// patterns, no third-party router needed).
func newMux(store *canvas.Store, reloader *devreload.Reloader) *http.ServeMux {
	mux := http.NewServeMux()

	renderPage := func(w http.ResponseWriter, page xango.Node) error {
		html, err := xango.RenderString(page)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err = io.WriteString(w, reloader.Wrap(html))
		return err
	}

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		docs, err := store.ListDocuments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := renderPage(w, canvas.HomePage(canvas.HomePageProps{Documents: docs})); err != nil {
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
		if errors.Is(err, canvas.ErrNotFound) {
			http.Error(w, "document not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Println("reading a document:", err)
			http.Error(w, "could not read the document", http.StatusInternalServerError)
			return
		}
		if err := renderPage(w, canvas.CanvasPage(canvas.CanvasPageProps{ID: id, Content: content})); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("PUT /api/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req canvas.SaveDocumentRequest
		// A document is text typed in a box: a few MiB is far more than it needs.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := store.SaveDocument(r.Context(), id, req.Content); err != nil {
			if errors.Is(err, canvas.ErrNotFound) {
				http.Error(w, "document not found", http.StatusNotFound)
				return
			}
			log.Println("saving a document:", err)
			http.Error(w, "could not save the document", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Handler on a nil reloader serves 404, so this is safe to mount
	// unconditionally rather than gating it on dev mode too.
	mux.Handle(devreload.Path(), reloader.Handler())

	return mux
}
