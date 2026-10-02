package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	for input, want := range map[string]string{
		"My Novel":       "My Novel",
		"../draft:01":    "draft_01",
		"bad/name?.epub": "name_.epub",
		"":               "novel",
	} {
		if got := safeFilename(input); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEmbeddedStudioAndSecurityHeaders(t *testing.T) {
	server, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5173/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "AINOVEL Local Studio") {
		t.Fatal("embedded index is not the current studio build")
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing Content-Security-Policy")
	}

	recorder = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:5173/api/start", strings.NewReader(`{"prompt":"test"}`))
	req.Header.Set("Origin", "https://example.com")
	server.routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", recorder.Code)
	}
}

func TestSameOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:5173/api/start", nil)
	if !sameOrigin(req) {
		t.Fatal("requests without Origin should be accepted for local CLI clients")
	}
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	if !sameOrigin(req) {
		t.Fatal("same-origin browser request should be accepted")
	}
	req.Header.Set("Origin", "https://example.com")
	if sameOrigin(req) {
		t.Fatal("cross-origin browser request should be rejected")
	}
}
