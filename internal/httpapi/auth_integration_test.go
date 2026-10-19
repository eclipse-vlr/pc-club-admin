//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
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
	key                 string
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
	if b.key != "" {
		req.Header.Set("Idempotency-Key", b.key)
	}
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

func TestOpeningChecklistBoundary(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	repo := postgres.AuthRepository{Pool: pool}
	access := auth.New(repo, "synthetic-stage3-pepper-at-least-32-characters")
	password := "synthetic-manager-password"
	if err := access.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	c := config.Config{Environment: "test", Origin: "http://127.0.0.1:8080", Timezone: "Europe/Moscow", CashPolicyStatus: "unverified"}
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, false)
	handler := httpapi.NewWithShifts(c, func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, work)
	newBrowser := func(source string) *browser {
		return &browser{t: t, h: handler, cookies: map[string]*http.Cookie{}, source: source}
	}
	manager := newBrowser("192.0.2.60")
	manager.call("GET", "/api/v1/auth/csrf", nil, 200)
	manager.call("POST", "/api/v1/auth/manager-login", map[string]any{"login": "manager", "password": password}, 200)
	admin := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Тестовый администратор", "roles": []string{"admin"}, "pin": "123456"}, 201)
	owner := admin["id"].(string)
	other := manager.call("POST", "/api/v1/users", map[string]any{"display_name": "Другой администратор", "roles": []string{"admin"}, "pin": "654321"}, 201)
	input := map[string]any{"owner_id": owner, "business_date": "2026-10-19", "kind": "night", "reason": "Синтетический первоначальный пересчёт"}
	manager.key = "synthetic-bootstrap-key-001"
	manager.call("POST", "/api/v1/shifts/bootstrap", input, 400) // nil does not become zero
	input["opening_cash_kopecks"] = 0
	draft := manager.call("POST", "/api/v1/shifts/bootstrap", input, 201)
	shiftID := draft["id"].(string)
	if draft["status"] != "opening" || draft["actual_start_at"] != nil {
		t.Fatal("draft assigned responsibility")
	}
	entries := draft["checklist"].([]any)
	acceptCount, closeCount := 0, 0
	for _, v := range entries {
		e := v.(map[string]any)
		if e["answer"] != "unanswered" {
			t.Fatal("auto-filled answer")
		}
		if e["phase"] == "acceptance" {
			acceptCount++
		} else {
			closeCount++
		}
	}
	if acceptCount != 12 || closeCount != 19 {
		t.Fatalf("wrong seed: %d/%d", acceptCount, closeCount)
	}
	replay := manager.call("POST", "/api/v1/shifts/bootstrap", input, 201)
	if replay["id"] != shiftID {
		t.Fatal("duplicate shift on retry")
	}
	input["opening_cash_kopecks"] = 1
	manager.call("POST", "/api/v1/shifts/bootstrap", input, 409)
	input["opening_cash_kopecks"] = 0
	manager.key = "synthetic-bootstrap-key-002"
	manager.call("POST", "/api/v1/shifts/bootstrap", input, 409)
	manager.key = ""
	manager.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_d", map[string]any{"answer": "yes", "version": 1}, 403)
	manager.call("PATCH", "/api/v1/users/"+owner, map[string]any{"display_name": "Тест", "active": false, "version": 1}, 422)
	code := manager.call("POST", "/api/v1/devices/enrollment", map[string]any{"password": password}, 201)["code"].(string)
	club := newBrowser("192.0.2.61")
	club.call("GET", "/api/v1/auth/csrf", nil, 200)
	club.call("POST", "/api/v1/devices/register", map[string]any{"code": code, "label": "Тестовый браузер"}, 204)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": other["id"], "pin": "654321"}, 200)
	club.call("GET", "/api/v1/shifts/"+shiftID, nil, 404)
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_d", map[string]any{"answer": "yes", "version": 1}, 403)
	club.call("POST", "/api/v1/auth/pin-login", map[string]any{"user_id": owner, "pin": "123456"}, 200)
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_o", map[string]any{"answer": "—", "version": 1}, 400)
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/close_p", map[string]any{"answer": "yes", "version": 1}, 409)
	for _, v := range entries {
		e := v.(map[string]any)
		if e["phase"] == "acceptance" && e["code"] != "accept_o" {
			club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/"+e["code"].(string), map[string]any{"answer": "yes", "version": 1}, 200)
		}
	}
	ready := club.call("GET", "/api/v1/shifts/"+shiftID+"/readiness", nil, 200)
	progress := ready["acceptance"].(map[string]any)
	if progress["completed"] != float64(11) || progress["total"] != float64(12) || ready["can_activate"] != false {
		t.Fatalf("T01 readiness: %+v", ready)
	}
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_d", map[string]any{"answer": "no", "version": 1}, 409)
	equipment := club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_i", map[string]any{"answer": "yes", "equipment_issue": true, "version": 2}, 200)
	_ = equipment
	ready = club.call("GET", "/api/v1/shifts/"+shiftID+"/readiness", nil, 200)
	raw, _ := json.Marshal(ready)
	if !strings.Contains(string(raw), "NOTE_REQUIRED") {
		t.Fatal("equipment note not required")
	}
	club.call("POST", "/api/v1/shifts/"+shiftID+"/notes", map[string]any{"checklist_code": "accept_i", "category": "equipment", "object_label": "ПК-01", "text": "Не включается. Ремонт ещё не выполнен"}, 201)
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_o", map[string]any{"answer": "yes", "version": 1}, 200)
	ready = club.call("GET", "/api/v1/shifts/"+shiftID+"/readiness", nil, 200)
	raw, _ = json.Marshal(ready)
	if strings.Contains(string(raw), "NOTE_REQUIRED") || ready["acceptance"].(map[string]any)["completed"] != float64(12) || ready["can_prepare_handover"] != false {
		t.Fatalf("T06: %+v", ready)
	}
	// Publish a later source version: current snapshot must retain its original text.
	if _, err := pool.Exec(ctx, `INSERT INTO checklist_template_versions(version,published) VALUES(2,true); INSERT INTO checklist_template_items SELECT 2,code,phase,'Изменённый текст',help_text,allowed_answers,required,sort_order,equipment_check FROM checklist_template_items WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	unchanged := club.call("GET", "/api/v1/shifts/"+shiftID, nil, 200)
	if unchanged["template_version"] != float64(1) || unchanged["checklist"].([]any)[0].(map[string]any)["label"] != entries[0].(map[string]any)["label"] {
		t.Fatal("T25 snapshot changed")
	}
	// Test-only active fixture to exercise closing rules; the app has no activation endpoint yet.
	if _, err := pool.Exec(ctx, "UPDATE shifts SET status='active',actual_start_at=planned_start_at WHERE id=$1", shiftID); err != nil {
		t.Fatal(err)
	}
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/accept_o", map[string]any{"answer": "no", "version": 2}, 409)
	for _, v := range entries {
		e := v.(map[string]any)
		if e["phase"] == "closing" {
			answer := "yes"
			if e["code"] == "conflicts_recorded" {
				answer = "no"
			}
			club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/"+e["code"].(string), map[string]any{"answer": answer, "version": 1}, 200)
		}
	}
	ready = club.call("GET", "/api/v1/shifts/"+shiftID+"/readiness", nil, 200)
	if ready["closing"].(map[string]any)["completed"] != float64(19) || len(ready["closing"].(map[string]any)["blockers"].([]any)) != 0 {
		t.Fatalf("T03: %+v", ready)
	}
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/conflicts_recorded", map[string]any{"answer": "yes", "version": 2}, 200)
	ready = club.call("GET", "/api/v1/shifts/"+shiftID+"/readiness", nil, 200)
	raw, _ = json.Marshal(ready)
	if !strings.Contains(string(raw), "NOTE_REQUIRED") {
		t.Fatal("T05 conflict note not required")
	}
	club.call("POST", "/api/v1/shifts/"+shiftID+"/notes", map[string]any{"checklist_code": "conflicts_recorded", "category": "conflict", "text": "Синтетический конфликт записан"}, 201)
	// Refusing the audit INSERT must also refuse the answer update and its version bump.
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT reject_shift_audit CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/close_p", map[string]any{"answer": "no", "version": 2}, 503)
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT reject_shift_audit"); err != nil {
		t.Fatal(err)
	}
	var answer string
	var author string
	var version int
	if err := pool.QueryRow(ctx, "SELECT answer,answered_by::text,version FROM shift_checklist_entries WHERE shift_id=$1 AND code='close_p'", shiftID).Scan(&answer, &author, &version); err != nil {
		t.Fatal(err)
	}
	if answer != "yes" || version != 2 || author != owner {
		t.Fatal("answer or attribution survived failed audit")
	}
	if _, err := pool.Exec(ctx, "UPDATE shifts SET status='closed',actual_end_at=planned_end_at WHERE id=$1", shiftID); err != nil {
		t.Fatal(err)
	}
	club.call("PATCH", "/api/v1/shifts/"+shiftID+"/checklist/close_p", map[string]any{"answer": "no", "version": 2}, 409)
	// Production blocks draft mutations even before operational endpoints exist.
	production := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, true)
	manager.h = httpapi.NewWithShifts(c, func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)), access, production)
	manager.key = "production-bootstrap-key"
	manager.call("POST", "/api/v1/shifts/bootstrap", input, 503)
}

func TestConcurrentOpeningAndAnswer(t *testing.T) {
	pool := authDB(t)
	ctx := context.Background()
	access := auth.New(postgres.AuthRepository{Pool: pool}, "synthetic-concurrency-pepper-32-characters")
	password := "synthetic-manager-password"
	if err := access.Bootstrap(ctx, auth.UserInput{Name: "Тестовый управляющий", Login: "manager", Password: password, Roles: []string{"manager"}}); err != nil {
		t.Fatal(err)
	}
	login, err := access.Login(ctx, auth.Request{Source: "192.0.2.70", RequestID: "test-login"}, "manager", password, false)
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: login.Identity.User.ID, Source: "192.0.2.70", RequestID: "test-manager"}
	owner, err := access.CreateUser(ctx, manager, auth.UserInput{Name: "Тестовый администратор", Roles: []string{"admin"}, PIN: "123456"})
	if err != nil {
		t.Fatal(err)
	}
	service := shifts.New(postgres.ShiftRepository{Pool: pool}, access, "Europe/Moscow", false)
	zero := int64(0)
	input := shifts.BootstrapInput{OwnerID: owner.ID, BusinessDate: "2026-10-19", Kind: "day", InitialCash: &zero, Reason: "Синтетический тест"}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT reject_bootstrap_audit CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Bootstrap(ctx, manager, input, "failed-bootstrap-key"); err == nil {
		t.Fatal("bootstrap succeeded without audit")
	}
	var rolledBack int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM shifts)+(SELECT count(*) FROM idempotency_records)").Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack != 0 {
		t.Fatal("bootstrap or idempotency result survived failed audit")
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT reject_bootstrap_audit"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		shift  shifts.Shift
		replay bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			s, replay, err := service.Bootstrap(ctx, manager, input, "concurrent-bootstrap-key")
			results <- result{s, replay, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.shift.ID != second.shift.ID || first.replay == second.replay {
		t.Fatalf("concurrent bootstrap: %+v %+v", first, second)
	}
	shiftID := first.shift.ID
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='shift_bootstrapped'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("duplicate bootstrap audit")
	}
	enrollment, err := access.Enroll(ctx, manager, password)
	if err != nil {
		t.Fatal(err)
	}
	device, err := access.RegisterDevice(ctx, auth.Request{Source: "192.0.2.71"}, enrollment, "Test browser")
	if err != nil {
		t.Fatal(err)
	}
	login, err = access.Login(ctx, auth.Request{DeviceToken: device, Source: "192.0.2.71"}, owner.ID, "123456", true)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Request{SessionToken: login.Token, CSRF: login.CSRF, ActorID: owner.ID, DeviceToken: device, Source: "192.0.2.71", RequestID: "test-answer"}
	start = make(chan struct{})
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := service.Answer(ctx, admin, shiftID, "accept_d", shifts.AnswerInput{Answer: "yes", Version: 1})
			errorsChannel <- err
		}()
	}
	close(start)
	e1, e2 := <-errorsChannel, <-errorsChannel
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("expected one conflict: %v %v", e1, e2)
	}
	conflict := e1
	if conflict == nil {
		conflict = e2
	}
	failure, ok := conflict.(*auth.Failure)
	if !ok || failure.Code != "VERSION_CONFLICT" {
		t.Fatalf("unexpected conflict: %v", conflict)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='checklist_answered'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("duplicate successful answer audit")
	}
	// Check the actual DB invariant before a future activate/accept implementation exists.
	if _, err := pool.Exec(ctx, "UPDATE shifts SET status='active',actual_start_at=planned_start_at WHERE id=$1", shiftID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO shifts(owner_id,business_date,kind,status,planned_start_at,planned_end_at,actual_start_at,template_version,created_by) SELECT owner_id,business_date,kind,'active',planned_start_at,planned_end_at,planned_start_at,template_version,created_by FROM shifts WHERE id=$1`, shiftID)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.ConstraintName != "one_responsible_shift" {
		t.Fatalf("expected one_responsible_shift violation, got %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM shifts WHERE status IN ('active','handover_pending')").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("responsibility invariant lost")
	}
}
