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

	"github.com/elaous/gojiberry/devreload"
	"github.com/elaous/gojiberry/examples/canvas-app/canvas"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://canvas:canvas@127.0.0.1:5432/canvas?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing the database: %v", err)
		}
	})
	if err := db.Ping(); err != nil {
		t.Skipf("SKIPPED, NOT TESTED: no reachable postgres at %s (%v). Set TEST_DATABASE_URL to a Postgres that has the documents table "+
			"(canvas/migrations/1_create_documents.up.sql); see \"Postgres-backed tests\" in docs/testing-workflow.md for how to start one locally", dsn, err)
	}
	return db
}

func TestDevserverEndToEnd(t *testing.T) {
	db := openTestDB(t)
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM documents`); err != nil {
			t.Errorf("emptying documents: %v", err)
		}
	})

	store := canvas.NewStore(db)
	srv := httptest.NewServer(newMux(store, nil))
	defer srv.Close()

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

	resp, err = http.Get(docURL)
	if err != nil {
		t.Fatalf("GET %s: %v", docURL, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `id="canvas"`) {
		t.Fatalf("expected canvas page markup, got: %s", body)
	}

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

	resp, err = http.Get(docURL)
	if err != nil {
		t.Fatalf("GET %s: %v", docURL, err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello from the test") {
		t.Fatalf("expected saved content in reloaded page, got: %s", body)
	}

	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello from the test") {
		t.Fatalf("expected home page to list the document's preview, got: %s", body)
	}

	badReq, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/documents/does-not-exist",
		strings.NewReader(`{"content":"x"}`))
	badReq.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("PUT to missing document: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 saving a nonexistent document, got status %d", resp.StatusCode)
	}
}

func TestDevModeInjectsLiveReloadScript(t *testing.T) {
	db := openTestDB(t)
	store := canvas.NewStore(db)

	devSrv := httptest.NewServer(newMux(store, devreload.New()))
	defer devSrv.Close()

	resp, err := http.Get(devSrv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `fetch("/__dev/version"`) || !strings.HasSuffix(strings.TrimSpace(string(body)), "</html>") {
		t.Fatalf("expected the live-reload script injected before </body>, got: %s", body)
	}

	resp, err = http.Get(devSrv.URL + "/__dev/version")
	if err != nil {
		t.Fatalf("GET /__dev/version: %v", err)
	}
	verBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(verBody) == 0 {
		t.Fatalf("expected a non-empty dev version, got status %d body %q", resp.StatusCode, verBody)
	}

	prodSrv := httptest.NewServer(newMux(store, nil))
	defer prodSrv.Close()

	resp, err = http.Get(prodSrv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "/__dev/version") {
		t.Fatalf("expected no live-reload script without DEV mode, got: %s", body)
	}

	resp, err = http.Get(prodSrv.URL + "/__dev/version")
	if err != nil {
		t.Fatalf("GET /__dev/version: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /__dev/version to not exist without DEV mode, got status %d", resp.StatusCode)
	}
}
