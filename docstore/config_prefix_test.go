package docstore

import (
	"net/http"
	"strings"
	"testing"
)

func TestConfigKeyspaceIsRefusedByTheDocumentAPI(t *testing.T) {
	srv := newTestServer()
	h := srv.Handler()
	if _, err := srv.adapter.Load(t.Context(), "config/x"); err == nil {
		t.Fatal("empty to start with")
	}
	_ = srv.adapter.Save(t.Context(), ConfigPrefix+"abc/prefs", []byte("secret setting"))
	for _, id := range []string{"config/abc/prefs", "CONFIG/abc/prefs", "Config/new"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			rec := serve(h, method, "/docs/"+id, "x")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s = %d, want 400", method, id, rec.Code)
			}
		}
	}
	if list := serve(h, "GET", "/docs", ""); strings.Contains(list.Body.String(), "config/") {
		t.Fatalf("the listing shows the keyspace: %s", list.Body)
	}
}
