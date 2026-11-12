package main

import (
	"compress/gzip"
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
	"github.com/elaous/gojiberry/wasmserve"
	"github.com/elaous/xango"
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
	defer func() {
		if err := db.Close(); err != nil {
			log.Println("closing the database:", err)
		}
	}()
	if err := db.Ping(); err != nil {
		log.Fatalf("pinging database: %v", err)
	}

	store := canvas.NewStore(db)

	// nil Reloader outside dev: Wrap is a no-op, Handler 404s
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
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			log.Println("writing the save response:", err)
		}
	})

	mux.Handle("/static/", http.StripPrefix("/static/", wasmserve.Handler(os.DirFS("static"), wasmserve.Options{GzipLevel: gzip.BestSpeed})))

	mux.Handle(devreload.Path(), reloader.Handler())

	return mux
}
