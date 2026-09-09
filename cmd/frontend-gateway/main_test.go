package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayServesSPAAndStaticAsset(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<main>gopher-spa</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "app.js"), []byte("app-static"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	backendURL, _ := url.Parse(backend.URL)
	handler, err := newGatewayHandler(dist, backendURL)
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{"/ai-chat": "gopher-spa", "/app.js": "app-static"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), expected) || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("unexpected static response for %s: %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGatewayRewritesAPIAndPreservesHealth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, request.URL.Path+"?"+request.URL.RawQuery)
	}))
	defer backend.Close()
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	backendURL, _ := url.Parse(backend.URL)
	handler, err := newGatewayHandler(dist, backendURL)
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		"/api/evaluations/collaboration/latest?view=summary": "/api/v1/evaluations/collaboration/latest?view=summary",
		"/health/ready": "/health/ready?",
		"/metrics":      "/metrics?",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != expected {
			t.Fatalf("unexpected proxy response for %s: %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGatewayRequiresBuiltIndex(t *testing.T) {
	backendURL, _ := url.Parse("http://127.0.0.1:9090")
	if _, err := newGatewayHandler(t.TempDir(), backendURL); err == nil {
		t.Fatal("missing frontend build was accepted")
	}
}
