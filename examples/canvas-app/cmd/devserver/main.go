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
//
// Set DEV=1 (cmd/devwatch does this) to inject a small live-reload
// script into every rendered page: it polls GET /__dev/version and
// reloads the tab once the value changes, i.e. once devwatch has
// rebuilt and restarted this process. Off by default -- production
// (or a plain `go run` without devwatch) renders exactly the same
// bytes as before, no injected script, no /__dev/version route.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/elaous/gojiberry/examples/canvas-app/canvas"
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
	devMode := os.Getenv("DEV") == "1"
	mux := newMux(store, devMode)

	addr := ":8090"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	fmt.Println("gox-canvas devserver listening on", addr)
	if devMode {
		fmt.Println("dev mode: live-reload script injected, GET /__dev/version enabled")
	}
	log.Fatal(http.ListenAndServe(addr, mux))
}

// newMux wires the same canvas.Store logic that canvas/api.go's Encore
// handlers use into a plain http.ServeMux (Go 1.22+ method+path-param
// patterns, no third-party router needed).
func newMux(store *canvas.Store, devMode bool) *http.ServeMux {
	mux := http.NewServeMux()

	// devVersion identifies this process instance: cmd/devwatch kills
	// and restarts the whole process on every rebuild, which is enough
	// on its own to give each run a fresh value here -- no file or
	// signal needed to bump it.
	devVersion := strconv.FormatInt(time.Now().UnixNano(), 10)

	renderPage := func(w http.ResponseWriter, page gox.Node) error {
		if !devMode {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			return gox.Render(w, page)
		}
		html, err := gox.RenderString(page)
		if err != nil {
			return err
		}
		html = strings.Replace(html, "</body>", devReloadScript(devVersion)+"</body>", 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err = io.WriteString(w, html)
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
		if err != nil {
			http.Error(w, "document not found", http.StatusNotFound)
			return
		}
		if err := renderPage(w, canvas.CanvasPage(canvas.CanvasPageProps{ID: id, Content: content})); err != nil {
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

	if devMode {
		mux.HandleFunc("GET /__dev/version", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, devVersion)
		})
	}

	return mux
}

// devReloadScript polls /__dev/version and reloads the tab once it
// differs from the value the page was rendered with -- i.e. once
// devwatch has rebuilt and restarted the server. A failed fetch (the
// old process already exited, the new one isn't listening yet) is
// treated as "not ready yet", not "reload now".
func devReloadScript(version string) string {
	return `<script>` + fmt.Sprintf(`
(function () {
	var v = %q;
	setInterval(function () {
		fetch("/__dev/version", { cache: "no-store" }).then(function (r) {
			return r.text();
		}).then(function (t) {
			if (t !== v) location.reload();
		}).catch(function () {});
	}, 500);
})();
`, version) + `</script>`
}
