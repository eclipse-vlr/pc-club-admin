//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/storage/postgres"
)

func authDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil || raw == "" || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") || os.Getenv("APP_ENV") == "production" {
		t.Fatal("dedicated TEST_DATABASE_URL with *_test database required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("auth_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err := postgres.Migrate(ctx, pool, "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	return pool
}

type browser struct {
	t                   *testing.T
	h                   http.Handler
	cookies             map[string]*http.Cookie
	source, actor, csrf string
}

func (b *browser) call(method, path string, in any, want int) map[string]any {
	b.t.Helper()
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			b.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &body)
	req.RemoteAddr = b.source + ":1234"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", b.csrf)
	req.Header.Set("X-Actor-ID", b.actor)
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	b.h.ServeHTTP(response, req)
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	if response.Code != want {
		b.t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
	}
	result := map[string]any{}
	if response.Body.Len() > 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			if strings.HasPrefix(response.Body.String(), "[") {
				return map[string]any{"list": response.Body.String()}
			}
			b.t.Fatal(err)
		}
	}
	if csrf, ok := result["csrf_token"].(string); ok {
		b.csrf = csrf
	}
	if id, ok := result["identity"].(map[string]any); ok {
		b.actor = id["user"].(map[string]any)["id"].(string)
	}
	return result
}
func TestAuthenticationBoundary(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	repo := postgres.AuthRepository{Pool: pool}
	pepper := "synthetic-test-pepper-with-at-least-32-characters"
	service := auth.New(repo, pepper)
	password := "synthetic-manager-password"
	if err := service.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(ctx, auth.UserInput{Name: "Второй", Login: "another", Password: password, Roles: []string{"manager"}}); err == nil {
		t.Fatal("bootstrap repeated")
	}
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	handler := func(s *auth.Service) http.Handler {
		return httpapi.New(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), s)
	}
	newBrowser := func(source string) *browser {
		return &browser{t: t, h: handler(service), cookies: map[string]*http.Cookie{}, source: source}
	}
	manager := newBrowser("192.0.2.1")
	manager.call("GET", "/readyz", nil, 200)
	manager.call("GET", "/api/v1/auth/admin-options", nil, 403)
	manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 403)
	manager.call("GET", "/api/v1/auth/csrf", nil, 200)
	manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
	managerID := manager.actor
	managerSession := manager.cookies["pcclub_session"]
	if !managerSession.HttpOnly || managerSession.SameSite != http.SameSiteLaxMode {
		t.Fatal("insecure session cookie")
	}
	manager.call("GET", "/api/v1/auth/me", nil, 200) // remote manager, no device
	admin := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Тестовый админ", "roles": []string{"admin"}, "pin": "123456"}, 201)
	adminID := admin["id"].(string)
	if _, ok := admin["pin_hash"]; ok {
		t.Fatal("PIN hash exposed")
	}
	manager.call("PATCH", "/api/v1/users/"+managerID, map[string]any{"display_name": "Менеджер", "active": false, "version": 1}, 422)
	manager.call("PATCH", "/api/v1/users/"+adminID, map[string]any{"display_name": "Админ", "active": true, "version": 999}, 409)
	enrollment := manager.call("POST", "/api/v1/devices/enrollment", map[string]any{"password": password}, 201)["code"].(string)
	club := newBrowser("192.0.2.2")
	club.call("GET", "/api/v1/auth/csrf", nil, 200)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": adminID, "pin": "123456"}, 403)
	club.call("POST", "/api/v1/devices/register", map[string]any{"code": enrollment, "label": "Синтетический браузер"}, 204)
	club.call("POST", "/api/v1/devices/register", map[string]any{"code": enrollment, "label": "Повтор"}, 403)
	club.call("GET", "/api/v1/auth/admin-options", nil, 200)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": adminID, "pin": "123456"}, 200)
	oldSession := club.cookies["pcclub_session"]
	oldCSRF := club.csrf
	club.call("GET", "/api/v1/users", nil, 403)
	// Polling must not extend idle expiry.
	var before, after time.Time
	if err := pool.QueryRow(ctx, "SELECT idle_expires_at FROM sessions WHERE token_hash=$1", auth.Digest(oldSession.Value)).Scan(&before); err != nil {
		t.Fatal(err)
	}
	club.call("GET", "/api/v1/auth/me", nil, 200)
	if err := pool.QueryRow(ctx, "SELECT idle_expires_at FROM sessions WHERE token_hash=$1", auth.Digest(oldSession.Value)).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !before.Equal(after) {
		t.Fatal("polling renewed idle expiry")
	}
	proof := club.call("POST", "/api/v1/auth/reauth", map[string]any{"pin": "123456", "action": "accept_handover", "target_id": adminID, "revision": 7}, 201)["proof"].(string)
	var proofActor, proofAction string
	var revision int
	if err := pool.QueryRow(ctx, "SELECT actor_id::text,action,revision FROM reauth_proofs WHERE token_hash=$1", auth.Digest(proof)).Scan(&proofActor, &proofAction, &revision); err != nil {
		t.Fatal(err)
	}
	if proofActor != adminID || proofAction != "accept_handover" || revision != 7 {
		t.Fatal("proof not bound")
	}

	if err := repo.Within(ctx, func(tx auth.Tx) error {
		p := auth.Proof{Hash: auth.Digest(proof), ActorID: adminID, Action: "accept_handover", TargetID: adminID, SessionHash: auth.Digest(oldSession.Value), Revision: 8}
		ok, err := tx.ConsumeProof(p)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("proof accepted for wrong revision")
		}
		p.Revision = 7
		p.Action = "collection"
		ok, err = tx.ConsumeProof(p)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("proof accepted for wrong action")
		}
		p.Action = "accept_handover"
		ok, err = tx.ConsumeProof(p)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatal("valid proof rejected")
		}
		ok, err = tx.ConsumeProof(p)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("proof reused")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Same browser switches to manager: old personal session must be invalid.
	club.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
	var revoked bool
	if err := pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM sessions WHERE token_hash=$1", auth.Digest(oldSession.Value)).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("old session survived user switch")
	}
	currentCSRF := club.csrf
	club.csrf = oldCSRF
	club.actor = adminID
	club.call("POST", "/api/v1/auth/activity", map[string]any{}, 403)
	club.csrf = currentCSRF
	club.actor = managerID
	club.call("POST", "/api/v1/auth/logout", map[string]any{}, 204)
	club.call("GET", "/api/v1/auth/csrf", nil, 200)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": adminID, "pin": "123456"}, 200)
	manager.call("POST", "/api/v1/users/"+adminID+"/reset-pin", map[string]any{"pin": "654321", "version": 1}, 204)
	club.call("GET", "/api/v1/auth/me", nil, 401)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": adminID, "pin": "654321"}, 200)
	var deviceID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM trusted_devices WHERE token_hash=$1", auth.Digest(club.cookies["pcclub_device"].Value)).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	manager.call("POST", "/api/v1/devices/"+deviceID+"/revoke", map[string]any{}, 204)
	club.call("GET", "/api/v1/auth/me", nil, 403)
	club.call("GET", "/api/v1/auth/admin-options", nil, 403)
	// Durable account lock cannot be bypassed by service restart or changing IP.
	attacker := newBrowser("192.0.2.50")
	attacker.call("GET", "/api/v1/auth/csrf", nil, 200)
	for range 5 {
		attacker.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": "incorrect-password"}, 401)
	}
	restarted := auth.New(repo, pepper)
	attacker.h = handler(restarted)
	attacker.source = "192.0.2.51"
	attacker.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 429)
	// Existing manager session still works, and last-manager rule is intact.
	manager.call("GET", "/api/v1/auth/me", nil, 200)
	// Audit write failure must rollback user creation, never report success.
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT test_audit_failure CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Не должен сохраниться", "roles": []string{"admin"}, "pin": "111111"}, 503)
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE display_name='Не должен сохраниться'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("business write survived missing audit")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT test_audit_failure"); err != nil {
		t.Fatal(err)
	}
	var auditText string
	if err := pool.QueryRow(ctx, "SELECT string_agg(row_to_json(audit_events)::text,',') FROM audit_events").Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{password, "123456", "654321", enrollment, proof, managerSession.Value} {
		if strings.Contains(auditText, secret) {
			t.Fatal("secret in audit")
		}
	}
	// Expiration is enforced server-side and polling cannot revive it.
	if _, err := pool.Exec(ctx, "UPDATE sessions SET idle_expires_at=now()-interval '1 second' WHERE token_hash=$1", auth.Digest(manager.cookies["pcclub_session"].Value)); err != nil {
		t.Fatal(err)
	}
	manager.call("GET", "/api/v1/auth/me", nil, 401)
}
