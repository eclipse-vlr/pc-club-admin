//go:build integration

package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/storage/postgres"
)

func TestRevocationInvalidatesOutstandingPINProof(t *testing.T) {
	// Keep Argon2 fixture setup sequential to bound memory in the race suite.
	for _, scenario := range []string{"pin_reset", "user_disabled", "device_revoked", "idle_expired", "absolute_expired"} {
		t.Run(scenario, func(t *testing.T) {
			pool := authDB(t)
			ctx := context.Background()
			repo := postgres.AuthRepository{Pool: pool}
			access := auth.New(repo, "synthetic-revocation-pepper-at-least-32-characters")
			password := "synthetic-revocation-manager-password"
			if err := access.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
				t.Fatal(err)
			}
			c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
			handler := httpapi.New(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access)
			manager := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.140"}
			club := &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: "192.0.2.141"}
			manager.call("GET", "/api/v1/auth/csrf", nil, 200)
			manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
			userID := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Синтетический сотрудник", "roles": []string{"admin"}, "pin": "123456"}, 201)["id"].(string)
			register := func() {
				t.Helper()
				code := manager.call("POST", "/api/v1/devices/enrollment", map[string]any{"password": password}, 201)["code"]
				club.call("GET", "/api/v1/auth/csrf", nil, 200)
				club.call("POST", "/api/v1/devices/register", map[string]any{"code": code, "label": "Тест отзыва"}, 204)
			}
			register()
			club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": userID, "pin": "123456"}, 200)
			sessionHash := auth.Digest(club.cookies["pcclub_session"].Value)
			issueProof := func() string {
				t.Helper()
				return club.call("POST", "/api/v1/auth/reauth", map[string]any{"pin": "123456", "action": "collection", "target_id": userID, "revision": 1}, 201)["proof"].(string)
			}
			consume := func(token string) bool {
				t.Helper()
				var used bool
				err := repo.Within(ctx, func(tx auth.Tx) error {
					var err error
					used, err = tx.ConsumeProof(auth.Proof{Hash: auth.Digest(token), ActorID: userID, Action: "collection", TargetID: userID, Revision: 1, SessionHash: sessionHash})
					return err
				})
				if err != nil {
					t.Fatalf("ConsumeProof(%s) error = %v, want nil", scenario, err)
				}
				return used
			}
			if !consume(issueProof()) {
				t.Fatal("ConsumeProof(active session) = false, want true")
			}
			pending := issueProof()
			wantStatus, wantCode, nextPIN := 401, "AUTH_REQUIRED", "123456"
			switch scenario {
			case "pin_reset":
				manager.call("POST", "/api/v1/users/"+userID+"/reset-pin", map[string]any{"pin": "654321", "version": 1}, 204)
				nextPIN = "654321"
			case "user_disabled":
				manager.call("PATCH", "/api/v1/users/"+userID, map[string]any{"display_name": "Синтетический сотрудник", "active": false, "version": 1}, 200)
			case "device_revoked":
				var deviceID string
				if err := pool.QueryRow(ctx, "SELECT id::text FROM trusted_devices WHERE token_hash=$1", auth.Digest(club.cookies["pcclub_device"].Value)).Scan(&deviceID); err != nil {
					t.Fatal(err)
				}
				manager.call("POST", "/api/v1/devices/"+deviceID+"/revoke", map[string]any{}, 204)
				wantStatus, wantCode = 403, "DEVICE_REQUIRED"
			case "idle_expired":
				if _, err := pool.Exec(ctx, "UPDATE sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", sessionHash); err != nil {
					t.Fatal(err)
				}
			case "absolute_expired":
				if _, err := pool.Exec(ctx, "UPDATE sessions SET absolute_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", sessionHash); err != nil {
					t.Fatal(err)
				}
			}
			// A stale tab retains valid CSRF and actor values; server-side state must win.
			for _, route := range []string{"/api/v1/auth/activity", "/api/v1/auth/reauth"} {
				body := map[string]any{}
				if route == "/api/v1/auth/reauth" {
					body = map[string]any{"pin": nextPIN, "action": "collection", "target_id": userID, "revision": 1}
				}
				result := club.call("POST", route, body, wantStatus)
				if got := result["error"].(map[string]any)["code"]; got != wantCode {
					t.Errorf("POST %s after %s code = %v, want %s", route, scenario, got, wantCode)
				}
			}
			if consume(pending) {
				t.Errorf("ConsumeProof(after %s) = true, want false", scenario)
			}
			if scenario == "user_disabled" {
				manager.call("PATCH", "/api/v1/users/"+userID, map[string]any{"display_name": "Синтетический сотрудник", "active": true, "version": 2}, 200)
				club.call("GET", "/api/v1/auth/me", nil, 401)
			}
			if scenario == "device_revoked" {
				register()
			}
			if scenario == "pin_reset" {
				club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": userID, "pin": "123456"}, 401)
			}
			club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": userID, "pin": nextPIN}, 200)
			club.call("GET", "/api/v1/auth/me", nil, 200)
			manager.call("GET", "/api/v1/auth/me", nil, 200)
			if consume(pending) {
				t.Errorf("ConsumeProof(old proof after fresh login, %s) = true, want false", scenario)
			}
			var unused bool
			var authors int
			if err := pool.QueryRow(ctx, "SELECT used_at IS NULL FROM reauth_proofs WHERE token_hash=$1", auth.Digest(pending)).Scan(&unused); err != nil {
				t.Fatal(err)
			}
			if !unused {
				t.Errorf("Outstanding proof after %s was consumed, want unused", scenario)
			}
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='reauth' AND success AND actor_id=$1", userID).Scan(&authors); err != nil {
				t.Fatal(err)
			}
			if authors != 2 {
				t.Errorf("Committed reauthorship after %s = %d events, want 2 with original actor", scenario, authors)
			}
		})
	}
}
