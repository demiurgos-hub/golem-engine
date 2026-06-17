package host

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestValidateTLSFilesRequiresPair(t *testing.T) {
	if err := ValidateTLSFiles("cert.pem", ""); err == nil {
		t.Fatal("expected missing key to fail")
	}
	if err := ValidateTLSFiles("", "key.pem"); err == nil {
		t.Fatal("expected missing cert to fail")
	}
	if err := ValidateTLSFiles("cert.pem", "key.pem"); err != nil {
		t.Fatalf("ValidateTLSFiles pair: %v", err)
	}
	if err := ValidateTLSFiles("", ""); err != nil {
		t.Fatalf("ValidateTLSFiles empty pair: %v", err)
	}
}

func TestSPAHandlerDelegatesAPIPrefixAndFallsBackToIndex(t *testing.T) {
	apiCalled := false
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		_, _ = w.Write([]byte("api"))
	})
	handler := SPAHandler(SPAOptions{
		APIHandler: api,
		APIPrefix:  "/api",
		EmbeddedFS: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("index")},
			"app.js":     &fstest.MapFile{Data: []byte("app")},
		},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/realtime-config", nil))
	if !apiCalled || rec.Body.String() != "api" {
		t.Fatalf("api response = %q apiCalled=%v", rec.Body.String(), apiCalled)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing/route", nil))
	if !strings.Contains(rec.Body.String(), "index") {
		t.Fatalf("fallback response = %q", rec.Body.String())
	}
}

func TestSPAHandlerFallsBackToAPIOnlyWhenNoFS(t *testing.T) {
	handler := SPAHandler(SPAOptions{APIHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api-only"))
	})})
	if _, ok := handler.(fs.FS); ok {
		t.Fatal("handler unexpectedly implements fs.FS")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if rec.Body.String() != "api-only" {
		t.Fatalf("response = %q", rec.Body.String())
	}
}
