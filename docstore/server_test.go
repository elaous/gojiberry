package docstore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *Server {
	return NewServer(NewMemoryAdapter(), "memory")
}

func TestServerHealthReportsBackend(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"backend":"memory"`) {
		t.Fatalf("body = %q, want it to mention backend=memory", rec.Body.String())
	}
}

func TestServerPutGetDeleteRoundTrip(t *testing.T) {
	srv := newTestServer()
	handler := srv.Handler()

	put := httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", rec.Code)
	}

	get := httptest.NewRequest(http.MethodGet, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("GET body = %q, want hello", rec.Body.String())
	}

	del := httptest.NewRequest(http.MethodDelete, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}

	get2 := httptest.NewRequest(http.MethodGet, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get2)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404", rec.Code)
	}
}

func TestServerGetMissingReturns404(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/docs/nope", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServerListReturnsSavedMetadata(t *testing.T) {
	srv := newTestServer()
	handler := srv.Handler()

	for _, id := range []string{"a", "b"} {
		req := httptest.NewRequest(http.MethodPut, "/docs/"+id, strings.NewReader("x"))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"a"`) || !strings.Contains(body, `"b"`) {
		t.Fatalf("list body = %q, want both a and b", body)
	}
}

func TestServerCorsPreflightShortCircuits(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodOptions, "/docs/doc1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header on preflight response")
	}
}

func TestServerMissingIDReturns400(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
