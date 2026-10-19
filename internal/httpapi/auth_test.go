package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"pc-club-admin/internal/config"
	"strings"
	"testing"
)

func TestOriginAndCookieBoundary(t *testing.T) {
	mux := http.NewServeMux()
	authRoutes(mux, config.Config{Origin: "https://club.example"}, nil)
	initial := httptest.NewRecorder()
	mux.ServeHTTP(initial, httptest.NewRequest("GET", "/api/v1/auth/csrf", nil))
	var body map[string]string
	if err := json.Unmarshal(initial.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cookie := initial.Result().Cookies()[0]
	if cookie.Name != "__Host-pcclub_csrf" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("HTTPS cookie boundary incorrect")
	}
	for _, tt := range []struct {
		origin, token, contentType string
		want                       int
	}{
		{"", body["csrf_token"], "application/json", 403},
		{"https://evil.example", body["csrf_token"], "application/json", 403},
		{"https://club.example", "wrong", "application/json", 403},
		{"https://club.example", body["csrf_token"], "text/plain", 400},
	} {
		req := httptest.NewRequest("POST", "/api/v1/auth/manager-login", strings.NewReader(`{}`))
		req.AddCookie(cookie)
		req.Header.Set("Origin", tt.origin)
		req.Header.Set("X-CSRF-Token", tt.token)
		req.Header.Set("Content-Type", tt.contentType)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != tt.want {
			t.Fatalf("got %d want %d", response.Code, tt.want)
		}
	}
}
func TestJSONFraming(t *testing.T) {
	for _, body := range []string{`{"unexpected":true}`, `{} {}`, `null trailing`, `null`, `[]`, ``} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if decode(req, &struct{}{}) == nil {
			t.Fatalf("accepted invalid payload %q", body)
		}
	}
}
