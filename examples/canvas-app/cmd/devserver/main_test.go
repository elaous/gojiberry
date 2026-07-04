package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/elaous/gojiberry/examples/canvas-app/canvas"
)

// TestDevserverEndToEnd exercises the whole create -> view -> save ->
// reload -> list flow against a real Postgres (started directly on
// this machine; see the README for why not a Docker container).
func TestDevserverEndToEnd(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://canvas:canvas@127.0.0.1:5432/canvas?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("no reachable postgres at %s: %v", dsn, err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM documents`) })

	store := canvas.NewStore(db)
	srv := httptest.NewServer(newMux(store))
	defer srv.Close()

	// Create a document.
	resp, err := http.Post(srv.URL+"/documents", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("POST /documents: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /documents: status %d (want 200 after following the redirect)", resp.StatusCode)
	}
	docURL := resp.Request.URL.String()
	if !strings.Contains(docURL, "/documents/") {
		t.Fatalf("expected redirect to /documents/{id}, ended up at %s", docURL)
	}
	id := docURL[strings.LastIndex(docURL, "/")+1:]

	// The freshly created document's canvas page should render with empty content.
	resp, err = http.Get(docURL)
	if err != nil {
		t.Fatalf("GET %s: %v", docURL, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `id="canvas"`) {
		t.Fatalf("expected canvas page markup, got: %s", body)
	}

	// Save some content via the same PUT endpoint the WASM client uses.
	saveReq, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/documents/"+id,
		strings.NewReader(`{"content":"hello from the test"}`))
	saveReq.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(saveReq)
	if err != nil {
		t.Fatalf("PUT /api/documents/%s: %v", id, err)
	}
	saveBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var saveResult map[string]bool
	if err := json.Unmarshal(saveBody, &saveResult); err != nil || !saveResult["ok"] {
		t.Fatalf("unexpected save response: %s (err %v)", saveBody, err)
	}

	// Reloading the canvas page should reflect the saved content.
	resp, err = http.Get(docURL)
	if err != nil {
		t.Fatalf("GET %s: %v", docURL, err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello from the test") {
		t.Fatalf("expected saved content in reloaded page, got: %s", body)
	}

	// The home page should list the document with its preview text.
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello from the test") {
		t.Fatalf("expected home page to list the document's preview, got: %s", body)
	}

	// Saving to a document that doesn't exist should fail.
	badReq, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/documents/does-not-exist",
		strings.NewReader(`{"content":"x"}`))
	badReq.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("PUT to missing document: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected an error saving a nonexistent document, got status %d", resp.StatusCode)
	}
}
