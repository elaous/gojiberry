package docstorecmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func TestTelemetryIsOffUnlessTheOperatorSetsIt(t *testing.T) {
	post := func(srv *docstore.Server) int {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/telemetry", strings.NewReader("{}")))
		return rec.Code
	}
	for _, v := range []string{"", "off", "true", "1", "no"} {
		srv := docstore.NewServer(docstore.NewMemoryAdapter(), "memory")
		if withTelemetry(srv, v) {
			t.Errorf("%q turned the receiver on", v)
		}
		if c := post(srv); c != http.StatusNotFound {
			t.Errorf("%q: /telemetry answers %d, want 404 (no such route)", v, c)
		}
	}
	srv := docstore.NewServer(docstore.NewMemoryAdapter(), "memory")
	if !withTelemetry(srv, " ON ") {
		t.Fatal("on was not honoured")
	}
	if c := post(srv); c != http.StatusBadRequest {
		t.Fatalf("with it on an empty report is refused: %d", c)
	}
}
