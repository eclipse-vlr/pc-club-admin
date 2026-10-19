package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

// Check verifies database connectivity, schema compatibility and persisted settings.
type Check func(context.Context) error

type status struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func New(c config.Config, check Check, logger *slog.Logger, services ...*auth.Service) http.Handler {
	var access *auth.Service
	if len(services) > 0 {
		access = services[0]
	}
	return NewWithShifts(c, check, logger, access, nil)
}
func NewWithShifts(c config.Config, check Check, logger *slog.Logger, access *auth.Service, work *shifts.Service) http.Handler {
	mux := http.NewServeMux()
	if access != nil {
		authRoutes(mux, c, access)
	}
	if work != nil {
		shiftRoutes(mux, c, work)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, status{Status: "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if c.ProductionBlocked() {
			writeJSON(w, 503, status{Status: "blocked", Reason: "CASH_POLICY_UNVERIFIED"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := check(ctx); err != nil {
			writeJSON(w, 503, status{Status: "not_ready", Reason: "DATABASE_OR_SCHEMA_UNAVAILABLE"})
			return
		}
		writeJSON(w, 200, status{Status: "ready"})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, struct {
			Name              string `json:"name"`
			Stage             string `json:"stage"`
			Environment       string `json:"environment"`
			Timezone          string `json:"timezone"`
			CashPolicy        string `json:"cash_policy_status"`
			OperationsEnabled bool   `json:"operations_enabled"`
		}{"PC Club Admin", "cash-test", c.Environment, c.Timezone, c.CashPolicyStatus, false})
	})
	// Reserved namespace: unknown API routes must never return the frontend shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Маршрут недоступен", "request_id": w.Header().Get("X-Request-ID")}})
	})
	mux.Handle("/", frontend(c.WebDistDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		requestID := hex.EncodeToString(token[:])
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		start := time.Now()
		mux.ServeHTTP(w, r)
		// Never log query strings, cookies, bodies or arbitrary client request IDs.
		logger.Info("http request", "request_id", requestID, "method", r.Method, "duration_ms", time.Since(start).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func frontend(dir string) http.Handler {
	root := os.DirFS(dir)
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		name := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r.URL.Path)), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || strings.HasPrefix(name, ".") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(root, name); err != nil {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
