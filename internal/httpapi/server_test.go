package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"pc-club-admin/internal/config"
	"strings"
	"testing"
)

func TestHealthAndReadiness(t *testing.T) {
	for _, tt := range []struct {
		name, env, path, reason string
		databaseError           bool
		want                    int
	}{
		{"liveness ignores database", "test", "/healthz", "", true, 200},
		{"ready schema", "test", "/readyz", "", false, 200},
		{"unavailable schema", "test", "/readyz", "DATABASE_OR_SCHEMA_UNAVAILABLE", true, 503},
		{"production policy blocks", "production", "/readyz", "CASH_POLICY_UNVERIFIED", false, 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			check := func(context.Context) error {
				if tt.databaseError {
					return errors.New("postgres://secret:password@db/internal")
				}
				return nil
			}
			handler := New(config.Config{Environment: tt.env}, check, slog.New(slog.NewTextHandler(io.Discard, nil)))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if response.Code != tt.want {
				t.Fatalf("got %d want %d", response.Code, tt.want)
			}
			if tt.reason != "" && !strings.Contains(response.Body.String(), tt.reason) {
				t.Fatalf("missing reason: %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "password") {
				t.Fatal("internal error exposed")
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("request ID missing")
			}
		})
	}
}

func TestNoOperationalEndpoints(t *testing.T) {
	handler := New(config.Config{Environment: "test"}, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/api/v1/shifts/bootstrap", "/api/v1/handovers/example/accept"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if response.Code != 404 {
			t.Fatalf("operation %s unexpectedly available: %d", path, response.Code)
		}
	}
}

func TestFrontendAndAPIBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html lang=\"ru\">PC Club Admin</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := New(config.Config{Environment: "test", WebDistDir: dir}, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range []struct {
		path    string
		want    int
		content string
	}{
		{"/", 200, "PC Club Admin"},
		{"/api/v1/missing", 404, "NOT_FOUND"},
		{"/missing.js", 404, "404"},
		{"/api/v1/status", 200, `"operations_enabled":false`},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if response.Code != tt.want || !strings.Contains(response.Body.String(), tt.content) {
			t.Fatalf("%s: %d %s", tt.path, response.Code, response.Body.String())
		}
	}
}
